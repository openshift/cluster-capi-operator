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

	. "github.com/onsi/ginkgo/v2"

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

const (
	// releaseImageLATESTEnvVar names the release image CI provided for this
	// job. It is the interim update target whenever CI supplies no Y+1
	// payload: it lets the suite run everywhere without a second payload,
	// and it is the payload dev-scripts mirrors for the cluster, so both
	// host and in-cluster pulls resolve it.
	releaseImageLATESTEnvVar = "RELEASE_IMAGE_LATEST"

	// clusterCAPIComponent is the payload component shipping the Cluster
	// API provider manifests the preflight extracts the fixture CRD from.
	clusterCAPIComponent = "cluster-capi-controllers"
)

// errNoUpdateTarget reports that CI provided no release image for the
// preflight to assess.
var errNoUpdateTarget = errors.New("no update target release image provided")

// resolveUpdateTargetRelease returns the release image the upgradeability
// preflight assesses. It is resolved exactly once:
//   - OPENSHIFT_AVAILABLE_UPDATE when CI supplies an explicit target payload.
//   - otherwise RELEASE_IMAGE_LATEST, the release image CI provided for
//     this job..
//
// Whichever source is chosen, the target image must be pullable both from the
// test runner and in-cluster. Specifically, if the cluster uses mirroring, this
// payload image must have been mirrored to the target cluster.
func resolveUpdateTargetRelease() (ref string, err error) {
	updateSource := upgradeTargetEnvVar
	ref = strings.TrimSpace(os.Getenv(updateSource))
	if ref == "" {
		updateSource = releaseImageLATESTEnvVar
		ref = strings.TrimSpace(os.Getenv(updateSource))
	}

	if ref == "" {
		return "", errNoUpdateTarget
	}

	// A tag would drift under the preflight; a release is immutable only by
	// digest.
	if !strings.Contains(ref, "@sha256:") {
		return "", fmt.Errorf("%s is %q, which is not digest-pinned: set it to a digest-pinned release image", updateSource, ref)
	}

	GinkgoWriter.Printf("Using target release image from %s: %s\n", updateSource, ref)

	return ref, nil
}

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
// digest-pinned pull spec of its image. releaseImage is always a ref the
// environment supplied explicitly (OPENSHIFT_AVAILABLE_UPDATE or
// RELEASE_IMAGE_LATEST): the cluster's own status.desired.image may name a
// cluster-internal mirror the test host cannot resolve, so it is never
// dereferenced.
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

// resolveGraphServerImage returns the digest-pinned tools image the fixture
// pod runs. The fixture needs python3, which only the tools image provides.
// Failing here is intentional: substituting another payload image produces a
// crash-looping pod instead of a diagnosable error.
func resolveGraphServerImage(ctx context.Context) (string, error) {
	imageRef, err := runOC(ctx, "get", "imagestreamtag", graphServerImageStreamTag,
		"--namespace", graphServerImageStreamTagNamespace,
		"-o", "jsonpath={.image.dockerImageReference}")
	if err != nil {
		return "", fmt.Errorf("resolving %s/%s: %w", graphServerImageStreamTagNamespace, graphServerImageStreamTag, err)
	}

	imageRef = strings.TrimSpace(imageRef)
	if !strings.Contains(imageRef, "@sha256:") {
		return "", fmt.Errorf("%s/%s records %q, which is not digest-pinned",
			graphServerImageStreamTagNamespace, graphServerImageStreamTag, imageRef)
	}

	return imageRef, nil
}

// clustersCRDFromReleaseImage extracts the clusters.cluster.x-k8s.io CRD
// from the default-profile provider manifests shipped in the
// cluster-capi-controllers image referenced by releaseImage.
func clustersCRDFromReleaseImage(ctx context.Context, releaseImage string) (*apiextensionsv1.CustomResourceDefinition, error) {
	imageRef, err := resolvePayloadComponentImage(ctx, releaseImage, clusterCAPIComponent)
	if err != nil {
		return nil, err
	}

	return clustersCRDFromImage(ctx, imageRef)
}

// clustersCRDFromImage extracts the clusters.cluster.x-k8s.io CRD from the
// default-profile provider manifests shipped in imageRef. It shells out to
// oc, which must be present in PATH and able to pull the image: payload
// metadata is served anonymously but image blobs are not, so the cluster
// pull secret is resolved and passed as --registry-config.
func clustersCRDFromImage(ctx context.Context, imageRef string) (*apiextensionsv1.CustomResourceDefinition, error) {
	extractDir, err := os.MkdirTemp("", "capi-operator-manifests-")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir for manifest extraction: %w", err)
	}
	defer func() { _ = os.RemoveAll(extractDir) }()

	registryAuthFile, err := determineRegistryAuthFilePath(ctx, registryAuthClient, extractDir)
	if err != nil {
		return nil, fmt.Errorf("resolving registry credentials for release images: %w", err)
	}

	extractArgs := []string{"image", "extract", imageRef,
		"--path", "/capi-operator-manifests/default/manifests.yaml:" + extractDir}

	if _, err := runOC(ctx, append(extractArgs, ocAuthArgs(registryAuthFile)...)...); err != nil {
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
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			exitCode = exitErr.ExitCode()
		}
		return "", fmt.Errorf("oc %s failed with exit code %d: %s", strings.Join(args, " "), exitCode, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}
