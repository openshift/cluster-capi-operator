//go:build !e2e

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
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	k8stesting "k8s.io/client-go/testing"
)

const pullSecretDockerConfigJSON = `{"auths":{"quay-proxy.ci.openshift.org":{"auth":"dGVzdDp0ZXN0"}}}`

// TestDetermineRegistryAuthFilePath exercises the credential ladder used to
// authenticate oc against the registries serving a release payload. Run
// without a cluster via:
//
//	go test -run TestDetermineRegistryAuthFilePath ./e2e/...
func TestDetermineRegistryAuthFilePath(t *testing.T) {
	t.Run("prefers REGISTRY_AUTH_FILE", func(t *testing.T) {
		g := gomega.NewWithT(t)

		t.Setenv(registryAuthFileEnvVar, "/run/credentials/auth.json")

		authFile, err := determineRegistryAuthFilePath(context.Background(), noClientFunc(t), t.TempDir())
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(authFile).To(gomega.Equal("/run/credentials/auth.json"))
	})

	t.Run("falls back to the CI cluster profile pull secret", func(t *testing.T) {
		g := gomega.NewWithT(t)

		t.Setenv(registryAuthFileEnvVar, "")
		profilePullSecret := filepath.Join(t.TempDir(), "pull-secret")
		writeFile(t, profilePullSecret)

		authFile, err := registryAuthFilePathFrom(context.Background(), noClientFunc(t), t.TempDir(), profilePullSecret)
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(authFile).To(gomega.Equal(profilePullSecret),
			"the cluster profile pull secret should be used before the cluster's own pull secret")
	})

	t.Run("falls through an unreadable CI cluster profile pull secret", func(t *testing.T) {
		g := gomega.NewWithT(t)

		t.Setenv(registryAuthFileEnvVar, "")
		tmpDir := t.TempDir()

		// A path whose parent is a file cannot be stat'd (ENOTDIR): the ladder
		// must fall through to the cluster pull secret rather than hand oc a
		// path it cannot read.
		parent := filepath.Join(t.TempDir(), "not-a-directory")
		writeFile(t, parent)

		authFile, err := registryAuthFilePathFrom(context.Background(), staticClient(clusterWithPullSecret().CoreV1()),
			tmpDir, filepath.Join(parent, "pull-secret"))
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(authFile).To(gomega.Equal(filepath.Join(tmpDir, ".dockerconfigjson")),
			"the cluster pull secret should be used when the CI profile path is unreadable")
	})

	t.Run("copies the cluster pull secret into tmpDir", func(t *testing.T) {
		g := gomega.NewWithT(t)

		t.Setenv(registryAuthFileEnvVar, "")
		tmpDir := t.TempDir()

		authFile, err := registryAuthFilePathFrom(context.Background(), staticClient(clusterWithPullSecret().CoreV1()), tmpDir, absentProfilePullSecret(t))
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(authFile).To(gomega.Equal(filepath.Join(tmpDir, ".dockerconfigjson")))

		copied, err := os.ReadFile(authFile)
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(string(copied)).To(gomega.Equal(pullSecretDockerConfigJSON))

		info, err := os.Stat(authFile)
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(info.Mode().Perm()).To(gomega.Equal(os.FileMode(0600)),
			"the copied pull secret must not be readable by other users")
	})

	t.Run("allows anonymous access when the cluster has no pull secret", func(t *testing.T) {
		g := gomega.NewWithT(t)

		t.Setenv(registryAuthFileEnvVar, "")

		authFile, err := registryAuthFilePathFrom(context.Background(), staticClient(fake.NewSimpleClientset().CoreV1()), t.TempDir(), absentProfilePullSecret(t))
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(authFile).To(gomega.BeEmpty(),
			"MicroShift has no openshift-config/pull-secret, so images are read anonymously")
	})

	t.Run("allows anonymous access without a client func", func(t *testing.T) {
		g := gomega.NewWithT(t)

		t.Setenv(registryAuthFileEnvVar, "")

		authFile, err := registryAuthFilePathFrom(context.Background(), nil, t.TempDir(), absentProfilePullSecret(t))
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(authFile).To(gomega.BeEmpty())
	})

	t.Run("errors when the cluster client cannot be built", func(t *testing.T) {
		g := gomega.NewWithT(t)

		t.Setenv(registryAuthFileEnvVar, "")

		_, err := registryAuthFilePathFrom(context.Background(), func() (corev1client.CoreV1Interface, error) {
			return nil, errors.New("no kubeconfig")
		}, t.TempDir(), absentProfilePullSecret(t))
		g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("unable to create client for the cluster under test")))
	})

	t.Run("errors when the cluster pull secret cannot be read", func(t *testing.T) {
		g := gomega.NewWithT(t)

		t.Setenv(registryAuthFileEnvVar, "")

		client := fake.NewSimpleClientset()
		client.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("etcd is on fire")
		})

		_, err := registryAuthFilePathFrom(context.Background(), staticClient(client.CoreV1()), t.TempDir(), absentProfilePullSecret(t))
		g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("etcd is on fire")))
	})
}

// TestOCAuthArgs covers the global flags handed to oc so that it reaches the
// cluster under test and the registries serving its release payload.
func TestOCAuthArgs(t *testing.T) {
	t.Run("passes both the kubeconfig and the registry auth file", func(t *testing.T) {
		g := gomega.NewWithT(t)

		t.Setenv(kubeConfigEnvVar, "/run/secrets/ci.openshift.io/multi-stage/kubeconfig")

		g.Expect(ocAuthArgs("/run/credentials/auth.json")).To(gomega.Equal([]string{
			"--kubeconfig=/run/secrets/ci.openshift.io/multi-stage/kubeconfig",
			"--registry-config=/run/credentials/auth.json",
		}))
	})

	t.Run("omits the registry auth file when no credentials were found", func(t *testing.T) {
		g := gomega.NewWithT(t)

		t.Setenv(kubeConfigEnvVar, "/tmp/kubeconfig")

		g.Expect(ocAuthArgs("")).To(gomega.Equal([]string{"--kubeconfig=/tmp/kubeconfig"}))
	})

	t.Run("omits the kubeconfig when it is unset", func(t *testing.T) {
		g := gomega.NewWithT(t)

		t.Setenv(kubeConfigEnvVar, "")

		g.Expect(ocAuthArgs("")).To(gomega.BeEmpty())
	})
}

// staticClient returns a RegistryAuthClientFunc that yields c.
func staticClient(c corev1client.CoreV1Interface) RegistryAuthClientFunc {
	return func() (corev1client.CoreV1Interface, error) { return c, nil }
}

// noClientFunc returns a RegistryAuthClientFunc that fails the test if the
// ladder builds a client: the sources ahead of the cluster pull secret must
// short-circuit it, which is what makes the ladder usable with no kubeconfig.
func noClientFunc(t *testing.T) RegistryAuthClientFunc {
	t.Helper()

	return func() (corev1client.CoreV1Interface, error) {
		t.Fatal("the credential ladder built a cluster client even though the environment provided credentials")

		return nil, nil
	}
}

// clusterWithPullSecret returns a clientset for a cluster holding the usual
// openshift-config/pull-secret.
func clusterWithPullSecret() *fake.Clientset {
	return fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-config", Name: "pull-secret"},
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(pullSecretDockerConfigJSON)},
	})
}

// absentProfilePullSecret returns the path of a cluster profile pull secret
// that does not exist.
func absentProfilePullSecret(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "absent")
}

// writeFile creates a fixture pull secret at path.
func writeFile(t *testing.T, path string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(pullSecretDockerConfigJSON), 0600); err != nil {
		t.Fatalf("creating the fixture pull secret: %v", err)
	}
}
