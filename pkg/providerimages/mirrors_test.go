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

var _ = Describe("resolveImageRef", func() {
	type testInput struct {
		imageRef string
		mirrors  map[string][]string
		expected string
	}

	DescribeTable("should resolve image references through mirrors",
		func(tt testInput) {
			result := resolveImageRef(tt.imageRef, tt.mirrors)
			Expect(result).To(Equal(tt.expected))
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
			expected: "virthost.ostest.test.metalkube.org:5000/localimages/aws-cluster-api-controllers@sha256:aabbccdd",
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
			expected: "virthost.ostest.test.metalkube.org:5000/localimages/local-release-image/aws-cluster-api-controllers@sha256:aabbccdd",
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

		mirrors, skippedWildcards, err := getImageRegistryMirrors(ctx, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(skippedWildcards).To(BeEmpty())
		Expect(mirrors).To(HaveLen(2))
		Expect(mirrors).To(HaveKeyWithValue("registry.ci.openshift.org/openshift",
			[]string{"virthost.ostest.test.metalkube.org:5000/localimages/local-release-image"}))
		Expect(mirrors).To(HaveKeyWithValue("quay-proxy.ci.openshift.org/openshift/ci",
			[]string{"virthost.ostest.test.metalkube.org:5000/localimages/ci"}))
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

		mirrors, skippedWildcards, err := getImageRegistryMirrors(ctx, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(skippedWildcards).To(BeEmpty())
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

		mirrors, skippedWildcards, err := getImageRegistryMirrors(ctx, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(skippedWildcards).To(BeEmpty())
		Expect(mirrors).To(BeEmpty())
	})

	It("should filter out wildcard sources", func() {
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

		mirrors, skippedWildcards, err := getImageRegistryMirrors(ctx, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(skippedWildcards).To(ConsistOf("*.redhat.io"))
		Expect(mirrors).To(HaveLen(1))
		Expect(mirrors).NotTo(HaveKey("*.redhat.io"))
		Expect(mirrors).To(HaveKey("registry.ci.openshift.org"))
	})

	It("should propagate real API errors", func() {
		expectedErr := fmt.Errorf("connection refused")
		c := fake.NewClientBuilder().WithScheme(fullScheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
				return expectedErr
			},
		}).Build()

		_, _, err := getImageRegistryMirrors(ctx, c)
		Expect(err).To(MatchError(expectedErr))
	})
})
