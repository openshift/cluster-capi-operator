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

package objectpruning

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var (
	testEnv *envtest.Environment
	cfg     *rest.Config
	cl      client.Client
)

var defaultNodeTimeout = NodeTimeout(10 * time.Second)

func TestObjectPruning(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ObjectPruning Controller Suite")
}

var _ = BeforeSuite(func(ctx context.Context) {
	logf.SetLogger(GinkgoLogr)

	By("bootstrapping test environment")

	var err error

	testEnv = &envtest.Environment{
		WebhookInstallOptions: envtest.WebhookInstallOptions{},
	}
	cfg, cl, err = test.StartEnvTest(testEnv)

	DeferCleanup(func() {
		By("tearing down the test environment")
		Expect(test.StopEnvTest(testEnv)).To(Succeed())
	})

	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())
	Expect(cl).NotTo(BeNil())

	// Set up komega with the client
	komega.SetClient(cl)

	// Initialize validator and webhook server
	_, startWebhookServer := initValidator(ctx, cfg, cl.Scheme(), testEnv)
	startWebhookServer()
}, NodeTimeout(30*time.Second))

func initValidator(ctx context.Context, cfg *rest.Config, scheme *runtime.Scheme, testEnv *envtest.Environment) (*validator, func()) {
	By("Setting up an object validator with webhook server")

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		WebhookServer: webhook.NewServer(webhook.Options{
			CertDir: testEnv.WebhookInstallOptions.LocalServingCertDir,
			Port:    testEnv.WebhookInstallOptions.LocalServingPort,
			Host:    testEnv.WebhookInstallOptions.LocalServingHost,
		}),
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).ToNot(HaveOccurred(), "Manager should be created")

	objectValidator := NewValidator()
	err = objectValidator.SetupWithManager(ctx, mgr)
	Expect(err).ToNot(HaveOccurred(), "Object Validator should be setup with manager")

	return objectValidator, func() {
		startWebhookServer(ctx, mgr)
	}
}

// startWebhookServer starts the webhook server and waits for it to be ready.
//
//nolint:contextcheck // the comment below explains why we don't inherit the ginkgo node context
func startWebhookServer(ctx context.Context, mgr ctrl.Manager) {
	By("Starting the webhook server")

	// We expect to start the manager from a Before node. We start the manager
	// with its own context because we need it to live longer than the Before
	// node.
	mgrCtx, mgrCancel := context.WithCancel(context.Background())
	mgrDone := make(chan struct{})

	go func() {
		defer GinkgoRecover()
		defer close(mgrDone)

		Expect(mgr.Start(mgrCtx)).To(Succeed())
	}()

	DeferCleanup(func(ctx context.Context) {
		By("Stopping the webhook server")
		stopWebhookServer(ctx, mgrCancel, mgrDone)
	})

	// Wait for the manager to signal that it became leader (i.e. it completed
	// initialisation), or the node context to be cancelled
	select {
	case <-mgr.Elected():
	case <-ctx.Done():
	}

	// Error if the manager didn't startup in time.
	Expect(mgr.Elected()).To(BeClosed(), "Manager didn't startup in time")
}

func stopWebhookServer(ctx context.Context, mgrCancel context.CancelFunc, mgrDone chan struct{}) {
	By("Stopping the webhook server")
	mgrCancel()

	// Wait for the mgrDone to be closed, which will happen once the mgr has stopped.
	select {
	case <-mgrDone:
	case <-ctx.Done():
	}

	// Error if the manager didn't stop in time.
	Expect(mgrDone).To(BeClosed(), "Manager didn't stop in time")
}

func kWithCtx(ctx context.Context) komega.Komega {
	return komega.New(cl).WithContext(ctx)
}
