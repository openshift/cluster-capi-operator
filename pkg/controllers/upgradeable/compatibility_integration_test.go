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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openshiftapiextensionsv1 "github.com/openshift/api/apiextensions/v1"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	assessment "github.com/openshift/cluster-capi-operator/pkg/upgradeable"
	createrequirements "github.com/openshift/cluster-capi-operator/pkg/upgradeable/create-requirements"
	extractimages "github.com/openshift/cluster-capi-operator/pkg/upgradeable/extract-images"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

var _ = Describe("Upgrade compatibility integration", func() {
	ctx := context.Background()

	const crdName = "widgets.upgradeable.example.com"

	AfterEach(func() {
		By("Removing assessment requirements through the real compatibility reconciler")

		requirements := &openshiftapiextensionsv1.CompatibilityRequirementList{}
		Expect(assessmentClient.List(ctx, requirements)).To(Succeed())
		checker := crdcompatibility.NewCompatibilityRequirementReconciler(assessmentClient)

		for i := range requirements.Items {
			requirement := &requirements.Items[i]
			Expect(assessmentClient.Delete(ctx, requirement)).To(Succeed())
			_, err := checker.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(requirement)})
			Expect(err).NotTo(HaveOccurred())
			Expect(test.CleanupAndWait(ctx, assessmentClient, requirement)).To(Succeed())
		}

		Expect(assessmentClient.DeleteAllOf(ctx, &corev1.Pod{}, client.InNamespace(assessment.Namespace), client.GracePeriodSeconds(0))).To(Succeed())
		Expect(assessmentClient.DeleteAllOf(ctx, &corev1.ConfigMap{}, client.InNamespace(assessment.Namespace))).To(Succeed())
		Expect(test.CleanupAndWait(ctx, assessmentClient,
			&configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: clusterVersionName}},
			&operatorv1alpha1.ClusterAPI{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}},
			&apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: crdName}},
		)).To(Succeed())
	})

	DescribeTable("should assess served version changes according to CRD management",
		func(ownership string, currentVersions, targetVersions []string, expectedStatus configv1.ConditionStatus) {
			By("Declaring whether the installer or an external actor manages the CRD")

			clusterAPI := &operatorv1alpha1.ClusterAPI{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec:       &operatorv1alpha1.ClusterAPISpec{},
			}
			if ownership == "external" {
				clusterAPI.Spec.UnmanagedCustomResourceDefinitions = []string{crdName}
			}

			Expect(assessmentClient.Create(ctx, clusterAPI)).To(Succeed())

			if len(currentVersions) > 0 {
				By("Installing the current CRD without applying the future payload")

				current := test.GenerateCRD(schema.GroupVersionKind{Group: "upgradeable.example.com", Version: currentVersions[0], Kind: "Widget"}, currentVersions[1:]...)
				if ownership == "managed" {
					current.Labels = map[string]string{
						revisiongenerator.ManagedLabelKey: "core",
						"app.kubernetes.io/managed-by":    "boxcutter",
					}
					current.Annotations = map[string]string{operatorstatus.CAPIOperatorIdentifierDomain + "/revision": "test-revision"}
				}

				Expect(assessmentClient.Create(ctx, current)).To(Succeed())
			}

			By("Offering a next minor release through ClusterVersion status")

			cv := &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: clusterVersionName},
				Spec:       configv1.ClusterVersionSpec{ClusterID: "11111111-1111-4111-8111-111111111111"},
			}
			Expect(assessmentClient.Create(ctx, cv)).To(Succeed())
			cv.Status.Desired = configv1.Release{Version: "4.16.0", Image: "release:current"}
			cv.Status.AvailableUpdates = []configv1.Release{{Version: "4.17.0", Image: "release:target"}}
			Expect(assessmentClient.Status().Update(ctx, cv)).To(Succeed())

			By("Running the assessment steps against target image filesystem fixtures")

			target := test.GenerateCRD(schema.GroupVersionKind{Group: "upgradeable.example.com", Version: targetVersions[0], Kind: "Widget"}, targetVersions[1:]...)
			root := writeCompatibilityImageFixture(target)
			r := &UpgradeableController{Client: assessmentClient, ContainerImage: "operator:current"}
			_, err := r.Reconcile(ctx, ctrl.Request{})
			Expect(err).NotTo(HaveOccurred())
			Expect(extractimages.Step(ctx, assessmentClient, "4.16.0", "4.17.0", filepath.Join(root, "images.yaml"))).To(Succeed())
			completeCompatibilityAssessmentPod(ctx, assessment.ExtractorPodName)
			_, err = r.Reconcile(ctx, ctrl.Request{})
			Expect(err).NotTo(HaveOccurred())
			Expect(createrequirements.Step(ctx, assessmentClient, "4.16.0", "4.17.0", root)).To(Succeed())
			completeCompatibilityAssessmentPod(ctx, assessment.RequirementsPodName)

			By("Evaluating the generated requirements with the real compatibility checker")

			requirements := &openshiftapiextensionsv1.CompatibilityRequirementList{}
			Expect(assessmentClient.List(ctx, requirements, client.MatchingLabels(assessment.AssessmentLabels("4.16.0", "4.17.0")))).To(Succeed())
			checker := crdcompatibility.NewCompatibilityRequirementReconciler(assessmentClient)

			for i := range requirements.Items {
				_, err := checker.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&requirements.Items[i])})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Publishing upgradeability without fabricating requirement verdicts")

			_, err = r.Reconcile(ctx, ctrl.Request{})
			Expect(err).NotTo(HaveOccurred())

			reason, message := "Compatible", "4.17.0"
			if expectedStatus == configv1.ConditionFalse {
				reason, message = "NotCompatible", crdName
			}

			expectUpgradeable(ctx, expectedStatus, reason, message)
		},
		Entry("should allow an installer managed CRD to gain a served version during upgrade",
			"managed", []string{"v1beta1"}, []string{"v1beta1", "v1beta2"}, configv1.ConditionTrue),
		Entry("should block when an unchanged external CRD lacks a version required by future controllers",
			"external", []string{"v1beta1"}, []string{"v1beta1", "v1beta2"}, configv1.ConditionFalse),
		Entry("should block when the target removes a currently served version from an installer managed CRD",
			"managed", []string{"v1beta1", "v1beta2"}, []string{"v1beta1"}, configv1.ConditionFalse),
		Entry("should allow an absent CRD that the target would newly install",
			"absent", []string{}, []string{"v1beta1"}, configv1.ConditionTrue),
	)
})

func writeCompatibilityImageFixture(target *apiextensionsv1.CustomResourceDefinition) string {
	GinkgoHelper()

	root := GinkgoT().TempDir()
	Expect(os.WriteFile(filepath.Join(root, "images.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\ndata:\n  core: provider:target\n"), 0600)).To(Succeed())
	profile := filepath.Join(root, providerimages.VolumeNameForImageRef("provider:target"), "capi-operator-manifests", "default")
	Expect(os.MkdirAll(profile, 0700)).To(Succeed())

	data, err := yaml.Marshal(target)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(profile, "manifests.yaml"), data, 0600)).To(Succeed())

	return root
}

func completeCompatibilityAssessmentPod(ctx context.Context, name string) {
	GinkgoHelper()

	// Envtest has no kubelet. Execute the real step against image fixtures before
	// marking its pod complete, so pod status cannot conceal a missing output.
	pod := &corev1.Pod{}
	Expect(assessmentClient.Get(ctx, client.ObjectKey{Namespace: assessment.Namespace, Name: name}, pod)).To(Succeed())
	pod.Status.Phase = corev1.PodSucceeded
	Expect(assessmentClient.Status().Update(ctx, pod)).To(Succeed())
}
