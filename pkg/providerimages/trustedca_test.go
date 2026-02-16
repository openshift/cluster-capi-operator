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
package providerimages

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// generateTestCACertPEM creates a valid self-signed CA certificate PEM for testing.
func generateTestCACertPEM() string {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred(), "generating test CA key")

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"Test CA"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).NotTo(HaveOccurred(), "creating test CA certificate")

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

var _ = Describe("getTrustedCATransport", func() {
	var (
		ctx       context.Context
		log       logr.Logger
		scheme    *runtime.Scheme
		testCAPEM string
	)

	BeforeEach(func() {
		ctx = context.Background()
		log = logr.Discard()
		scheme = runtime.NewScheme()
		Expect(configv1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		testCAPEM = generateTestCACertPEM()
	})

	It("should return default transport when neither CA source exists", func() {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		transport, err := getTrustedCATransport(ctx, c, log)
		Expect(err).NotTo(HaveOccurred())
		Expect(transport).To(Equal(remote.DefaultTransport), "expected default transport on a connected cluster with no additional CAs")
	})

	It("should return a transport from trusted-ca-bundle alone", func() {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      trustedCABundleName,
					Namespace: trustedCABundleNamespace,
				},
				Data: map[string]string{"ca-bundle.crt": testCAPEM},
			},
		).Build()

		transport, err := getTrustedCATransport(ctx, c, log)
		Expect(err).NotTo(HaveOccurred())
		Expect(transport).NotTo(BeNil(), "expected transport from CNO-managed trust bundle")

		httpTransport, ok := transport.(*http.Transport)
		Expect(ok).To(BeTrue(), "expected *http.Transport")
		Expect(httpTransport.TLSClientConfig).NotTo(BeNil(), "expected TLS config on transport")
		Expect(httpTransport.TLSClientConfig.RootCAs).NotTo(BeNil(), "expected custom root CA pool")
		Expect(httpTransport.TLSClientConfig.MinVersion).To(Equal(uint16(tls.VersionTLS12)), "expected TLS 1.2 minimum")
	})

	It("should return a transport from image.config additionalTrustedCA alone", func() {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&configv1.Image{
				ObjectMeta: metav1.ObjectMeta{Name: imageConfigName},
				Spec: configv1.ImageSpec{
					AdditionalTrustedCA: configv1.ConfigMapNameReference{Name: "registry-cas"},
				},
			},
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "registry-cas", Namespace: openshiftConfigNamespace},
				Data: map[string]string{
					"mirror.disconnected.local..5000": testCAPEM,
				},
			},
		).Build()

		transport, err := getTrustedCATransport(ctx, c, log)
		Expect(err).NotTo(HaveOccurred())
		Expect(transport).NotTo(BeNil(), "expected transport from image.config additionalTrustedCA")
	})

	It("should merge CAs from both sources", func() {
		secondCAPEM := generateTestCACertPEM()

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      trustedCABundleName,
					Namespace: trustedCABundleNamespace,
				},
				Data: map[string]string{"ca-bundle.crt": testCAPEM},
			},
			&configv1.Image{
				ObjectMeta: metav1.ObjectMeta{Name: imageConfigName},
				Spec: configv1.ImageSpec{
					AdditionalTrustedCA: configv1.ConfigMapNameReference{Name: "registry-cas"},
				},
			},
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "registry-cas", Namespace: openshiftConfigNamespace},
				Data: map[string]string{
					"mirror.disconnected.local..5000": secondCAPEM,
				},
			},
		).Build()

		transport, err := getTrustedCATransport(ctx, c, log)
		Expect(err).NotTo(HaveOccurred())
		Expect(transport).NotTo(BeNil(), "expected transport with CAs merged from both sources")

		httpTransport, ok := transport.(*http.Transport)
		Expect(ok).To(BeTrue(), "expected *http.Transport")
		Expect(httpTransport.TLSClientConfig).NotTo(BeNil())
		Expect(httpTransport.TLSClientConfig.RootCAs).NotTo(BeNil())
		Expect(httpTransport.TLSClientConfig.MinVersion).To(Equal(uint16(tls.VersionTLS12)))
	})

	It("should return default transport when trusted-ca-bundle exists but has no valid PEM", func() {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      trustedCABundleName,
					Namespace: trustedCABundleNamespace,
				},
				Data: map[string]string{"ca-bundle.crt": "not-a-pem"},
			},
		).Build()

		transport, err := getTrustedCATransport(ctx, c, log)
		Expect(err).NotTo(HaveOccurred())
		Expect(transport).To(Equal(remote.DefaultTransport), "expected default transport when ConfigMap has no valid PEM")
	})

	It("should return default transport when image.config exists but additionalTrustedCA is empty", func() {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&configv1.Image{
				ObjectMeta: metav1.ObjectMeta{Name: imageConfigName},
				Spec:       configv1.ImageSpec{},
			},
		).Build()

		transport, err := getTrustedCATransport(ctx, c, log)
		Expect(err).NotTo(HaveOccurred())
		Expect(transport).To(Equal(remote.DefaultTransport), "expected default transport when additionalTrustedCA is not set")
	})

	It("should return default transport when image.config references a missing ConfigMap", func() {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&configv1.Image{
				ObjectMeta: metav1.ObjectMeta{Name: imageConfigName},
				Spec: configv1.ImageSpec{
					AdditionalTrustedCA: configv1.ConfigMapNameReference{Name: "missing-cm"},
				},
			},
		).Build()

		transport, err := getTrustedCATransport(ctx, c, log)
		Expect(err).NotTo(HaveOccurred())
		Expect(transport).To(Equal(remote.DefaultTransport), "expected default transport when referenced CA ConfigMap is missing")
	})

	It("should load CAs from keys with valid PEM and ignore keys with invalid PEM", func() {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      trustedCABundleName,
					Namespace: trustedCABundleNamespace,
				},
				Data: map[string]string{
					"ca-bundle.crt":                   testCAPEM,
					"garbage.crt":                     "not-valid-pem",
					"mirror.disconnected.local..5000": testCAPEM,
				},
			},
		).Build()

		transport, err := getTrustedCATransport(ctx, c, log)
		Expect(err).NotTo(HaveOccurred())
		Expect(transport).NotTo(BeNil(), "expected transport when at least one key has valid PEM")
	})

	It("should propagate non-NotFound errors from trusted-ca-bundle lookup", func() {
		expectedErr := fmt.Errorf("connection refused")
		c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*corev1.ConfigMap); ok && key.Name == trustedCABundleName {
					return expectedErr
				}

				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()

		_, err := getTrustedCATransport(ctx, c, log)
		Expect(err).To(MatchError(expectedErr))
	})

	It("should propagate non-NotFound errors from image.config lookup", func() {
		expectedErr := fmt.Errorf("forbidden")
		c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*configv1.Image); ok {
					return expectedErr
				}

				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()

		_, err := getTrustedCATransport(ctx, c, log)
		Expect(err).To(MatchError(expectedErr))
	})

	It("should propagate non-NotFound errors from additionalTrustedCA ConfigMap lookup", func() {
		expectedErr := fmt.Errorf("timeout")
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&configv1.Image{
				ObjectMeta: metav1.ObjectMeta{Name: imageConfigName},
				Spec: configv1.ImageSpec{
					AdditionalTrustedCA: configv1.ConfigMapNameReference{Name: "registry-cas"},
				},
			},
		).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*corev1.ConfigMap); ok && key.Name == "registry-cas" {
					return expectedErr
				}

				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()

		_, err := getTrustedCATransport(ctx, c, log)
		Expect(err).To(MatchError(expectedErr))
	})
})
