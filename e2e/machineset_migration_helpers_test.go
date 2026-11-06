//go:build !e2e

// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/onsi/gomega"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// testCAPINamespace is the namespace the CAPI mirrors live in.
const testCAPINamespace = framework.CAPINamespace

// newTestAWSMachineTemplate builds an AWSMachineTemplate carrying an identifiable instance type.
func newTestAWSMachineTemplate(name, instanceType string, createdAt time.Time) *awsv1.AWSMachineTemplate {
	return &awsv1.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         testCAPINamespace,
			CreationTimestamp: metav1.NewTime(createdAt),
		},
		Spec: awsv1.AWSMachineTemplateSpec{
			Template: awsv1.AWSMachineTemplateResource{
				Spec: awsv1.AWSMachineSpec{
					InstanceType: instanceType,
				},
			},
		},
	}
}

// newTestCAPIMachineSet builds a CAPI MachineSet referencing the named AWSMachineTemplate.
func newTestCAPIMachineSet(name, templateName string) *clusterv1.MachineSet {
	machineSet := &clusterv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testCAPINamespace,
		},
	}
	machineSet.Spec.Template.Spec.InfrastructureRef = clusterv1.ContractVersionedObjectReference{
		Kind:     awsMachineTemplateKind,
		Name:     templateName,
		APIGroup: awsv1.GroupVersion.Group,
	}

	return machineSet
}

// TestResolveAWSMachineTemplateForMachineSet covers template resolution when more than one
// template name starts with the MachineSet name, which happens whenever a controller rolls the
// infrastructure template: templates are immutable, so a new object is created and the MachineSet
// infrastructureRef is repointed, leaving the superseded template in place.
func TestResolveAWSMachineTemplateForMachineSet(t *testing.T) {
	ctx := context.Background()

	t.Run("returns the referenced template when several share the MachineSet name prefix", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)

		superseded := newTestAWSMachineTemplate("ms-auth-capi-abcde-14daf3dc", "m6a.xlarge", time.Now().Add(-time.Hour))
		current := newTestAWSMachineTemplate("ms-auth-capi-abcde-4110f2a2", "m5.large", time.Now())
		machineSet := newTestCAPIMachineSet("ms-auth-capi-abcde", current.Name)

		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).
			WithObjects(superseded, current, machineSet).Build()

		gotMachineSet, gotTemplate, err := resolveAWSMachineTemplateForMachineSet(ctx, fakeClient, machineSet.Name)
		g.Expect(err).ToNot(gomega.HaveOccurred(),
			"resolving MachineSet %s should succeed even though templates %s and %s share its name prefix",
			machineSet.Name, superseded.Name, current.Name)
		g.Expect(gotMachineSet.Name).To(gomega.Equal(machineSet.Name),
			"the MachineSet that was read should be returned to the caller")
		g.Expect(gotTemplate.Name).To(gomega.Equal(current.Name),
			"the referenced template should be returned, not the superseded %s", superseded.Name)
		g.Expect(gotTemplate.Spec.Template.Spec.InstanceType).To(gomega.Equal("m5.large"),
			"the returned template should carry the referenced template's spec")
	})

	t.Run("returns the referenced template even when it is not the newest", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)

		referenced := newTestAWSMachineTemplate("ms-auth-capi-fghij-14daf3dc", "m6a.xlarge", time.Now().Add(-time.Hour))
		newer := newTestAWSMachineTemplate("ms-auth-capi-fghij-4110f2a2", "m5.large", time.Now())
		machineSet := newTestCAPIMachineSet("ms-auth-capi-fghij", referenced.Name)

		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).
			WithObjects(referenced, newer, machineSet).Build()

		_, gotTemplate, err := resolveAWSMachineTemplateForMachineSet(ctx, fakeClient, machineSet.Name)
		g.Expect(err).ToNot(gomega.HaveOccurred(),
			"resolving MachineSet %s should succeed", machineSet.Name)
		g.Expect(gotTemplate.Name).To(gomega.Equal(referenced.Name),
			"the referenced template should be returned, not the newer %s created later", newer.Name)
	})

	t.Run("follows the reference when it is replaced between attempts", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)

		first := newTestAWSMachineTemplate("ms-auth-capi-klmno-14daf3dc", "m6a.xlarge", time.Now().Add(-time.Hour))
		machineSet := newTestCAPIMachineSet("ms-auth-capi-klmno", first.Name)

		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).
			WithObjects(first, machineSet).Build()

		_, gotTemplate, err := resolveAWSMachineTemplateForMachineSet(ctx, fakeClient, machineSet.Name)
		g.Expect(err).ToNot(gomega.HaveOccurred(),
			"resolving MachineSet %s should succeed before the reference is replaced", machineSet.Name)
		g.Expect(gotTemplate.Name).To(gomega.Equal(first.Name),
			"the originally referenced template should be returned first")

		second := newTestAWSMachineTemplate("ms-auth-capi-klmno-4110f2a2", "m5.large", time.Now())
		g.Expect(fakeClient.Create(ctx, second)).To(gomega.Succeed(),
			"creating the replacement template %s should succeed", second.Name)

		updated := &clusterv1.MachineSet{}
		g.Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(machineSet), updated)).To(gomega.Succeed(),
			"reading MachineSet %s back should succeed", machineSet.Name)
		updated.Spec.Template.Spec.InfrastructureRef.Name = second.Name
		g.Expect(fakeClient.Update(ctx, updated)).To(gomega.Succeed(),
			"repointing the infrastructureRef of MachineSet %s should succeed", machineSet.Name)

		_, gotTemplate, err = resolveAWSMachineTemplateForMachineSet(ctx, fakeClient, machineSet.Name)
		g.Expect(err).ToNot(gomega.HaveOccurred(),
			"resolving MachineSet %s should succeed after the reference is replaced", machineSet.Name)
		g.Expect(gotTemplate.Name).To(gomega.Equal(second.Name),
			"a re-read of the MachineSet should follow the replaced infrastructureRef to %s", second.Name)
	})

	t.Run("errors when the MachineSet does not exist", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)

		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

		_, _, err := resolveAWSMachineTemplateForMachineSet(ctx, fakeClient, "ms-missing")
		g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("get Cluster API MachineSet openshift-cluster-api/ms-missing")),
			"a missing MachineSet should be reported with its namespaced name so a retrying caller can see what is absent")
	})

	t.Run("errors when the referenced template does not exist", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)

		machineSet := newTestCAPIMachineSet("ms-auth-capi-pqrst", "ms-auth-capi-pqrst-4110f2a2")

		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(machineSet).Build()

		_, _, err := resolveAWSMachineTemplateForMachineSet(ctx, fakeClient, machineSet.Name)
		g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("get AWSMachineTemplate openshift-cluster-api/ms-auth-capi-pqrst-4110f2a2 referenced by Cluster API MachineSet ms-auth-capi-pqrst")),
			"a dangling reference should name both the missing template and the MachineSet that references it")
	})

	t.Run("errors when the MachineSet has no infrastructureRef name", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)

		machineSet := newTestCAPIMachineSet("ms-auth-capi-uvwxy", "")

		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(machineSet).Build()

		_, _, err := resolveAWSMachineTemplateForMachineSet(ctx, fakeClient, machineSet.Name)
		g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("no infrastructureRef name on Cluster API MachineSet")),
			"an unset infrastructureRef should be reported distinctly from a missing template")
	})

	t.Run("errors when the infrastructureRef is not an AWSMachineTemplate", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)

		machineSet := newTestCAPIMachineSet("ms-auth-capi-zabcd", "ms-auth-capi-zabcd-4110f2a2")
		machineSet.Spec.Template.Spec.InfrastructureRef.Kind = "GCPMachineTemplate"

		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(machineSet).Build()

		_, _, err := resolveAWSMachineTemplateForMachineSet(ctx, fakeClient, machineSet.Name)
		g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(`unexpected infrastructure kind "GCPMachineTemplate"`)),
			"a non-AWS infrastructureRef should be reported as a kind mismatch rather than a missing AWSMachineTemplate")
	})

	t.Run("errors when the MachineSet name is empty", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)

		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

		_, _, err := resolveAWSMachineTemplateForMachineSet(ctx, fakeClient, "")
		g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("machineSet name cannot be empty")),
			"an empty MachineSet name should fail fast instead of reading the API")
	})
}

// TestWaitForMachineSetAWSMachineTemplateFollowsReplacedReference exercises the polling helper
// itself, not just the resolver it calls, because the defect this guards against is re-reading the
// MachineSet inside the retry loop rather than caching it outside.
//
// The fake client repoints the MachineSet infrastructureRef at an already existing replacement on
// the first read of the original template, then reports that original template as NotFound for that
// one attempt. A helper that caches the MachineSet, or the reference taken from it, outside the
// polling closure retries the original name, finds that object still present, and returns the
// superseded template. The helper under test must instead re-read the MachineSet and return the
// replacement.
//
// The helper polls on framework.RetryMedium, so the single retry bounds this test at roughly one
// polling interval.
func TestWaitForMachineSetAWSMachineTemplateFollowsReplacedReference(t *testing.T) {
	// Not parallel: this swaps the package-level client the helper reads and Gomega's global
	// failure handler.
	g := gomega.NewWithT(t)

	original := newTestAWSMachineTemplate("ms-auth-capi-waitr-14daf3dc", "m6a.xlarge", time.Now().Add(-time.Hour))
	replacement := newTestAWSMachineTemplate("ms-auth-capi-waitr-4110f2a2", "m5.large", time.Now())
	machineSet := newTestCAPIMachineSet("ms-auth-capi-waitr", original.Name)

	referenceReplaced := false

	testClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).
		WithObjects(original, replacement, machineSet).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, wrapped client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if referenceReplaced || key.Name != original.Name {
					return wrapped.Get(ctx, key, obj, opts...)
				}

				referenceReplaced = true

				current := &clusterv1.MachineSet{}
				if err := wrapped.Get(ctx, client.ObjectKeyFromObject(machineSet), current); err != nil {
					return err
				}

				current.Spec.Template.Spec.InfrastructureRef.Name = replacement.Name
				if err := wrapped.Update(ctx, current); err != nil {
					return err
				}

				return apierrors.NewNotFound(awsv1.GroupVersion.WithResource("awsmachinetemplates").GroupResource(), key.Name)
			},
		}).Build()

	withGomegaFailHandler(t)
	withTestClient(t, testClient, func() {
		gotMachineSet, gotTemplate := waitForMachineSetAWSMachineTemplate(machineSet.Name)

		g.Expect(referenceReplaced).To(gomega.BeTrue(),
			"the fake client should have replaced the infrastructureRef during the first attempt, otherwise this test proves nothing")
		g.Expect(gotTemplate.Name).To(gomega.Equal(replacement.Name),
			"the helper should return the replacement template %q the MachineSet now references, not the superseded %q, which means it must re-read the MachineSet on each attempt instead of caching it outside the polling closure",
			replacement.Name, original.Name)
		g.Expect(gotMachineSet.Spec.Template.Spec.InfrastructureRef.Name).To(gomega.Equal(replacement.Name),
			"the helper should return the re-read MachineSet whose infrastructureRef names %q", replacement.Name)
	})
}

// withGomegaFailHandler routes Gomega's global failure handler, used by the Ginkgo-style helpers
// under test, to t for the duration of the test. The handler is package-global state, so tests
// using it must not run in parallel.
func withGomegaFailHandler(t *testing.T) {
	t.Helper()

	gomega.RegisterTestingT(t)

	t.Cleanup(func() { gomega.RegisterFailHandler(nil) })
}
