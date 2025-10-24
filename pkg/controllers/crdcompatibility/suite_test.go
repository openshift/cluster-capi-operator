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
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
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

	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/crdvalidation"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var (
	testEnv *envtest.Environment
	cfg     *rest.Config
	cl      client.Client
)

const (
	admissionregv1 = "admissionregistration.k8s.io/v1"
)

var errUnsupportedAPIVersion = errors.New("only " + admissionregv1 + " is supported")

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
		// This struct intentionally left blank
		// We do this in startManager instead
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

	InitManager = func(ctx context.Context) (*CRDCompatibilityReconciler, func()) {
		return initManager(ctx, cfg, cl.Scheme(), testEnv)
	}
}, NodeTimeout(30*time.Second))

func initManager(ctx context.Context, cfg *rest.Config, scheme *runtime.Scheme, testEnv *envtest.Environment) (*CRDCompatibilityReconciler, func()) {
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

	crdCompatibilityReconciler := NewCRDCompatibilityReconciler(mgr.GetClient())
	crdValidator := crdvalidation.NewValidator(mgr.GetClient())

	// controller-runtime stores controller names in a global which we can't
	// clear between test runs. This causes name validation to fail every time
	// we start a manager after the first time.
	skipNameValidation := func(builder *builder.Builder) *builder.Builder {
		return builder.WithOptions(controller.Options{
			SkipNameValidation: ptr.To(true),
		})
	}

	Expect(crdCompatibilityReconciler.SetupWithManager(ctx, mgr, skipNameValidation)).To(Succeed(), "Reconciler should be setup with manager")
	Expect(crdValidator.SetupWithManager(ctx, mgr, skipNameValidation)).To(Succeed(), "CRD Validator should be setup with manager")

	return crdCompatibilityReconciler, func() {
		startManager(ctx, mgr)
	}
}

// startManager starts the manager and registers the webhooks.
//
//nolint:contextcheck // the comment below explains why we don't inherit the ginkgo node context
func startManager(ctx context.Context, mgr ctrl.Manager) {
	// Normally we would let controller-runtime register webhooks for us by
	// specifying them in WebhookInstallOptions. However, this means we cannot
	// perform CRUD operations when the manager is not running. As not all tests
	// run the manager, we manually register and deregister the webhooks when
	// starting and stopping the manager.
	// This code borrows heavily from the equivalent code in controller-runtime.
	By("Registering webhooks")

	for hook, err := range readWebhookManifests(
		filepath.Join("..", "..", "..", "manifests", "0000_20_crd-compatibility-checker_06_webhooks.yaml"),
	) {
		Expect(err).NotTo(HaveOccurred(), "reading webhook manifests")
		createTestObject(ctx, hook, "webhook "+hook.GetName())
	}

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

	// Wait for the manager to signal that it became leader (i.e. it completed
	// initialisation), or the node context to be cancelled
	select {
	case <-mgr.Elected():
	case <-ctx.Done():
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

func kWithCtx(ctx context.Context) komega.Komega {
	return komega.New(cl).WithContext(ctx)
}

// updateClientConfig updates the client config to point to testEnv's webhook endpoint.
func updateClientConfig(clientConfig *admissionv1.WebhookClientConfig) {
	clientConfig.CABundle = testEnv.WebhookInstallOptions.LocalServingCAData
	if clientConfig.Service != nil && clientConfig.Service.Path != nil {
		hostPort := net.JoinHostPort(testEnv.WebhookInstallOptions.LocalServingHost, fmt.Sprintf("%d", testEnv.WebhookInstallOptions.LocalServingPort))
		clientConfig.URL = ptr.To(fmt.Sprintf("https://%s/%s", hostPort, *clientConfig.Service.Path))
		clientConfig.Service = nil
	}
}

// readWebhookManifests returns an iterator over all webhook configuration
// objects defined in the given paths. The client config of each webhook is
// updated to point to testEnv's webhook endpoint.
func readWebhookManifests(paths ...string) iter.Seq2[client.Object, error] {
	return func(yield func(client.Object, error) bool) {
		for doc, err := range readYAMLDocuments(paths) {
			if err != nil {
				yield(nil, err)
				return
			}

			var generic metav1.PartialObjectMetadata
			if err = yaml.Unmarshal(doc, &generic); err != nil {
				yield(nil, err)
				return
			}

			if generic.APIVersion != admissionregv1 {
				yield(nil, fmt.Errorf("%w: APIVersion=%s", errUnsupportedAPIVersion, generic.APIVersion))
				return
			}

			switch generic.Kind {
			case "ValidatingWebhookConfiguration":
				hook := &admissionv1.ValidatingWebhookConfiguration{}
				if err := yaml.Unmarshal(doc, hook); err != nil {
					yield(nil, err)
					return
				}

				// Update the client config to point to testEnv's webhook endpoint
				for i := range hook.Webhooks {
					updateClientConfig(&hook.Webhooks[i].ClientConfig)
				}

				if !yield(hook, nil) {
					return
				}

			// Ignore unexpected kinds
			default:
			}
		}
	}
}

// readYAMLDocuments returns an iterator over all YAML documents contained in all the given paths.
func readYAMLDocuments(paths []string) iter.Seq2[[]byte, error] {
	return func(yield func([]byte, error) bool) {
		for _, path := range paths {
			b, err := os.ReadFile(path)
			if err != nil {
				yield(nil, err)
				return
			}

			reader := k8syaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(b)))

			for {
				// Read document
				doc, err := reader.Read()
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}

					yield(nil, err)

					return
				}

				if !yield(doc, nil) {
					return
				}
			}
		}
	}
}
