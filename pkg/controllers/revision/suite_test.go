/*
Copyright 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package revision

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var (
	testEnv *envtest.Environment
	cfg     *rest.Config
	cl      client.WithWatch
)

var defaultNodeTimeout = NodeTimeout(10 * time.Second)

func TestRevisionController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Revision Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(klog.Background())

	By("bootstrapping test environment")

	var err error

	testEnv = &envtest.Environment{}
	cfg, cl, err = test.StartEnvTest(testEnv)
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())
	Expect(cl).NotTo(BeNil())
	DeferCleanup(func() {
		By("tearing down the test environment")
		Expect(test.StopEnvTest(testEnv)).To(Succeed())
	})

	By("setting up provider image fixtures")
	setupProviderFixtures()
})

func setupProviderFixtures() {
	tb := GinkgoTB()

	// Each provider uses its ContentID as the ConfigMap name so that
	// different provider sets produce different revision contentIDs.
	defaultProviderImgs = []providerimages.ProviderImageManifests{
		test.NewProviderImageManifests(tb, "core").
			WithContentID("core-content-id").
			WithImageRef("registry.example.com/core@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef").
			WithManifests(test.ConfigMapYAML("core-content-id")).
			Build(),
		test.NewProviderImageManifests(tb, "infra-aws").
			WithInstallOrder(20).
			WithPlatform(configv1.AWSPlatformType).
			WithContentID("infra-aws-content-id").
			WithImageRef("registry.example.com/infra-aws@sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210").
			WithManifests(test.ConfigMapYAML("infra-aws-content-id")).
			Build(),
	}

	updatedProviderImgs = []providerimages.ProviderImageManifests{
		test.NewProviderImageManifests(tb, "core").
			WithContentID("core-content-id-2").
			WithImageRef("registry.example.com/core@sha256:1111111111111111111111111111111111111111111111111111111111111111").
			WithManifests(test.ConfigMapYAML("core-content-id-2")).
			Build(),
		test.NewProviderImageManifests(tb, "infra-aws").
			WithInstallOrder(20).
			WithPlatform(configv1.AWSPlatformType).
			WithContentID("infra-aws-content-id-2").
			WithImageRef("registry.example.com/infra-aws@sha256:2222222222222222222222222222222222222222222222222222222222222222").
			WithManifests(test.ConfigMapYAML("infra-aws-content-id-2")).
			Build(),
	}

	nonMatchingProviderImgs = []providerimages.ProviderImageManifests{
		test.NewProviderImageManifests(tb, "infra-gcp").
			WithInstallOrder(20).
			WithPlatform(configv1.GCPPlatformType).
			WithContentID("infra-gcp-content-id").
			WithImageRef("registry.example.com/infra-gcp@sha256:3333333333333333333333333333333333333333333333333333333333333333").
			WithManifests(test.ConfigMapYAML("infra-gcp-content-id")).
			Build(),
	}
}

func kWithCtx(ctx context.Context) komega.Komega {
	return komega.New(cl).WithContext(ctx)
}
