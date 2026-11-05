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
package upgradeable

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openshiftapiextensionsv1 "github.com/openshift/api/apiextensions/v1"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	assessment "github.com/openshift/cluster-capi-operator/pkg/upgradeable"
	createrequirements "github.com/openshift/cluster-capi-operator/pkg/upgradeable/create-requirements"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var _ = Describe("CRD conflict diagnostics", func() {
	ctx := context.Background()

	const diagnostic = "served versions removed : v1beta2"

	newRequirement := func(crd *apiextensionsv1.CustomResourceDefinition, compatible metav1.ConditionStatus) *openshiftapiextensionsv1.CompatibilityRequirement {
		GinkgoHelper()

		requirement, err := createrequirements.BuildRequirement(crd)
		Expect(err).NotTo(HaveOccurred())

		compatibilityReason := openshiftapiextensionsv1.CompatibilityRequirementCompatibleReason
		compatibilityMessage := "The CRD is compatible with this requirement"

		if compatible == metav1.ConditionFalse {
			compatibilityReason = openshiftapiextensionsv1.CompatibilityRequirementRequirementsNotMetReason
			compatibilityMessage = diagnostic
		}

		requirement.Generation = 1
		requirement.Labels = assessment.AssessmentLabels("4.16.0", "4.17.0")
		requirement.Status.CRDName = crd.Name
		requirement.Status.Conditions = []metav1.Condition{
			{Type: "Admitted", Status: metav1.ConditionTrue, Reason: "Admitted", LastTransitionTime: metav1.Now(), ObservedGeneration: 1},
			{Type: "Compatible", Status: compatible, Reason: compatibilityReason, Message: compatibilityMessage, LastTransitionTime: metav1.Now(), ObservedGeneration: 1},
		}

		return requirement
	}

	DescribeTable("should explain conflicts without changing ownership decisions",
		func(ownership string, present bool, compatible metav1.ConditionStatus, reason crdConflictReason, remediation string) {
			crd := test.GenerateCRD(schema.GroupVersionKind{Group: "conflicts.example.com", Version: "v1beta1", Kind: "Widget"})
			requirement := newRequirement(crd, compatible)

			if !present {
				requirement.Status.Conditions[1].Reason = openshiftapiextensionsv1.CompatibilityRequirementCRDNotFoundReason
				requirement.Status.Conditions[1].Message = "The target CRD was not found"
			}

			reader := fake.NewClientBuilder().WithScheme(assessmentClient.Scheme())

			if ownership == "managed" {
				crd.Labels = map[string]string{revisiongenerator.ManagedLabelKey: "core"}
			}

			if present {
				reader.WithObjects(crd)
			}

			unmanaged := sets.New[string]()
			if ownership == "unmanaged" {
				unmanaged.Insert(crd.Name)
			}

			r := &UpgradeableController{Client: reader.Build()}

			err := r.requirementConflicts(ctx, requirement, unmanaged)
			if reason == "" {
				Expect(err).NotTo(HaveOccurred())
				return
			}

			var conflict *crdConflictError
			Expect(errors.As(err, &conflict)).To(BeTrue())
			Expect(conflict).To(SatisfyAll(
				HaveField("CRDName", Equal(crd.Name)),
				HaveField("Reason", Equal(reason)),
				HaveField("CompatibilityReason", Equal(requirement.Status.Conditions[1].Reason)),
				HaveField("CompatibilityMessage", Equal(requirement.Status.Conditions[1].Message)),
				HaveField("Remediation", ContainSubstring(remediation)),
			))
			Expect(err).To(MatchError(fmt.Sprintf("%s [%s]: %s\n  CompatibilityRequirement: %s: %s\n  Remediation: %s",
				crd.Name, reason, conflict.Message, conflict.CompatibilityReason, conflict.CompatibilityMessage, conflict.Remediation)))
			Expect(conflict.Remediation).NotTo(ContainSubstring("CompatibilityRequirement"))

			if reason == unmanagedCRDIncompatibleReason {
				Expect(conflict.CompatibilityMessage).To(ContainSubstring(diagnostic))
			}
		},
		Entry("foreign CRD requires an explicit declaration", "foreign", true, metav1.ConditionTrue, foreignCRDReason, "ClusterAPI/cluster.spec.unmanagedCustomResourceDefinitions"),
		Entry("missing unmanaged CRD must be installed externally", "unmanaged", false, metav1.ConditionFalse, unmanagedCRDNotFoundReason, "external CRD manager install"),
		Entry("incompatible unmanaged CRD must be updated externally", "unmanaged", true, metav1.ConditionFalse, unmanagedCRDIncompatibleReason, "external CRD manager update"),
		Entry("managed CRD can be updated", "managed", true, metav1.ConditionFalse, crdConflictReason(""), ""),
		Entry("compatible unmanaged CRD is accepted", "unmanaged", true, metav1.ConditionTrue, crdConflictReason(""), ""),
		Entry("absent foreign CRD can be installed", "foreign", false, metav1.ConditionFalse, crdConflictReason(""), ""),
		Entry("absent managed CRD can be installed", "managed", false, metav1.ConditionFalse, crdConflictReason(""), ""),
	)

	It("should aggregate each conflict reason and remediation in stable CRD order", func() {
		foreign := test.GenerateCRD(schema.GroupVersionKind{Group: "a.example.com", Version: "v1beta1", Kind: "Widget"})
		missing := test.GenerateCRD(schema.GroupVersionKind{Group: "z.example.com", Version: "v1beta1", Kind: "Widget"})
		foreignRequirement := newRequirement(foreign, metav1.ConditionTrue)
		foreignRequirement.Name = "z-foreign"
		missingRequirement := newRequirement(missing, metav1.ConditionFalse)
		missingRequirement.Name = "a-missing"
		missingRequirement.Status.Conditions[1].Reason = openshiftapiextensionsv1.CompatibilityRequirementCRDNotFoundReason
		missingRequirement.Status.Conditions[1].Message = "The target CRD was not found"
		clusterAPI := &operatorv1alpha1.ClusterAPI{
			ObjectMeta: metav1.ObjectMeta{Name: clusterAPIName},
			Spec:       &operatorv1alpha1.ClusterAPISpec{UnmanagedCustomResourceDefinitions: []string{missing.Name}},
		}
		reader := fake.NewClientBuilder().WithScheme(assessmentClient.Scheme()).WithObjects(clusterAPI, foreign, foreignRequirement, missingRequirement).Build()
		r := &UpgradeableController{Client: reader}
		unmanaged := sets.New(missing.Name)
		foreignConflict := r.requirementConflicts(ctx, foreignRequirement, unmanaged)
		missingConflict := r.requirementConflicts(ctx, missingRequirement, unmanaged)

		Expect(foreignConflict).To(HaveOccurred())
		Expect(missingConflict).To(HaveOccurred())

		result := r.processRequirements(ctx, foreignRequirement.Labels, "4.17.0")
		Expect(result.Error()).NotTo(HaveOccurred())
		Expect(result.WriteClusterOperatorStatus(ctx, logr.Discard(), assessmentClient)).To(Succeed())
		expectUpgradeable(ctx, configv1.ConditionFalse, "NotCompatible", "Incompatible CRDs:\n- "+foreignConflict.Error()+"\n- "+missingConflict.Error())
	})

	It("should indent multiline compatibility diagnostics within their CRD entry", func() {
		conflict := &crdConflictError{
			CRDName:              "widgets.example.com",
			Reason:               unmanagedCRDIncompatibleReason,
			Message:              "the unmanaged CRD is incompatible.",
			CompatibilityReason:  "RequirementsNotMet",
			CompatibilityMessage: "first mismatch\nsecond mismatch",
			Remediation:          "Update the externally managed CRD.",
		}
		Expect(conflict).To(MatchError("widgets.example.com [UnmanagedCRDIncompatible]: the unmanaged CRD is incompatible.\n  CompatibilityRequirement: RequirementsNotMet: first mismatch\n  second mismatch\n  Remediation: Update the externally managed CRD."))
	})

	It("should keep CRD lookup failures separate from compatibility conflicts", func() {
		crd := test.GenerateCRD(schema.GroupVersionKind{Group: "conflicts.example.com", Version: "v1beta1", Kind: "Widget"})
		requirement := newRequirement(crd, metav1.ConditionTrue)
		clusterAPI := &operatorv1alpha1.ClusterAPI{ObjectMeta: metav1.ObjectMeta{Name: clusterAPIName}, Spec: &operatorv1alpha1.ClusterAPISpec{}}
		denied := apierrors.NewForbidden(schema.GroupResource{Group: apiextensionsv1.GroupName, Resource: "customresourcedefinitions"}, crd.Name, errors.New("access denied"))
		reader := fake.NewClientBuilder().WithScheme(assessmentClient.Scheme()).WithObjects(clusterAPI, requirement).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, isCRD := obj.(*apiextensionsv1.CustomResourceDefinition); isCRD {
					return denied
				}

				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
		r := &UpgradeableController{Client: reader}
		err := r.requirementConflicts(ctx, requirement, sets.New[string]())
		Expect(apierrors.IsForbidden(err)).To(BeTrue())

		var conflict *crdConflictError
		Expect(errors.As(err, &conflict)).To(BeFalse())

		result := r.processRequirements(ctx, requirement.Labels, "4.17.0")
		Expect(result.Error()).To(HaveOccurred())
		Expect(result.WriteClusterOperatorStatus(ctx, logr.Discard(), assessmentClient)).To(Succeed())
		expectUpgradeable(ctx, configv1.ConditionUnknown, "EphemeralError", denied.Error())
	})
})
