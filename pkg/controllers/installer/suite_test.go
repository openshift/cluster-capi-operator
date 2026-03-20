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

package installer

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var (
	testEnv *envtest.Environment
	cfg     *rest.Config
	cl      client.WithWatch

	// reconcileCh allows tests to explicitly trigger reconciliation,
	// even when no ClusterAPI object exists.
	reconcileCh chan event.TypedGenericEvent[client.Object]

	// mgrCancel stops the manager goroutine.
	mgrCancel context.CancelFunc
	mgrDone   chan struct{}
)

var (
	defaultNodeTimeout       = NodeTimeout(15 * time.Second)
	defaultEventuallyTimeout = 5 * time.Second
)

func TestInstallerController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Installer Controller Suite")
}

var _ = BeforeSuite(func() {
	logger := test.NewVerboseGinkgoLogger(0)
	logf.SetLogger(logger)

	By("bootstrapping test environment")

	var err error

	testEnv = &envtest.Environment{}
	cfg, cl, err = test.StartEnvTest(testEnv)
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())
	Expect(cl).NotTo(BeNil())

	By("setting up provider profiles and manifest fixtures")
	setupProviderProfiles()

	By("creating manager with InstallerController")

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: cl.Scheme(),
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
		Controller: ctrlconfig.Controller{
			SkipNameValidation: ptr.To(true),
		},
	})
	Expect(err).NotTo(HaveOccurred())

	// Create channel source for test-triggered reconciliation.
	reconcileCh = make(chan event.TypedGenericEvent[client.Object], 1)
	toClusterAPI := func(_ context.Context, _ client.Object) []reconcile.Request {
		return []reconcile.Request{{
			NamespacedName: client.ObjectKey{Name: clusterAPIName},
		}}
	}
	triggerSource := source.Channel(
		reconcileCh,
		handler.EnqueueRequestsFromMapFunc(toClusterAPI),
	)

	Expect(SetupWithManager(mgr, allProviderProfiles, triggerSource)).To(Succeed())
	Expect(test.AddNamespaceFinalizerCleanup(mgr)).To(Succeed())

	// Start manager in background.
	mgrCtx, cancel := context.WithCancel(context.Background())
	mgrCancel = cancel
	mgrDone = make(chan struct{})

	go func() {
		defer GinkgoRecover()
		defer close(mgrDone)

		Expect(mgr.Start(mgrCtx)).To(Succeed())
	}()
})

var _ = AfterSuite(func() {
	By("stopping manager")

	if mgrCancel != nil {
		mgrCancel()
		Eventually(mgrDone).Should(BeClosed())
	}

	By("tearing down the test environment")

	if testEnv != nil {
		Expect(test.StopEnvTest(testEnv)).To(Succeed())
	}
})

func kWithCtx(ctx context.Context) komega.Komega {
	return komega.New(cl).WithContext(ctx)
}
