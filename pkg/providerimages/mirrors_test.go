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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var _ = Describe("sourceMatchesRef", func() {
	DescribeTable("should match sources at path boundaries",
		func(ref, source string, expected bool) {
			Expect(sourceMatchesRef(ref, source)).To(Equal(expected))
		},
		Entry("exact match",
			"registry.ci.openshift.org/openshift", "registry.ci.openshift.org/openshift", true),
		Entry("slash boundary",
			"registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd", "registry.ci.openshift.org/openshift", true),
		Entry("digest boundary",
			"registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd", "registry.ci.openshift.org/openshift/aws-cluster-api-controllers", true),
		Entry("partial segment does not match",
			"registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd", "registry.ci.openshift.org/open", false),
		Entry("no prefix match",
			"registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd", "quay.io/openshift", false),
		Entry("port boundary does not match",
			"registry.ci.openshift.org:5000/openshift/aws-cluster-api-controllers@sha256:aabbccdd", "registry.ci.openshift.org", false),
	)
})

var _ = Describe("wildcardMatchesRef", func() {
	DescribeTable("should match wildcard sources against hostnames",
		func(ref, wildcardSource string, expected bool) {
			Expect(wildcardMatchesRef(ref, wildcardSource)).To(Equal(expected))
		},
		Entry("single subdomain matches",
			"registry.redhat.io/product/repo@sha256:abc", "*.redhat.io", true),
		Entry("multi-level subdomain matches",
			"sub.domain.redhat.io/product/repo@sha256:abc", "*.redhat.io", true),
		Entry("bare domain does not match",
			"redhat.io/product/repo@sha256:abc", "*.redhat.io", false),
		Entry("unrelated domain does not match",
			"registry.example.com/product/repo@sha256:abc", "*.redhat.io", false),
		Entry("hostname with port matches",
			"registry.redhat.io:5000/product/repo@sha256:abc", "*.redhat.io", true),
	)
})

var _ = Describe("resolveImageRef", func() {
	type testInput struct {
		imageRef       string
		mirrors        map[string][]string
		expected       string
		expectedSource string
	}

	DescribeTable("should resolve image references through mirrors",
		func(tt testInput) {
			resolved, matchedSource := resolveImageRef(tt.imageRef, tt.mirrors)
			Expect(resolved).To(Equal(tt.expected))

			if tt.expectedSource != "" {
				Expect(matchedSource).To(Equal(tt.expectedSource))
			}
		},
		Entry("nil mirrors returns original ref", testInput{
			imageRef: "registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd",
			mirrors:  nil,
			expected: "registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd",
		}),
		Entry("empty mirrors returns original ref", testInput{
			imageRef: "registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd",
			mirrors:  map[string][]string{},
			expected: "registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd",
		}),
		Entry("exact source match rewrites ref", testInput{
			imageRef: "registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd",
			mirrors: map[string][]string{
				"registry.ci.openshift.org/openshift/aws-cluster-api-controllers": {
					"virthost.ostest.test.metalkube.org:5000/localimages/aws-cluster-api-controllers",
				},
			},
			expected:       "virthost.ostest.test.metalkube.org:5000/localimages/aws-cluster-api-controllers@sha256:aabbccdd",
			expectedSource: "registry.ci.openshift.org/openshift/aws-cluster-api-controllers",
		}),
		Entry("prefix match rewrites with suffix preserved", testInput{
			imageRef: "registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd",
			mirrors: map[string][]string{
				"registry.ci.openshift.org/openshift": {
					"virthost.ostest.test.metalkube.org:5000/localimages/local-release-image",
				},
			},
			expected: "virthost.ostest.test.metalkube.org:5000/localimages/local-release-image/aws-cluster-api-controllers@sha256:aabbccdd",
		}),
		Entry("longest prefix wins", testInput{
			imageRef: "quay-proxy.ci.openshift.org/openshift/ci/aws-cluster-api-controllers@sha256:aabbccdd",
			mirrors: map[string][]string{
				"quay-proxy.ci.openshift.org": {
					"virthost.ostest.test.metalkube.org:5000/short",
				},
				"quay-proxy.ci.openshift.org/openshift/ci": {
					"virthost.ostest.test.metalkube.org:5000/localimages/local-release-image",
				},
			},
			expected:       "virthost.ostest.test.metalkube.org:5000/localimages/local-release-image/aws-cluster-api-controllers@sha256:aabbccdd",
			expectedSource: "quay-proxy.ci.openshift.org/openshift/ci",
		}),
		Entry("multiple mirrors for one source uses first", testInput{
			imageRef: "registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd",
			mirrors: map[string][]string{
				"registry.ci.openshift.org/openshift": {
					"mirror1.disconnected.local:5000/openshift",
					"mirror2.disconnected.local:5000/openshift",
				},
			},
			expected: "mirror1.disconnected.local:5000/openshift/aws-cluster-api-controllers@sha256:aabbccdd",
		}),
		Entry("no matching source returns original ref", testInput{
			imageRef: "registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd",
			mirrors: map[string][]string{
				"quay.io/unrelated": {"mirror.local/unrelated"},
			},
			expected: "registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd",
		}),
		Entry("partial path segment should not match", testInput{
			imageRef: "registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd",
			mirrors: map[string][]string{
				"registry.ci.openshift.org/open": {"mirror.local/open"},
			},
			expected: "registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd",
		}),
		Entry("source with empty mirrors slice returns original ref", testInput{
			imageRef: "registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd",
			mirrors: map[string][]string{
				"registry.ci.openshift.org/openshift": {},
			},
			expected: "registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd",
		}),
		Entry("source matches at digest boundary", testInput{
			imageRef: "registry.ci.openshift.org/openshift/aws-cluster-api-controllers@sha256:aabbccdd",
			mirrors: map[string][]string{
				"registry.ci.openshift.org/openshift/aws-cluster-api-controllers": {
					"mirror.local/capi",
				},
			},
			expected: "mirror.local/capi@sha256:aabbccdd",
		}),
		Entry("wildcard match rewrites hostname and preserves path", testInput{
			imageRef: "registry.redhat.io/product/repo@sha256:aabbccdd",
			mirrors: map[string][]string{
				"*.redhat.io": {"mirror.local/redhat"},
			},
			expected:       "mirror.local/redhat/product/repo@sha256:aabbccdd",
			expectedSource: "*.redhat.io",
		}),
		Entry("literal match takes precedence over wildcard", testInput{
			imageRef: "registry.redhat.io/product/repo@sha256:aabbccdd",
			mirrors: map[string][]string{
				"*.redhat.io":                     {"mirror.local/redhat"},
				"registry.redhat.io/product/repo": {"mirror.local/specific"},
			},
			expected:       "mirror.local/specific@sha256:aabbccdd",
			expectedSource: "registry.redhat.io/product/repo",
		}),
		Entry("hostname-only literal takes precedence over wildcard", testInput{
			imageRef: "registry.redhat.io/product/repo@sha256:aabbccdd",
			mirrors: map[string][]string{
				"*.redhat.io":        {"mirror.local/redhat"},
				"registry.redhat.io": {"mirror.local/specific"},
			},
			expected:       "mirror.local/specific/product/repo@sha256:aabbccdd",
			expectedSource: "registry.redhat.io",
		}),
		Entry("longest wildcard wins", testInput{
			imageRef: "sub.registry.redhat.io/product/repo@sha256:aabbccdd",
			mirrors: map[string][]string{
				"*.redhat.io":          {"mirror.local/redhat"},
				"*.registry.redhat.io": {"mirror.local/registry"},
			},
			expected:       "mirror.local/registry/product/repo@sha256:aabbccdd",
			expectedSource: "*.registry.redhat.io",
		}),
		Entry("wildcard matches ref with port in hostname", testInput{
			imageRef: "registry.redhat.io:5000/product/repo@sha256:aabbccdd",
			mirrors: map[string][]string{
				"*.redhat.io": {"mirror.local/redhat"},
			},
			expected: "mirror.local/redhat/product/repo@sha256:aabbccdd",
		}),
		Entry("literal with empty mirrors falls through to wildcard", testInput{
			imageRef: "registry.redhat.io/product/repo@sha256:aabbccdd",
			mirrors: map[string][]string{
				"registry.redhat.io/product/repo": {},
				"*.redhat.io":                     {"mirror.local/redhat"},
			},
			expected:       "mirror.local/redhat/product/repo@sha256:aabbccdd",
			expectedSource: "*.redhat.io",
		}),
		Entry("wildcard with empty mirrors slice returns original ref", testInput{
			imageRef: "registry.redhat.io/product/repo@sha256:aabbccdd",
			mirrors: map[string][]string{
				"*.redhat.io": {},
			},
			expected: "registry.redhat.io/product/repo@sha256:aabbccdd",
		}),
	)
})

var _ = Describe("getImageRegistryMirrors", func() {
	var (
		ctx        context.Context
		fullScheme *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		fullScheme = runtime.NewScheme()
		Expect(configv1.AddToScheme(fullScheme)).To(Succeed())
		Expect(operatorv1alpha1.AddToScheme(fullScheme)).To(Succeed())
	})

	It("should return merged map when both IDMS and ICSP are present", func() {
		c := fake.NewClientBuilder().WithScheme(fullScheme).WithObjects(
			&configv1.ImageDigestMirrorSet{
				ObjectMeta: metav1.ObjectMeta{Name: "idms-1"},
				Spec: configv1.ImageDigestMirrorSetSpec{
					ImageDigestMirrors: []configv1.ImageDigestMirrors{
						{
							Source:  "registry.ci.openshift.org/openshift",
							Mirrors: []configv1.ImageMirror{"virthost.ostest.test.metalkube.org:5000/localimages/local-release-image"},
						},
					},
				},
			},
			&operatorv1alpha1.ImageContentSourcePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "icsp-1"},
				Spec: operatorv1alpha1.ImageContentSourcePolicySpec{
					RepositoryDigestMirrors: []operatorv1alpha1.RepositoryDigestMirrors{
						{
							Source:  "quay-proxy.ci.openshift.org/openshift/ci",
							Mirrors: []string{"virthost.ostest.test.metalkube.org:5000/localimages/ci"},
						},
					},
				},
			},
		).Build()

		mirrors, err := getImageRegistryMirrors(ctx, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(mirrors).To(SatisfyAll(
			HaveLen(2),
			HaveKeyWithValue("registry.ci.openshift.org/openshift",
				[]string{"virthost.ostest.test.metalkube.org:5000/localimages/local-release-image"}),
			HaveKeyWithValue("quay-proxy.ci.openshift.org/openshift/ci",
				[]string{"virthost.ostest.test.metalkube.org:5000/localimages/ci"}),
		))
	})

	It("should return IDMS results only when ICSP CRD is not installed", func() {
		c := fake.NewClientBuilder().WithScheme(fullScheme).WithObjects(
			&configv1.ImageDigestMirrorSet{
				ObjectMeta: metav1.ObjectMeta{Name: "idms-1"},
				Spec: configv1.ImageDigestMirrorSetSpec{
					ImageDigestMirrors: []configv1.ImageDigestMirrors{
						{
							Source:  "registry.ci.openshift.org/openshift",
							Mirrors: []configv1.ImageMirror{"virthost.ostest.test.metalkube.org:5000/localimages/local-release-image"},
						},
					},
				},
			},
		).WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*operatorv1alpha1.ImageContentSourcePolicyList); ok {
					return &meta.NoKindMatchError{}
				}

				return c.List(ctx, list, opts...)
			},
		}).Build()

		mirrors, err := getImageRegistryMirrors(ctx, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(mirrors).To(SatisfyAll(
			HaveLen(1),
			HaveKeyWithValue("registry.ci.openshift.org/openshift",
				[]string{"virthost.ostest.test.metalkube.org:5000/localimages/local-release-image"}),
		))
	})

	It("should return empty map when neither CRD is present", func() {
		c := fake.NewClientBuilder().WithScheme(fullScheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				return &meta.NoKindMatchError{}
			},
		}).Build()

		mirrors, err := getImageRegistryMirrors(ctx, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(mirrors).To(BeEmpty())
	})

	It("should include wildcard sources in the map", func() {
		c := fake.NewClientBuilder().WithScheme(fullScheme).WithObjects(
			&configv1.ImageDigestMirrorSet{
				ObjectMeta: metav1.ObjectMeta{Name: "idms-wildcard"},
				Spec: configv1.ImageDigestMirrorSetSpec{
					ImageDigestMirrors: []configv1.ImageDigestMirrors{
						{
							Source:  "*.redhat.io",
							Mirrors: []configv1.ImageMirror{"mirror.local/redhat"},
						},
						{
							Source:  "registry.ci.openshift.org",
							Mirrors: []configv1.ImageMirror{"mirror.local/openshift"},
						},
					},
				},
			},
		).Build()

		mirrors, err := getImageRegistryMirrors(ctx, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(mirrors).To(SatisfyAll(
			HaveLen(2),
			HaveKeyWithValue("*.redhat.io", []string{"mirror.local/redhat"}),
			HaveKeyWithValue("registry.ci.openshift.org", []string{"mirror.local/openshift"}),
		))
	})

	It("should merge wildcard mirrors from both IDMS and ICSP", func() {
		c := fake.NewClientBuilder().WithScheme(fullScheme).WithObjects(
			&configv1.ImageDigestMirrorSet{
				ObjectMeta: metav1.ObjectMeta{Name: "idms-wildcard"},
				Spec: configv1.ImageDigestMirrorSetSpec{
					ImageDigestMirrors: []configv1.ImageDigestMirrors{
						{
							Source:  "*.redhat.io",
							Mirrors: []configv1.ImageMirror{"mirror-a.local/redhat"},
						},
					},
				},
			},
			&operatorv1alpha1.ImageContentSourcePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "icsp-wildcard"},
				Spec: operatorv1alpha1.ImageContentSourcePolicySpec{
					RepositoryDigestMirrors: []operatorv1alpha1.RepositoryDigestMirrors{
						{
							Source:  "*.redhat.io",
							Mirrors: []string{"mirror-b.local/redhat"},
						},
					},
				},
			},
		).Build()

		mirrors, err := getImageRegistryMirrors(ctx, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(mirrors).To(HaveKeyWithValue("*.redhat.io",
			[]string{"mirror-a.local/redhat", "mirror-b.local/redhat"}))
	})

	It("should propagate real API errors", func() {
		expectedErr := fmt.Errorf("connection refused")
		c := fake.NewClientBuilder().WithScheme(fullScheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
				return expectedErr
			},
		}).Build()

		_, err := getImageRegistryMirrors(ctx, c)
		Expect(err).To(MatchError(expectedErr))
	})
})
