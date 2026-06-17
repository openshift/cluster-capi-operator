// Copyright 2026 Red Hat, Inc.
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
package crdcompatibilityoperator

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/textlogger"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var (
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
)

const (
	testNamespace    = "test-namespace"
	testOperandImage = "test.io/operand:latest"
)

func TestCRDCompatibilityOperatorController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CRDCompatibilityOperatorController Suite")
}

var _ = BeforeSuite(func() {
	klog.SetOutput(GinkgoWriter)

	logf.SetLogger(textlogger.NewLogger(textlogger.NewConfig()))

	ctx, cancel = context.WithCancel(context.Background())

	By("Bootstrapping test environment")

	testEnv = &envtest.Environment{}
	cfg, cl, err := test.StartEnvTest(testEnv)
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())
	Expect(cl).NotTo(BeNil())

	k8sClient = cl
})

var _ = AfterSuite(func() {
	cancel()
	By("Tearing down test environment")
	Expect(testEnv.Stop()).To(Succeed())
})

func startManager(ctx context.Context, cfg *rest.Config, cl client.Client) (manager.Manager, error) {
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: cl.Scheme(),
	})
	if err != nil {
		return nil, err
	}

	controller := NewCRDCompatibilityOperatorController(mgr.GetClient(), mgr.GetScheme(), testNamespace, testOperandImage)
	if err := controller.SetupWithManager(mgr); err != nil {
		return nil, err
	}

	go func() {
		defer GinkgoRecover()

		Expect(mgr.Start(ctx)).To(Succeed())
	}()

	return mgr, nil
}
