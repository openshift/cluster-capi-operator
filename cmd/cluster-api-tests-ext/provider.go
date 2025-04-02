// Copyright 2024 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	kclientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubernetes/openshift-hack/e2e"
	conformancetestdata "k8s.io/kubernetes/test/conformance/testdata"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/framework/testfiles"
	e2etestingmanifests "k8s.io/kubernetes/test/e2e/testing-manifests"
	testfixtures "k8s.io/kubernetes/test/fixtures"

	// this appears to inexplicably auto-register global flags.
	_ "k8s.io/kubernetes/test/e2e/storage/drivers"

	// these are loading important global flags that we need to get and set.
	_ "k8s.io/kubernetes/test/e2e"
	_ "k8s.io/kubernetes/test/e2e/lifecycle"
)

// copied directly from github.com/openshift/kubernetes/openshift-hack/cmd/k8s-tests-ext/provider.go
// I attempted to use the clusterdiscovery.InitializeTestFramework in origin but it has too many additional parameters
// that as an test-ext, I felt we shouldn't have to load all that.  Hopefully origin's test-ext frameworks gets enhanced
// to have a simple way to initialize all this w/o having to copy/pasta like the openshift/kubernetes project did.
//
//nolint:funlen
func initializeTestFramework(provider string) error {
	providerInfo := &ClusterConfiguration{}

	if err := json.Unmarshal([]byte(provider), &providerInfo); err != nil {
		return fmt.Errorf("provider must be a JSON object with the 'type' key at a minimum: %w", err)
	}

	if len(providerInfo.ProviderName) == 0 {
		return fmt.Errorf("provider must be a JSON object with the 'type' key")
	}

	config := &ClusterConfiguration{}

	if err := json.Unmarshal([]byte(provider), config); err != nil {
		return fmt.Errorf("provider must decode into the ClusterConfig object: %w", err)
	}

	// update testContext with loaded config
	testContext := &framework.TestContext
	testContext.Provider = config.ProviderName
	testContext.CloudConfig = framework.CloudConfig{
		ProjectID:   config.ProjectID,
		Region:      config.Region,
		Zone:        config.Zone,
		Zones:       config.Zones,
		NumNodes:    config.NumNodes,
		MultiMaster: config.MultiMaster,
		MultiZone:   config.MultiZone,
		ConfigFile:  config.ConfigFile,
	}
	testContext.AllowedNotReadyNodes = -1
	testContext.MinStartupPods = -1
	testContext.MaxNodesToGather = 0
	testContext.KubeConfig = os.Getenv("KUBECONFIG")

	if ad := os.Getenv("ARTIFACT_DIR"); len(strings.TrimSpace(ad)) == 0 {
		if err := os.Setenv("ARTIFACT_DIR", filepath.Join(os.TempDir(), "artifacts")); err != nil {
			return fmt.Errorf("unable to set ARTIFACT_DIR: %w", err)
		}
	}

	testContext.DeleteNamespace = os.Getenv("DELETE_NAMESPACE") != "false"
	testContext.VerifyServiceAccount = true

	testfiles.AddFileSource(e2etestingmanifests.GetE2ETestingManifestsFS())
	testfiles.AddFileSource(testfixtures.GetTestFixturesFS())
	testfiles.AddFileSource(conformancetestdata.GetConformanceTestdataFS())

	testContext.KubectlPath = "kubectl"
	testContext.KubeConfig = os.Getenv("KUBECONFIG")

	// "debian" is used when not set. At least GlusterFS tests need "custom".
	// (There is no option for "rhel" or "centos".)
	testContext.NodeOSDistro = "custom"
	testContext.MasterOSDistro = "custom"

	// load and set the host variable for kubectl
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(&clientcmd.ClientConfigLoadingRules{ExplicitPath: testContext.KubeConfig}, &clientcmd.ConfigOverrides{})
	cfg, err := clientConfig.ClientConfig()

	if err != nil {
		return fmt.Errorf("failed to get client config: %w", err)
	}

	testContext.Host = cfg.Host

	// Ensure that Kube tests run privileged (like they do upstream)
	testContext.CreateTestingNS = func(ctx context.Context, baseName string, c kclientset.Interface, labels map[string]string) (*corev1.Namespace, error) {
		return e2e.CreateTestingNS(ctx, baseName, c, labels, true)
	}

	gomega.RegisterFailHandler(ginkgo.Fail)

	framework.AfterReadingAllFlags(testContext)
	testContext.DumpLogsOnFailure = true

	// these constants are taken from kube e2e and used by tests
	testContext.IPFamily = "ipv4"
	if config.HasIPv6 && !config.HasIPv4 {
		testContext.IPFamily = "ipv6"
	}

	testContext.ReportDir = os.Getenv("TEST_JUNIT_DIR")

	return nil
}
