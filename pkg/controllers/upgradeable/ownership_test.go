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
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openshiftapiextensionsv1 "github.com/openshift/api/apiextensions/v1"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility"
	assessment "github.com/openshift/cluster-capi-operator/pkg/controllers/upgradeable/pods"
	createrequirements "github.com/openshift/cluster-capi-operator/pkg/controllers/upgradeable/pods/create-requirements"
	extractimages "github.com/openshift/cluster-capi-operator/pkg/controllers/upgradeable/pods/extract-images"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("Upgradeable CRD ownership", func() {
	ctx := context.Background()
	gvk := schema.GroupVersionKind{Group: "ownership.example.com", Version: "v1beta1", Kind: "Widget"}

	var (
		k          komega.Komega
		r          *UpgradeableController
		clusterAPI *operatorv1alpha1.ClusterAPI
		crd        *apiextensionsv1.CustomResourceDefinition
		mgrCancel  context.CancelFunc
		mgrDone    chan struct{}
	)

	BeforeEach(func() {
		k = komega.New(assessmentClient)
		r = &UpgradeableController{Client: assessmentClient, ContainerImage: "operator:current"}
		mgrCancel = nil
		clusterAPI = &operatorv1alpha1.ClusterAPI{
			ObjectMeta: metav1.ObjectMeta{Name: clusterAPIName},
			Spec:       &operatorv1alpha1.ClusterAPISpec{},
		}
		Expect(assessmentClient.Create(ctx, clusterAPI)).To(Succeed())

		crd = test.GenerateCRD(gvk)
		Expect(assessmentClient.Create(ctx, crd)).To(Succeed())

		cv := &configv1.ClusterVersion{
			ObjectMeta: metav1.ObjectMeta{Name: clusterVersionName},
			Spec:       configv1.ClusterVersionSpec{ClusterID: "11111111-1111-4111-8111-111111111111"},
		}
		Expect(assessmentClient.Create(ctx, cv)).To(Succeed())
		cv.Status.Desired = configv1.Release{Version: "4.16.0", Image: "release:current"}
		cv.Status.AvailableUpdates = []configv1.Release{{Version: "4.17.0", Image: "release:target"}}
		Expect(assessmentClient.Status().Update(ctx, cv)).To(Succeed())
	})

	AfterEach(func() {
		if mgrCancel != nil {
			mgrCancel()
			Eventually(mgrDone, "10s").Should(BeClosed())
		}

		requirements := &openshiftapiextensionsv1.CompatibilityRequirementList{}
		Expect(assessmentClient.List(ctx, requirements)).To(Succeed())
		checker := crdcompatibility.NewCompatibilityRequirementReconciler(assessmentClient)

		for i := range requirements.Items {
			requirement := &requirements.Items[i]
			Expect(assessmentClient.Delete(ctx, requirement)).To(Succeed())
			_, err := checker.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(requirement)})
			Expect(err).NotTo(HaveOccurred())
			Expect(test.CleanupAndWait(ctx, assessmentClient, requirement)).To(Succeed())
		}

		Expect(assessmentClient.DeleteAllOf(ctx, &corev1.Pod{}, client.InNamespace(assessment.Namespace), client.GracePeriodSeconds(0))).To(Succeed())
		Expect(assessmentClient.DeleteAllOf(ctx, &corev1.ConfigMap{}, client.InNamespace(assessment.Namespace))).To(Succeed())
		Expect(test.CleanupAndWait(ctx, assessmentClient,
			&configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: clusterVersionName}},
			clusterAPI, crd,
		)).To(Succeed())
	})

	runAssessment := func(target *apiextensionsv1.CustomResourceDefinition) *openshiftapiextensionsv1.CompatibilityRequirement {
		GinkgoHelper()

		root := writeCompatibilityImageFixture(target)
		_, err := r.Reconcile(ctx, reconcile.Request{})
		Expect(err).NotTo(HaveOccurred())
		Expect(extractimages.Step(ctx, assessmentClient, "4.16.0", "4.17.0", filepath.Join(root, "images.yaml"))).To(Succeed())
		completeCompatibilityAssessmentPod(ctx, assessment.ExtractorPodName)
		_, err = r.Reconcile(ctx, reconcile.Request{})
		Expect(err).NotTo(HaveOccurred())
		Expect(createrequirements.Step(ctx, assessmentClient, "4.16.0", "4.17.0", root)).To(Succeed())
		completeCompatibilityAssessmentPod(ctx, assessment.RequirementsPodName)

		requirements := &openshiftapiextensionsv1.CompatibilityRequirementList{}
		Expect(assessmentClient.List(ctx, requirements, client.MatchingLabels(assessment.AssessmentLabels("4.16.0", "4.17.0")))).To(Succeed())
		Expect(requirements.Items).To(HaveLen(1))
		requirement := &requirements.Items[0]
		checker := crdcompatibility.NewCompatibilityRequirementReconciler(assessmentClient)
		_, err = checker.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(requirement)})
		Expect(err).NotTo(HaveOccurred())
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(requirement), requirement)).To(Succeed())

		return requirement
	}

	It("should reassess foreign CRDs on ownership and presence changes without a new assessment", func() {
		By("Completing a compatible assessment for a foreign CRD")

		requirement := runAssessment(test.GenerateCRD(gvk))
		resourceVersion := requirement.ResourceVersion
		Expect(requirement.Status.Conditions).To(ContainElement(SatisfyAll(
			HaveField("Type", Equal(openshiftapiextensionsv1.CompatibilityRequirementCompatible)),
			HaveField("Status", Equal(metav1.ConditionTrue)),
		)))

		mgr, err := ctrl.NewManager(assessmentConfig, ctrl.Options{
			Scheme:     assessmentClient.Scheme(),
			Metrics:    metricsserver.Options{BindAddress: "0"},
			Controller: controllerconfig.Controller{SkipNameValidation: ptr.To(true)},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.SetupWithManager(mgr)).To(Succeed())

		mgrCtx, cancel := context.WithCancel(ctx)
		mgrCancel = cancel
		mgrDone = make(chan struct{})

		go func() {
			defer GinkgoRecover()
			defer close(mgrDone)

			Expect(mgr.Start(mgrCtx)).To(Succeed())
		}()

		Expect(mgr.GetCache().WaitForCacheSync(mgrCtx)).To(BeTrue())

		expectStatus := func(status configv1.ConditionStatus) {
			GinkgoHelper()
			Eventually(k.Object(assessmentOwner), "10s").Should(HaveField("Status.Conditions", ContainElement(SatisfyAll(
				HaveField("Type", Equal(configv1.OperatorUpgradeable)),
				HaveField("Status", Equal(status)),
			))))
		}

		expectStatus(configv1.ConditionFalse)

		By("Marking the CRD as installer managed without changing its schema")

		Eventually(k.Update(crd, func() { crd.Labels = map[string]string{revisiongenerator.ManagedLabelKey: "core"} })).Should(Succeed())
		expectStatus(configv1.ConditionTrue)

		By("Removing installer ownership")

		Eventually(k.Update(crd, func() { crd.Labels = nil })).Should(Succeed())
		expectStatus(configv1.ConditionFalse)

		By("Removing the foreign CRD so the installer can create it")

		Expect(test.CleanupAndWait(ctx, assessmentClient, crd)).To(Succeed())
		expectStatus(configv1.ConditionTrue)

		By("Recreating the foreign CRD")

		crd = test.GenerateCRD(gvk)
		Expect(assessmentClient.Create(ctx, crd)).To(Succeed())
		expectStatus(configv1.ConditionFalse)

		By("Declaring the compatible CRD unmanaged")

		Eventually(k.Update(clusterAPI, func() { clusterAPI.Spec.UnmanagedCustomResourceDefinitions = []string{crd.Name} })).Should(Succeed())
		expectStatus(configv1.ConditionTrue)

		By("Removing an unmanaged CRD that the installer cannot recreate")

		Expect(test.CleanupAndWait(ctx, assessmentClient, crd)).To(Succeed())
		expectStatus(configv1.ConditionFalse)

		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(requirement), requirement)).To(Succeed())
		Expect(requirement.ResourceVersion).To(Equal(resourceVersion))
	})

	It("should honor an unmanaged declaration before a lingering installer management label", func() {
		By("Allowing installer updates to a managed CRD")

		Eventually(k.Update(crd, func() { crd.Labels = map[string]string{revisiongenerator.ManagedLabelKey: "core"} })).Should(Succeed())

		requirement := runAssessment(test.GenerateCRD(gvk, "v1beta2"))
		Expect(requirement.Status.Conditions).To(ContainElement(SatisfyAll(
			HaveField("Type", Equal(openshiftapiextensionsv1.CompatibilityRequirementCompatible)),
			HaveField("Status", Equal(metav1.ConditionFalse)),
		)))

		_, err := r.Reconcile(ctx, reconcile.Request{})
		Expect(err).NotTo(HaveOccurred())
		expectUpgradeable(ctx, configv1.ConditionTrue, "Compatible", "4.17.0")

		By("Declaring the still labelled CRD unmanaged")

		Eventually(k.Update(clusterAPI, func() { clusterAPI.Spec.UnmanagedCustomResourceDefinitions = []string{crd.Name} })).Should(Succeed())

		_, err = r.Reconcile(ctx, reconcile.Request{})
		Expect(err).NotTo(HaveOccurred())
		expectUpgradeable(ctx, configv1.ConditionFalse, "NotCompatible", crd.Name)
	})
})
