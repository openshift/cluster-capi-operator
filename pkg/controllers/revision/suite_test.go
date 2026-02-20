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
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

const (
	subResourceStatus = "status"
)

var (
	testEnv *envtest.Environment
	cfg     *rest.Config
	cl      client.WithWatch

	// Test fixtures - set by createFixtures.
	infra           *configv1.Infrastructure
	clusterAPI      *operatorv1alpha1.ClusterAPI
	clusterOperator *configv1.ClusterOperator

	defaultProviderImgs []providerimages.ProviderImageManifests = []providerimages.ProviderImageManifests{
		{
			ProviderMetadata: providerimages.ProviderMetadata{
				Name:         "core",
				InstallOrder: 10,
			},
			ContentID: "core-content-id",
			ImageRef:  "registry.example.com/core@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Profile:   "default",
		},
		{
			ProviderMetadata: providerimages.ProviderMetadata{
				Name:         "infra-aws",
				InstallOrder: 20,
				OCPPlatform:  configv1.AWSPlatformType,
			},
			ContentID: "infra-aws-content-id",
			ImageRef:  "registry.example.com/infra-aws@sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
			Profile:   "default",
		},
	}
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
})
