/*
Copyright 2025 Red Hat, Inc.

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

package crdcompatibility

import (
	"context"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/yaml"

	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var (
	testEnv *envtest.Environment
	cfg     *rest.Config
	cl      client.Client
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CRDCompatibility Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(GinkgoLogr)

	By("bootstrapping test environment")
	var err error
	testEnv = &envtest.Environment{
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{filepath.Join("..", "..", "..", "manifests", "0000_20_crd-compatibility-checker_02_webhooks.yaml")},
		},
	}
	cfg, cl, err = test.StartEnvTest(testEnv)

	DeferCleanup(func() {
		By("tearing down the test environment")
		Expect(test.StopEnvTest(testEnv)).To(Succeed())
	})

	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())
	Expect(cl).NotTo(BeNil())

	mgrCancel, mgrDone := startManager(context.Background(), cfg, cl.Scheme(), testEnv)

	DeferCleanup(func() {
		By("Stopping the manager")
		stopManager(mgrCancel, mgrDone)
	})
})

func startManager(ctx context.Context, cfg *rest.Config, scheme *runtime.Scheme, testEnv *envtest.Environment) (context.CancelFunc, chan struct{}) {
	mgrCtx, mgrCancel := context.WithCancel(ctx)
	mgrDone := make(chan struct{})

	By("Setting up a manager and controller")

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		WebhookServer: webhook.NewServer(webhook.Options{
			CertDir: testEnv.WebhookInstallOptions.LocalServingCertDir,
			Port:    testEnv.WebhookInstallOptions.LocalServingPort,
			Host:    testEnv.WebhookInstallOptions.LocalServingHost,
		}),
	})
	Expect(err).ToNot(HaveOccurred(), "Manager should be created")

	r := &CRDCompatibilityReconciler{client: mgr.GetClient()}
	Expect(r.SetupWithManager(ctx, mgr)).To(Succeed(), "Reconciler should be setup with manager")

	By("Starting the manager")

	go func() {
		defer GinkgoRecover()
		defer close(mgrDone)

		Expect((mgr).Start(mgrCtx)).To(Succeed())
	}()

	Eventually(mgr.Elected()).Should(BeClosed())

	return mgrCancel, mgrDone
}

func stopManager(mgrCancel context.CancelFunc, mgrDone chan struct{}) {
	By("Stopping the manager")
	mgrCancel()
	// Wait for the mgrDone to be closed, which will happen once the mgr has stopped.
	<-mgrDone

	Eventually(mgrDone).Should(BeClosed())
}

func toYAML(obj any) string {
	yaml, err := yaml.Marshal(obj)
	Expect(err).To(Succeed())

	return string(yaml)
}

func kWithCtx(ctx context.Context) komega.Komega {
	return komega.New(cl).WithContext(ctx)
}
