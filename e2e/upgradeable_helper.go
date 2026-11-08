// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"sigs.k8s.io/yaml"
)

// clustersCRDFromManifests returns the clusters.cluster.x-k8s.io
// CustomResourceDefinition from a multi-document provider manifests.yaml,
// such as /capi-operator-manifests/<profile>/manifests.yaml shipped in the
// cluster-capi-controllers image. Document order is irrelevant and
// non-CRD documents are skipped.
func clustersCRDFromManifests(manifestsYAML []byte) (*apiextensionsv1.CustomResourceDefinition, error) {
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(manifestsYAML)))
	for {
		document, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("splitting manifests into YAML documents: %w", err)
		}
		if len(bytes.TrimSpace(document)) == 0 {
			continue
		}

		var typeMeta metav1.TypeMeta
		if err := yaml.Unmarshal(document, &typeMeta); err != nil {
			return nil, fmt.Errorf("parsing YAML document: %w", err)
		}
		if typeMeta.Kind != "CustomResourceDefinition" {
			continue
		}

		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.Unmarshal(document, &crd); err != nil {
			return nil, fmt.Errorf("decoding CustomResourceDefinition document: %w", err)
		}
		if crd.Name == clusterCRDName {
			return &crd, nil
		}
	}

	return nil, fmt.Errorf("no CustomResourceDefinition named %q found in %d bytes of manifests", clusterCRDName, len(manifestsYAML))
}

// nextMinorVersion returns the next-minor target version X.(Y+1).0 for an
// OCP version X.Y.Z[-prerelease], e.g. "5.0.0-0.nightly-2026-10-30-023658"
// or "4.18.2" both map to a Y+1 target of "5.1.0" and "4.19.0"
// respectively. The patch and prerelease components are ignored.
func nextMinorVersion(version string) (string, error) {
	components := strings.Split(version, ".")
	if len(components) < 3 {
		return "", fmt.Errorf("version %q does not match X.Y.Z[-prerelease]", version)
	}

	major, err := strconv.Atoi(components[0])
	if err != nil {
		return "", fmt.Errorf("major version in %q is not a number", version)
	}
	minor, err := strconv.Atoi(components[1])
	if err != nil {
		return "", fmt.Errorf("minor version in %q is not a number", version)
	}

	return fmt.Sprintf("%d.%d.0", major, minor+1), nil
}

// graphServerImageComponent is the payload component whose image serves the
// fake update graph. The fixture serves the graph with "python3 -m
// http.server", so the image must contain python3: the cli image is built
// from the OCP base image, which installs python3 and
// python-unversioned-command.
const graphServerImageComponent = "cli"

// registryAuthClient resolves the client determineRegistryAuthFilePath uses to
// read the cluster's own pull secret. It returns a nil client when there is no
// cluster to talk to, so the credential ladder falls through to anonymous
// access.
func registryAuthClient() (corev1client.CoreV1Interface, error) {
	if restConfig == nil {
		return nil, nil
	}

	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("building a clientset to resolve registry credentials: %w", err)
	}

	return kubeClient.CoreV1(), nil
}

// resolvePayloadComponentImage resolves component of releaseImage to the
// digest-pinned pull spec of its image, for a pod on the cluster under test.
//
// The pod pulls the result through the cluster's own mirror configuration,
// which is the only image source that also works on a disconnected cluster: the
// payload ImageStreams in the internal registry are never imported there, so
// the internal registry cannot serve the fixture image.
func resolvePayloadComponentImage(ctx context.Context, releaseImage, component string) (string, error) {
	authDir, err := os.MkdirTemp("", "capi-operator-registry-auth-")
	if err != nil {
		return "", fmt.Errorf("creating temp dir for registry credentials: %w", err)
	}
	defer func() { _ = os.RemoveAll(authDir) }()

	registryAuthFile, err := determineRegistryAuthFilePath(ctx, registryAuthClient, authDir)
	if err != nil {
		return "", fmt.Errorf("resolving registry credentials for release images: %w", err)
	}

	args := []string{"adm", "release", "info", releaseImage, "--image-for=" + component}

	imageRef, err := runOC(ctx, append(args, ocAuthArgs(registryAuthFile)...)...)
	if err != nil {
		return "", fmt.Errorf("resolving the %s image of release %q: %w", component, releaseImage, err)
	}

	// A tag defaults imagePullPolicy to Always and lets the reference drift
	// under the fixture; the payload is immutable only by digest.
	if !strings.Contains(imageRef, "@sha256:") {
		return "", fmt.Errorf("resolved %s image %q of release %q is not digest-pinned", component, imageRef, releaseImage)
	}

	return imageRef, nil
}

const (
	// graphServerImageStreamTag is the payload-provided ImageStreamTag whose
	// image serves the fake update graph. The tools image contains python3,
	// which the fixture needs, and it is part of the payload, so the reference
	// the cluster records for it names the payload's own registry: the pod's
	// pull is rewritten to the cluster's mirror, which works on disconnected
	// and dev-scripts clusters alike.
	graphServerImageStreamTag          = "tools:latest"
	graphServerImageStreamTagNamespace = "openshift"
)

// resolveGraphServerImage returns the digest-pinned image the fixture pod runs.
//
// The cluster records the payload image of graphServerImageTag, so resolving it
// needs no release image dereference: on dev-scripts the current payload names
// a cluster-internal mirror that the test host cannot resolve. Resolving the
// current payload's cli image is the fallback for clusters without the tag.
func resolveGraphServerImage(ctx context.Context, currentPayload string) (string, error) {
	imageRef, err := runOC(ctx, "get", "imagestreamtag", graphServerImageStreamTag, "--namespace", graphServerImageStreamTagNamespace, "-o", "jsonpath={.image.dockerImageReference}")
	if err == nil {
		imageRef = strings.TrimSpace(imageRef)
		if strings.Contains(imageRef, "@sha256:") {
			return imageRef, nil
		}

		err = fmt.Errorf("%s/%s records %q, which is not digest-pinned", graphServerImageStreamTagNamespace, graphServerImageStreamTag, imageRef)
	}

	fallback, fallbackErr := resolvePayloadComponentImage(ctx, currentPayload, graphServerImageComponent)
	if fallbackErr != nil {
		return "", errors.Join(fmt.Errorf("resolving %s/%s: %w", graphServerImageStreamTagNamespace, graphServerImageStreamTag, err), fallbackErr)
	}

	return fallback, nil
}

// clustersCRDFromReleaseImage extracts the clusters.cluster.x-k8s.io CRD
// from the default-profile provider manifests shipped in the
// cluster-capi-controllers image referenced by releaseImage. It shells out
// to oc, which must be present in PATH and able to pull the component images
// of the payload: payload metadata is served anonymously but component image
// blobs are not, so the cluster pull secret is resolved and passed as
// --registry-config.
func clustersCRDFromReleaseImage(ctx context.Context, releaseImage string) (*apiextensionsv1.CustomResourceDefinition, error) {
	extractDir, err := os.MkdirTemp("", "capi-operator-manifests-")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir for manifest extraction: %w", err)
	}
	defer func() { _ = os.RemoveAll(extractDir) }()

	registryAuthFile, err := determineRegistryAuthFilePath(ctx, registryAuthClient, extractDir)
	if err != nil {
		return nil, fmt.Errorf("resolving registry credentials for release images: %w", err)
	}

	ocAuth := ocAuthArgs(registryAuthFile)

	releaseInfoArgs := []string{"adm", "release", "info", releaseImage, "--image-for=cluster-capi-controllers"}

	imageRef, err := runOC(ctx, append(releaseInfoArgs, ocAuth...)...)
	if err != nil {
		return nil, fmt.Errorf("resolving the cluster-capi-controllers image of release %q: %w", releaseImage, err)
	}

	extractArgs := []string{"image", "extract", imageRef,
		"--path", "/capi-operator-manifests/default/manifests.yaml:" + extractDir}

	if _, err := runOC(ctx, append(extractArgs, ocAuth...)...); err != nil {
		return nil, fmt.Errorf("extracting provider manifests from image %q: %w", imageRef, err)
	}

	manifestsYAML, err := os.ReadFile(filepath.Join(extractDir, "manifests.yaml"))
	if err != nil {
		return nil, fmt.Errorf("reading extracted manifests from %s: %w", extractDir, err)
	}

	return clustersCRDFromManifests(manifestsYAML)
}

// runOC runs an oc command and returns its stdout. Errors name the command,
// its exit code and its stderr so failures are debuggable from the error
// alone.
func runOC(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "oc", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("oc binary not found in PATH, required to inspect release images: %w", err)
		}
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		return "", fmt.Errorf("oc %s failed with exit code %d: %s", strings.Join(args, " "), exitCode, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}
