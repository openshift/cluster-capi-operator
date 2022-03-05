/*
Copyright 2021.

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

package controllers

import (
	"context"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2/klogr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

func init() {
	if err := configv1.Install(scheme.Scheme); err != nil {
		panic(err)
	}
	if err := operatorv1.Install(scheme.Scheme); err != nil {
		panic(err)
	}
	if err := v1.AddToScheme(scheme.Scheme); err != nil {
		panic(err)
	}
}

const (
	testManagedNamespace = "openshift-cluster-api"
)

var cfg *rest.Config
var cl client.Client
var testEnv *envtest.Environment

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func() {
	var err error
	logf.SetLogger(klogr.New())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("../..", "vendor", "github.com", "openshift", "api", "config", "v1"),
			filepath.Join("../..", "vendor", "github.com", "openshift", "api", "operator", "v1"),
		},
	}

	Expect(configv1.Install(scheme.Scheme)).To(Succeed())
	Expect(v1.AddToScheme(scheme.Scheme)).To(Succeed())

	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	// +kubebuilder:scaffold:scheme

	cl, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(cl).NotTo(BeNil())

	managedNamespace := &corev1.Namespace{}
	managedNamespace.SetName(testManagedNamespace)
	Expect(cl.Create(context.Background(), managedNamespace)).To(Succeed())

	ocpConfigNamespace := &corev1.Namespace{}
	ocpConfigNamespace.SetName(SecretSourceNamespace)
	Expect(cl.Create(context.Background(), ocpConfigNamespace)).To(Succeed())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
