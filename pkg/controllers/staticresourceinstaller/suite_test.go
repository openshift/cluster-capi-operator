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

package staticresourceinstaller

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var (
	testEnv *envtest.Environment
	cfg     *rest.Config
	cl      client.Client
)

// InitManager initializes a manager and adds a staticResourceInstallerController to it.
// It returns the controller and a startManager function.
// startManager blocks until the manager has started and is ready to be used.
var InitManager func(context.Context, Assets) (*staticResourceInstallerController, func())

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "StaticResourceInstaller Controller Suite")
}

var _ = BeforeSuite(func() {
	ctx := context.Background()

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

	komega.SetClient(cl)
	komega.SetContext(ctx)

	InitManager = func(ctx context.Context, assets Assets) (*staticResourceInstallerController, func()) {
		return initManager(ctx, cfg, cl.Scheme(), assets)
	}

	// Ensure the cluster operator is created as it is required
	// for the controller to reconcile an initial event.
	clusterOperator := &configv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{
			Name: controllers.ClusterOperatorName,
		},
	}
	Expect(cl.Create(ctx, clusterOperator)).To(Succeed())

	DeferCleanup(func() {
		Expect(cl.Delete(ctx, clusterOperator)).To(Succeed())
	})
})

func initManager(ctx context.Context, cfg *rest.Config, scheme *runtime.Scheme, assets Assets) (*staticResourceInstallerController, func()) {
	By("Setting up a manager and controller")

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
	})
	Expect(err).ToNot(HaveOccurred(), "Manager should be created")

	staticResourceController := NewStaticResourceInstallerController(assets)

	// Setup the controller with manager - this will also read the assets
	err = staticResourceController.SetupWithManager(ctx, mgr)
	Expect(err).ToNot(HaveOccurred(), "Controller should setup with manager")

	return staticResourceController, func() {
		startManager(ctx, mgr)
	}
}

// startManager starts the manager and waits for it to be ready.
func startManager(ctx context.Context, mgr ctrl.Manager) {
	By("Starting the manager")

	// Start the manager with its own context because we need it to live longer than the test node
	mgrCtx, mgrCancel := context.WithCancel(ctx)
	mgrDone := make(chan struct{})

	go func() {
		defer GinkgoRecover()
		defer close(mgrDone)

		Expect(mgr.Start(mgrCtx)).To(Succeed())
	}()

	DeferCleanup(func(ctx context.Context) {
		By("Stopping the manager")
		stopManager(ctx, mgrCancel, mgrDone)
	})

	// Wait for the manager to signal that it became leader (i.e. it completed initialisation)
	select {
	case <-mgr.Elected():
	case <-ctx.Done():
	}

	// Error if the manager didn't startup in time
	Expect(mgr.Elected()).To(BeClosed(), "Manager didn't startup in time")
}

func stopManager(ctx context.Context, mgrCancel context.CancelFunc, mgrDone chan struct{}) {
	By("Stopping the manager")
	mgrCancel()

	// Wait for the manager to stop
	select {
	case <-mgrDone:
	case <-ctx.Done():
	}

	// Error if the manager didn't stop in time
	Expect(mgrDone).To(BeClosed(), "Manager didn't stop in time")
}

func kWithCtx(ctx context.Context) komega.Komega {
	return komega.New(cl).WithContext(ctx)
}
