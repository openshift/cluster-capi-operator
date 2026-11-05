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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
		g.Expect(err).ToNot(gomega.HaveOccurred())
		g.Expect(gotMachineSet.Name).To(gomega.Equal(machineSet.Name))
		g.Expect(gotTemplate.Name).To(gomega.Equal(current.Name))
		g.Expect(gotTemplate.Spec.Template.Spec.InstanceType).To(gomega.Equal("m5.large"))
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
		g.Expect(err).ToNot(gomega.HaveOccurred())
		g.Expect(gotTemplate.Name).To(gomega.Equal(referenced.Name))
	})

	t.Run("follows the reference when it is replaced between attempts", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)

		first := newTestAWSMachineTemplate("ms-auth-capi-klmno-14daf3dc", "m6a.xlarge", time.Now().Add(-time.Hour))
		machineSet := newTestCAPIMachineSet("ms-auth-capi-klmno", first.Name)

		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).
			WithObjects(first, machineSet).Build()

		_, gotTemplate, err := resolveAWSMachineTemplateForMachineSet(ctx, fakeClient, machineSet.Name)
		g.Expect(err).ToNot(gomega.HaveOccurred())
		g.Expect(gotTemplate.Name).To(gomega.Equal(first.Name))

		second := newTestAWSMachineTemplate("ms-auth-capi-klmno-4110f2a2", "m5.large", time.Now())
		g.Expect(fakeClient.Create(ctx, second)).To(gomega.Succeed())

		updated := &clusterv1.MachineSet{}
		g.Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(machineSet), updated)).To(gomega.Succeed())
		updated.Spec.Template.Spec.InfrastructureRef.Name = second.Name
		g.Expect(fakeClient.Update(ctx, updated)).To(gomega.Succeed())

		_, gotTemplate, err = resolveAWSMachineTemplateForMachineSet(ctx, fakeClient, machineSet.Name)
		g.Expect(err).ToNot(gomega.HaveOccurred())
		g.Expect(gotTemplate.Name).To(gomega.Equal(second.Name),
			"a re-read of the MachineSet should follow the replaced infrastructureRef")
	})

	t.Run("errors when the MachineSet does not exist", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)

		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

		_, _, err := resolveAWSMachineTemplateForMachineSet(ctx, fakeClient, "ms-missing")
		g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("get Cluster API MachineSet openshift-cluster-api/ms-missing")))
	})

	t.Run("errors when the referenced template does not exist", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)

		machineSet := newTestCAPIMachineSet("ms-auth-capi-pqrst", "ms-auth-capi-pqrst-4110f2a2")

		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(machineSet).Build()

		_, _, err := resolveAWSMachineTemplateForMachineSet(ctx, fakeClient, machineSet.Name)
		g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("get AWSMachineTemplate openshift-cluster-api/ms-auth-capi-pqrst-4110f2a2 referenced by Cluster API MachineSet ms-auth-capi-pqrst")))
	})

	t.Run("errors when the MachineSet has no infrastructureRef name", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)

		machineSet := newTestCAPIMachineSet("ms-auth-capi-uvwxy", "")

		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(machineSet).Build()

		_, _, err := resolveAWSMachineTemplateForMachineSet(ctx, fakeClient, machineSet.Name)
		g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("no infrastructureRef name on Cluster API MachineSet")))
	})

	t.Run("errors when the infrastructureRef is not an AWSMachineTemplate", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)

		machineSet := newTestCAPIMachineSet("ms-auth-capi-zabcd", "ms-auth-capi-zabcd-4110f2a2")
		machineSet.Spec.Template.Spec.InfrastructureRef.Kind = "GCPMachineTemplate"

		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(machineSet).Build()

		_, _, err := resolveAWSMachineTemplateForMachineSet(ctx, fakeClient, machineSet.Name)
		g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(`unexpected infrastructure kind "GCPMachineTemplate"`)))
	})

	t.Run("errors when the MachineSet name is empty", func(t *testing.T) {
		t.Parallel()

		g := gomega.NewWithT(t)

		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

		_, _, err := resolveAWSMachineTemplateForMachineSet(ctx, fakeClient, "")
		g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("machineSet name cannot be empty")))
	})
}
