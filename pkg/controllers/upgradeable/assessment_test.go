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
	"os"
	"path/filepath"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openshiftapiextensionsv1 "github.com/openshift/api/apiextensions/v1"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	assessment "github.com/openshift/cluster-capi-operator/pkg/upgradeable"
	createrequirements "github.com/openshift/cluster-capi-operator/pkg/upgradeable/create-requirements"
	extractimages "github.com/openshift/cluster-capi-operator/pkg/upgradeable/extract-images"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

var (
	assessmentClient client.Client
	assessmentOwner  *configv1.ClusterOperator
)

var _ = BeforeSuite(func() {
	env := &envtest.Environment{}
	_, c, err := test.StartEnvTest(env)
	Expect(err).NotTo(HaveOccurred())

	assessmentClient = c

	DeferCleanup(func() { Expect(test.StopEnvTest(env)).To(Succeed()) })
	Expect(c.Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: assessment.Namespace}})).To(Succeed())
	assessmentOwner = &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: operatorstatus.ClusterOperatorName}}
	Expect(c.Create(context.Background(), assessmentOwner)).To(Succeed())
})

var _ = Describe("Assessment chain", func() {
	ctx := context.Background()

	var r *UpgradeableController

	BeforeEach(func() {
		r = &UpgradeableController{Client: assessmentClient, ContainerImage: "operator:latest"}
	})
	AfterEach(func() {
		Expect(assessmentClient.DeleteAllOf(ctx, &corev1.Pod{}, client.InNamespace(assessment.Namespace), client.GracePeriodSeconds(0))).To(Succeed())
		Expect(assessmentClient.DeleteAllOf(ctx, &corev1.ConfigMap{}, client.InNamespace(assessment.Namespace))).To(Succeed())
		Expect(assessmentClient.DeleteAllOf(ctx, &openshiftapiextensionsv1.CompatibilityRequirement{})).To(Succeed())
	})

	It("should run dedicated assessment binaries from the operator image", func() {
		target := &configv1.Release{Version: "4.17.0", Image: "release:latest"}
		extractor := r.extractorPod("4.16.0", target, assessmentOwner)
		Expect(extractor.Spec.Containers).To(ConsistOf(SatisfyAll(
			HaveField("Image", Equal(r.ContainerImage)),
			HaveField("Command", Equal([]string{"/upgradeable-extract-images"})),
			HaveField("Args", Equal([]string{"4.16.0", "4.17.0"})),
		)))

		requirements := r.requirementsPod("4.16.0", target.Version, &corev1.ConfigMap{Data: map[string]string{"core": "provider:latest"}}, assessmentOwner)
		Expect(requirements.Spec.Containers).To(ConsistOf(SatisfyAll(
			HaveField("Image", Equal(r.ContainerImage)),
			HaveField("Command", Equal([]string{"/upgradeable-create-requirements"})),
			HaveField("Args", Equal([]string{"4.16.0", "4.17.0"})),
		)))
	})

	It("should preserve API defaulted pods and recreate them after desired shape changes", func() {
		target := &configv1.Release{Version: "4.17.0", Image: "release:latest"}
		desired := r.extractorPod("4.16.0", target, assessmentOwner)
		Expect(r.ensureAssessmentPod(ctx, desired)).NotTo(BeNil())

		pod := &corev1.Pod{}
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(desired), pod)).To(Succeed())
		uid := pod.UID

		Expect(r.ensureAssessmentPod(ctx, r.extractorPod("4.16.0", target, assessmentOwner))).NotTo(BeNil())
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(desired), pod)).To(Succeed())
		Expect(pod.UID).To(Equal(uid))
		Expect(pod.DeletionTimestamp).To(BeNil())
		pod.Status.Phase = corev1.PodSucceeded
		Expect(assessmentClient.Status().Update(ctx, pod)).To(Succeed())
		Expect(r.ensureAssessmentPod(ctx, r.extractorPod("4.16.0", target, assessmentOwner))).To(BeNil())
		r.ContainerImage = "operator:new"
		Expect(r.ensureAssessmentPod(ctx, r.extractorPod("4.16.0", target, assessmentOwner))).NotTo(BeNil())
		Expect(apierrors.IsNotFound(assessmentClient.Get(ctx, client.ObjectKeyFromObject(desired), pod))).To(BeTrue())
	})

	It("should wait for terminating pods without recreating them", func() {
		target := &configv1.Release{Version: "4.17.0", Image: "release:latest"}
		pod := r.extractorPod("4.16.0", target, assessmentOwner)
		pod.Finalizers = []string{"test.openshift.io/hold"}
		Expect(assessmentClient.Create(ctx, pod)).To(Succeed())
		DeferCleanup(func() {
			Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(pod), pod)).To(Succeed())
			pod.Finalizers = nil
			Expect(assessmentClient.Update(ctx, pod)).To(Succeed())
		})
		Expect(assessmentClient.Delete(ctx, pod)).To(Succeed())
		result := r.ensureAssessmentPod(ctx, r.extractorPod("4.16.0", target, assessmentOwner))
		Expect(result).NotTo(BeNil())
		Expect(result.Error()).NotTo(HaveOccurred())
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(pod), pod)).To(Succeed())
		Expect(pod.DeletionTimestamp).NotTo(BeNil())
	})

	It("should delete old writers before their outputs and preserve the current assessment", func() {
		oldLabels := assessment.AssessmentLabels("4.15.0", "4.16.0")
		liveLabels := assessment.AssessmentLabels("4.16.0", "4.17.0")
		pod := r.extractorPod("4.15.0", &configv1.Release{Version: "4.16.0", Image: "release:old"}, assessmentOwner)
		Expect(assessmentClient.Create(ctx, pod)).To(Succeed())

		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: assessment.ImagesConfigMapName, Namespace: assessment.Namespace, Labels: oldLabels}}
		Expect(assessmentClient.Create(ctx, cm)).To(Succeed())

		crd := &apiextensionsv1.CustomResourceDefinition{}
		Expect(yaml.Unmarshal([]byte(testCRD), crd)).To(Succeed())
		cr, err := createrequirements.BuildRequirement(crd)
		Expect(err).NotTo(HaveOccurred())

		cr.Labels = oldLabels
		Expect(assessmentClient.Create(ctx, cr)).To(Succeed())

		current := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "current-assessment", Namespace: assessment.Namespace, Labels: liveLabels}}
		Expect(assessmentClient.Create(ctx, current)).To(Succeed())
		done, err := r.cleanup(ctx, liveLabels)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(cm), cm)).To(Succeed())
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(cr), cr)).To(Succeed())
		done, err = r.cleanup(ctx, liveLabels)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(apierrors.IsNotFound(assessmentClient.Get(ctx, client.ObjectKeyFromObject(cm), cm))).To(BeTrue())
		Expect(apierrors.IsNotFound(assessmentClient.Get(ctx, client.ObjectKeyFromObject(cr), cr))).To(BeTrue())
		done, err = r.cleanup(ctx, liveLabels)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(current), current)).To(Succeed())
	})

	It("should surface failed pods as ephemeral errors and recreate them", func() {
		target := &configv1.Release{Version: "4.17.0", Image: "release:latest"}
		pod := r.extractorPod("4.16.0", target, assessmentOwner)
		Expect(r.ensureAssessmentPod(ctx, pod)).NotTo(BeNil())
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(pod), pod)).To(Succeed())
		pod.Status.Phase = corev1.PodFailed
		pod.Status.Reason = "DeadlineExceeded"
		Expect(assessmentClient.Status().Update(ctx, pod)).To(Succeed())
		result := r.ensureAssessmentPod(ctx, r.extractorPod("4.16.0", target, assessmentOwner))
		Expect(result).NotTo(BeNil())
		Expect(result.Error()).To(HaveOccurred())
		Expect(result.WriteClusterOperatorStatus(ctx, logr.Discard(), assessmentClient)).To(Succeed())
		expectUpgradeable(ctx, configv1.ConditionUnknown, "EphemeralError", assessment.ExtractorPodName)
		Expect(apierrors.IsNotFound(assessmentClient.Get(ctx, client.ObjectKeyFromObject(pod), pod))).To(BeTrue())
		Expect(r.ensureAssessmentPod(ctx, r.extractorPod("4.16.0", target, assessmentOwner))).NotTo(BeNil())
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(pod), pod)).To(Succeed())
	})

	It("should extract owned image data and create Warn requirements idempotently", func() {
		root := GinkgoT().TempDir()
		manifest := filepath.Join(root, "images.yaml")
		Expect(os.WriteFile(manifest, []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  annotations:\n    ignored: value\ndata:\n  core: provider:latest\n"), 0600)).To(Succeed())
		Expect(extractimages.Step(ctx, assessmentClient, "4.16.0", "4.17.0", manifest)).To(Succeed())

		cm := &corev1.ConfigMap{}
		Expect(assessmentClient.Get(ctx, client.ObjectKey{Namespace: assessment.Namespace, Name: assessment.ImagesConfigMapName}, cm)).To(Succeed())
		Expect(cm.Annotations).To(BeEmpty())
		Expect(cm.OwnerReferences).To(ContainElement(HaveField("UID", Equal(assessmentOwner.UID))))
		rv := cm.ResourceVersion

		Expect(extractimages.Step(ctx, assessmentClient, "4.16.0", "4.17.0", manifest)).To(Succeed())
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(cm), cm)).To(Succeed())
		Expect(cm.ResourceVersion).To(Equal(rv))

		profileDir := filepath.Join(root, providerimages.VolumeNameForImageRef("provider:latest"), "capi-operator-manifests", "default")
		Expect(os.MkdirAll(profileDir, 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(profileDir, "manifests.yaml"), []byte(testCRD), 0600)).To(Succeed())
		Expect(createrequirements.Step(ctx, assessmentClient, "4.16.0", "4.17.0", root)).To(Succeed())

		crs := &openshiftapiextensionsv1.CompatibilityRequirementList{}
		Expect(assessmentClient.List(ctx, crs)).To(Succeed())
		Expect(crs.Items).To(HaveLen(1))
		cr := &crs.Items[0]
		Expect(cr.Spec.CustomResourceDefinitionSchemaValidation.Action).To(Equal(openshiftapiextensionsv1.CRDAdmitActionWarn))
		Expect(cr.Spec.CompatibilitySchema.RequiredVersions.DefaultSelection).To(Equal(openshiftapiextensionsv1.APIVersionSetTypeAllServed))
		rv = cr.ResourceVersion

		Expect(createrequirements.Step(ctx, assessmentClient, "4.16.0", "4.17.0", root)).To(Succeed())
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(cr), cr)).To(Succeed())
		Expect(cr.ResourceVersion).To(Equal(rv))

		By("waiting for all requirement verdicts before reporting compatibility")

		result := r.processRequirements(ctx, cm.Labels, "4.17.0")
		Expect(result.WriteClusterOperatorStatus(ctx, logr.Discard(), assessmentClient)).To(Succeed())
		expectUpgradeable(ctx, configv1.ConditionUnknown, "Progressing", "")

		cr.Status.CRDName = "widgets.example.com"
		cr.Status.Conditions = []metav1.Condition{
			{Type: "Admitted", Status: metav1.ConditionTrue, Reason: "Admitted", LastTransitionTime: metav1.Now(), ObservedGeneration: cr.Generation},
			{Type: "Compatible", Status: metav1.ConditionFalse, Reason: "RequirementsNotMet", LastTransitionTime: metav1.Now(), ObservedGeneration: cr.Generation},
		}
		Expect(assessmentClient.Status().Update(ctx, cr)).To(Succeed())
		result = r.processRequirements(ctx, cm.Labels, "4.17.0")
		Expect(result.WriteClusterOperatorStatus(ctx, logr.Discard(), assessmentClient)).To(Succeed())
		expectUpgradeable(ctx, configv1.ConditionFalse, "NotCompatible", "widgets.example.com")
		By("recovering after remediation without a version change")

		cr.Status.Conditions[1].Status = metav1.ConditionTrue
		Expect(assessmentClient.Status().Update(ctx, cr)).To(Succeed())
		result = r.processRequirements(ctx, cm.Labels, "4.17.0")
		Expect(result.WriteClusterOperatorStatus(ctx, logr.Discard(), assessmentClient)).To(Succeed())
		expectUpgradeable(ctx, configv1.ConditionTrue, "Compatible", "4.17.0")
		By("removing only the owned Upgradeable condition when there is no target")

		result = UpgradeableResult{ResultGenerator.Success()}
		Expect(result.WriteClusterOperatorStatus(ctx, logr.Discard(), assessmentClient)).To(Succeed())

		co := &configv1.ClusterOperator{}
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(assessmentOwner), co)).To(Succeed())
		Expect(co.Status.Conditions).NotTo(ContainElement(HaveField("Type", Equal(configv1.OperatorUpgradeable))))
	})

	DescribeTable("should wait for complete, fresh requirement verdicts",
		func(admitted, compatible metav1.ConditionStatus, observedGeneration int64) {
			crd := &apiextensionsv1.CustomResourceDefinition{}
			Expect(yaml.Unmarshal([]byte(testCRD), crd)).To(Succeed())
			cr, err := createrequirements.BuildRequirement(crd)
			Expect(err).NotTo(HaveOccurred())

			cr.Labels = assessment.AssessmentLabels("4.16.0", "4.17.0")
			Expect(assessmentClient.Create(ctx, cr)).To(Succeed())
			cr.Status.CRDName = crd.Name

			cr.Status.Conditions = []metav1.Condition{{Type: "Admitted", Status: admitted, Reason: "Observed", LastTransitionTime: metav1.Now(), ObservedGeneration: cr.Generation}}
			if compatible != "" {
				cr.Status.Conditions = append(cr.Status.Conditions, metav1.Condition{Type: "Compatible", Status: compatible, Reason: "Observed", LastTransitionTime: metav1.Now(), ObservedGeneration: observedGeneration})
			}

			Expect(assessmentClient.Status().Update(ctx, cr)).To(Succeed())
			result := r.processRequirements(ctx, cr.Labels, "4.17.0")
			Expect(result.WriteClusterOperatorStatus(ctx, logr.Discard(), assessmentClient)).To(Succeed())
			expectUpgradeable(ctx, configv1.ConditionUnknown, "Progressing", "")
		},
		Entry("not admitted", metav1.ConditionFalse, metav1.ConditionTrue, int64(1)),
		Entry("missing verdict", metav1.ConditionTrue, metav1.ConditionStatus(""), int64(1)),
		Entry("stale verdict", metav1.ConditionTrue, metav1.ConditionTrue, int64(0)),
		Entry("unknown verdict", metav1.ConditionTrue, metav1.ConditionUnknown, int64(1)),
	)
})

func expectUpgradeable(ctx context.Context, status configv1.ConditionStatus, reason, message string) {
	co := &configv1.ClusterOperator{}
	Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(assessmentOwner), co)).To(Succeed())
	Expect(co.Status.Conditions).To(ContainElement(SatisfyAll(HaveField("Type", Equal(configv1.OperatorUpgradeable)), HaveField("Status", Equal(status)), HaveField("Reason", Equal(reason)), HaveField("Message", ContainSubstring(message)))))
}

const testCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: Widget
    plural: widgets
    singular: widget
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
`
