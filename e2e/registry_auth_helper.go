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
	"context"
	"fmt"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/klog/v2"
)

const (
	// registryAuthFileEnvVar names a docker auth file holding pull secrets for
	// the registries serving the release payload under test.
	registryAuthFileEnvVar = "REGISTRY_AUTH_FILE"

	// kubeConfigEnvVar names the kubeconfig of the cluster under test.
	kubeConfigEnvVar = "KUBECONFIG"

	// clusterProfilePullSecretPath is the conventional location of the CI
	// cluster profile pull secret inside a CI job pod.
	clusterProfilePullSecretPath = "/run/secrets/ci.openshift.io/cluster-profile/pull-secret" //nolint:gosec // This is a file path, not a credential
)

// RegistryAuthClientFunc builds the client needed to read the cluster's own
// pull secret. It is called only if the credential ladder reaches that source,
// so a caller whose credentials come from the environment never needs a usable
// kubeconfig: it may return (nil, nil) when there is no cluster to talk to, and
// the ladder then falls through to anonymous access.
type RegistryAuthClientFunc func() (corev1client.CoreV1Interface, error)

// determineRegistryAuthFilePath returns the path of a docker auth file that oc
// can use, via --registry-config, to read the release payload and the component
// images it references, or "" when they must be read anonymously.
//
// Reading a release needs it because the CI mirrors serve payload metadata
// anonymously but not component image blobs: oc adm release info succeeds
// without credentials while oc image extract of a component image fails with
// "unauthorized: authentication required". Credentials are resolved in order:
//
//  1. $REGISTRY_AUTH_FILE, when set.
//  2. The CI cluster profile pull secret, when present and readable.
//  3. The cluster's own pull secret, openshift-config/pull-secret, copied into
//     tmpDir. MicroShift does not serve that secret, hence client may be nil or
//     return a nil client.
//  4. No credentials, so the caller must read the images anonymously.
//
// client is lazy because sources 1 and 2 are exactly the sources available when
// the cluster under test is unreachable: building a client eagerly would fail
// before they are consulted.
//
// This is a copy of DetermineAuthFilePath in origin's
// test/shared/registryauth package, which is being moved there from
// pkg/test/extensions/util.go. The copy exists so
// https://github.com/openshift/cluster-capi-operator/pull/709 is not blocked on
// that move: once it lands, replace this with the shared function.
func determineRegistryAuthFilePath(ctx context.Context, client RegistryAuthClientFunc, tmpDir string) (string, error) {
	return registryAuthFilePathFrom(ctx, client, tmpDir, clusterProfilePullSecretPath)
}

// registryAuthFilePathFrom implements determineRegistryAuthFilePath with the
// cluster profile pull secret path injectable so the ladder can be tested.
func registryAuthFilePathFrom(ctx context.Context, client RegistryAuthClientFunc, tmpDir, profilePullSecretPath string) (string, error) {
	if authFile := os.Getenv(registryAuthFileEnvVar); authFile != "" {
		return authFile, nil
	}

	switch _, err := os.Stat(profilePullSecretPath); {
	case err == nil:
		return profilePullSecretPath, nil
	case !os.IsNotExist(err):
		// Fall through instead of selecting a path that cannot be read: oc
		// would fail to load it and every image operation would fail with it.
		klog.V(2).InfoS("Ignoring unreadable CI cluster profile pull secret",
			"path", profilePullSecretPath, "err", err)
	}

	if client == nil {
		return "", nil
	}

	coreV1, err := client()
	if err != nil {
		return "", fmt.Errorf("unable to create client for the cluster under test: %w", err)
	}

	if coreV1 == nil {
		return "", nil
	}

	pullSecret, err := coreV1.Secrets("openshift-config").Get(ctx, "pull-secret", metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}

		return "", fmt.Errorf("reading secret openshift-config/pull-secret: %w", err)
	}

	authFile := filepath.Join(tmpDir, ".dockerconfigjson")
	if err := os.WriteFile(authFile, pullSecret.Data[corev1.DockerConfigJsonKey], 0600); err != nil {
		return "", fmt.Errorf("writing secret openshift-config/pull-secret to %s: %w", authFile, err)
	}

	return authFile, nil
}

// ocAuthArgs returns the global oc flags that let oc reach the cluster under
// test and pull from the registries serving its release payload. The registry
// auth flag is omitted when no credentials were found, which leaves oc reading
// the images anonymously.
func ocAuthArgs(registryAuthFile string) []string {
	var args []string

	if kubeconfig := os.Getenv(kubeConfigEnvVar); kubeconfig != "" {
		args = append(args, "--kubeconfig="+kubeconfig)
	}

	if registryAuthFile != "" {
		args = append(args, "--registry-config="+registryAuthFile)
	}

	return args
}
