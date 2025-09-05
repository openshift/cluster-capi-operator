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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
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

// InitManager initialises a manager and adds a CRDCompatibilityReconciler to
// it. It returns the reconciler, and a startmanager function. It is not
// necessary to call startmanager if, for example, the test will call the
// reconcile function directly.
//
// startmanager blocks until the manager has started and is ready to be used,
// which must happen before the context passed to InitManager is cancelled. It
// uses DeferCleanup to ensure that the manager will be stopped at the
// appropriate time.
var InitManager func(context.Context) (*CRDCompatibilityReconciler, func())

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CRDCompatibility Controller Suite")
}

var _ = BeforeSuite(func(ctx context.Context) {
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

	InitManager = func(nodeCtx context.Context) (*CRDCompatibilityReconciler, func()) {
		return initManager(nodeCtx, cfg, cl.Scheme(), testEnv)
	}
}, NodeTimeout(30*time.Second))

func initManager(nodeCtx context.Context, cfg *rest.Config, scheme *runtime.Scheme, testEnv *envtest.Environment) (*CRDCompatibilityReconciler, func()) {
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

	// controller-runtime stores controller names in a global which we can't
	// clear between test runs. This causes name validation to fail every time
	// we start a manager after the first time.
	skipNameValidation := func(builder *builder.Builder) *builder.Builder {
		return builder.WithOptions(controller.Options{
			SkipNameValidation: ptr.To(true),
		})
	}

	Expect(r.SetupWithManager(nodeCtx, mgr, skipNameValidation)).To(Succeed(), "Reconciler should be setup with manager")

	return r, func() {
		startManager(nodeCtx, mgr)
	}
}

func startManager(nodeCtx context.Context, mgr ctrl.Manager) {
	By("Starting the manager")

	// We expect to start the manager from a Before node. We start the manager
	// with its own context because we need it to live longer than the Before
	// node.
	mgrCtx, mgrCancel := context.WithCancel(context.Background())
	mgrDone := make(chan struct{})

	go func() {
		defer GinkgoRecover()
		defer close(mgrDone)

		Expect((mgr).Start(mgrCtx)).To(Succeed())
	}()

	DeferCleanup(func(ctx context.Context) {
		By("Stopping the manager")
		stopManager(ctx, mgrCancel, mgrDone)
	})

	// Wait for the manager to signal that it became leader (i.e. it completed initialisation)
	select {
	case <-mgr.Elected():
	case <-nodeCtx.Done():
	}

	// Error if the manager didn't startup in time.
	Expect(mgr.Elected()).To(BeClosed(), "Manager didn't startup in time")
}

func stopManager(ctx context.Context, mgrCancel context.CancelFunc, mgrDone chan struct{}) {
	By("Stopping the manager")
	mgrCancel()

	// Wait for the mgrDone to be closed, which will happen once the mgr has stopped.
	select {
	case <-mgrDone:
	case <-ctx.Done():
	}

	// Error if the manager didn't stop in time.
	Expect(mgrDone).To(BeClosed(), "Manager didn't stop in time")
}

func toYAML(obj any) string {
	yaml, err := yaml.Marshal(obj)
	Expect(err).To(Succeed())

	return string(yaml)
}

func kWithCtx(ctx context.Context) komega.Komega {
	return komega.New(cl).WithContext(ctx)
}
