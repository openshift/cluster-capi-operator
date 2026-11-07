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
	assessment "github.com/openshift/cluster-capi-operator/pkg/controllers/upgradeable/pods"
	createrequirements "github.com/openshift/cluster-capi-operator/pkg/controllers/upgradeable/pods/create-requirements"
	extractimages "github.com/openshift/cluster-capi-operator/pkg/controllers/upgradeable/pods/extract-images"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/test"
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
			By("Configuring whether the CRD is managed, unmanaged, or foreign")

			// Create a ClusterAPI object to configure the CRD ownership.
			clusterAPI := &operatorv1alpha1.ClusterAPI{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec:       &operatorv1alpha1.ClusterAPISpec{},
			}
			// If the CRD is unmanaged, add the CRD name to the ClusterAPI object spec.
			if ownership == "unmanaged" {
				clusterAPI.Spec.UnmanagedCustomResourceDefinitions = []string{crdName}
			}

			Expect(assessmentClient.Create(ctx, clusterAPI)).To(Succeed())

			// If there are current versions, install the current CRD without applying the future payload.
			if len(currentVersions) > 0 {
				By("Installing the current CRD without applying the future payload")

				// Generate the current CRD.
				current := test.GenerateCRD(schema.GroupVersionKind{Group: "upgradeable.example.com", Version: currentVersions[0], Kind: "Widget"}, currentVersions[1:]...)
				// If the CRD is managed, add the managed label and annotation.
				if ownership == "managed" {
					current.Labels = map[string]string{
						revisiongenerator.ManagedLabelKey: "core",
						"app.kubernetes.io/managed-by":    "boxcutter",
					}
					current.Annotations = map[string]string{operatorstatus.CAPIOperatorIdentifierDomain + "/revision": "test-revision"}
				}

				// Create the current CRD.
				Expect(assessmentClient.Create(ctx, current)).To(Succeed())
			}

			By("Offering a next minor release through ClusterVersion status")

			// Create a ClusterVersion object with the desired and available updates to offer a next minor release.
			cv := &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: clusterVersionName},
				Spec:       configv1.ClusterVersionSpec{ClusterID: "11111111-1111-4111-8111-111111111111"},
			}
			Expect(assessmentClient.Create(ctx, cv)).To(Succeed())
			cv.Status.Desired = configv1.Release{Version: "4.16.0", Image: "release:current"}
			cv.Status.AvailableUpdates = []configv1.Release{{Version: "4.17.0", Image: "release:target"}}
			Expect(assessmentClient.Status().Update(ctx, cv)).To(Succeed())

			By("Running the assessment steps against target image filesystem fixtures")

			// Generate a target CRD for the upgradeable test.
			target := test.GenerateCRD(schema.GroupVersionKind{Group: "upgradeable.example.com", Version: targetVersions[0], Kind: "Widget"}, targetVersions[1:]...)
			// Write the target CRD to the compatibility image filesystem fixture.
			root := writeCompatibilityImageFixture(target)

			// Create an UpgradeableController instance.
			r := &UpgradeableController{Client: assessmentClient, ContainerImage: "operator:current"}
			// Reconcile the UpgradeableController instance to invoke the extract images step.
			_, err := r.Reconcile(ctx, ctrl.Request{})
			Expect(err).NotTo(HaveOccurred())

			// Extract the images from the compatibility image filesystem fixture.
			Expect(extractimages.Step(ctx, assessmentClient, "4.16.0", "4.17.0", filepath.Join(root, "images.yaml"))).To(Succeed())
			// Mark the extractor pod as complete, so the extract images step can be deemed complete by the UpgradeableController.
			completeCompatibilityAssessmentPod(ctx, assessment.ExtractorPodName)
			// Assert that the extractor created the configmap with the images.
			configmap := &corev1.ConfigMap{}
			Expect(assessmentClient.Get(ctx, client.ObjectKey{Namespace: assessment.Namespace, Name: assessment.ImagesConfigMapName}, configmap)).To(Succeed())
			Expect(configmap.Data["core"]).To(Equal("provider:target"))

			// Reconcile the UpgradeableController instance again. This time, the create requirements step will be executed.
			_, err = r.Reconcile(ctx, ctrl.Request{})
			Expect(err).NotTo(HaveOccurred())
			// Invoke the create requirements step pod logic to create the requirements for the upgradeable test.
			Expect(createrequirements.Step(ctx, assessmentClient, "4.16.0", "4.17.0", root)).To(Succeed())
			// Mark the requirements pod as complete, so the create requirements step can be deemed complete by the UpgradeableController.
			completeCompatibilityAssessmentPod(ctx, assessment.RequirementsPodName)

			By("Evaluating the generated requirements with the real compatibility checker")

			// List the compatibility requirements that were created by the create requirements step.
			requirements := &openshiftapiextensionsv1.CompatibilityRequirementList{}
			Expect(assessmentClient.List(ctx, requirements, client.MatchingLabels(assessment.AssessmentLabels("4.16.0", "4.17.0")))).To(Succeed())
			// Create a new CompatibilityRequirementReconciler instance.
			checker := crdcompatibility.NewCompatibilityRequirementReconciler(assessmentClient)

			// Reconcile each compatibility requirement.
			for i := range requirements.Items {
				_, err := checker.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&requirements.Items[i])})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Publishing upgradeability without fabricating requirement verdicts")

			// Reconcile the UpgradeableController instance again. This time, the publish upgradeability step will be executed.
			_, err = r.Reconcile(ctx, ctrl.Request{})
			Expect(err).NotTo(HaveOccurred())

			// Set the reason and message for the upgradeable test based on the expected status.
			reason, message := "Compatible", "4.17.0"
			if expectedStatus == configv1.ConditionFalse {
				reason, message = "NotCompatible", crdName
			}

			// Assert that the upgradeable condition is published to the ClusterVersion object with the expected status, reason, and message.
			expectUpgradeable(ctx, expectedStatus, reason, message)
		},
		Entry("should report Upgradeable=True when the target would add a new served version to a CRD that the installer currently manages",
			"managed",
			[]string{"v1beta1"},            // current
			[]string{"v1beta1", "v1beta2"}, // target
			// expect Upgradeable=True because the CRD is managed and the target would add a new version to it.
			// This is expected lifecycle behavior for a managed CRD.
			configv1.ConditionTrue,
		),
		Entry("should report Upgradeable=False when the target would add a new served version to a CRD that is declared unmanaged",
			"unmanaged",
			[]string{"v1beta1"},            // current
			[]string{"v1beta1", "v1beta2"}, // target
			// expect Upgradeable=False because the CRD is declared unmanaged and the target would add a new version to it.
			// This would require manual intervention to add the new version to the CRD
			// as the cluster-api operator explicitly does not manage unmanage it.
			configv1.ConditionFalse,
		),
		Entry("should report Upgradeable=True when the target would retire a version from a CRD that is managed by the installer",
			"managed",
			[]string{"v1beta1", "v1beta2"}, // current
			[]string{"v1beta2"},            // target
			// expect Upgradeable=True because the CRD is managed
			// and the target would retire a version from it.
			// This is expected lifecycle behavior for a managed CRD.
			configv1.ConditionTrue,
		),
		Entry("should report Upgradeable=True when the target would add a CRD that is currently absent from the cluster (and is not declared unmanaged, meaning it is foreign)",
			"foreign",
			[]string{},          // current
			[]string{"v1beta1"}, // target
			// expect Upgradeable=True because the CRD is absent and foreign,
			// so not known to the cluster-api operator.
			configv1.ConditionTrue,
		),
		Entry("should report Upgradeable=False when the target would add a CRD that is currently absent from the cluster (and is declared unmanaged)",
			"unmanaged",
			[]string{},          // current
			[]string{"v1beta1"}, // target
			// expect Upgradeable=False because the CRD is declared unmanaged
			// and even if it is absent from the cluster
			// it is not guaranteed to be compatible with the target version.
			configv1.ConditionFalse,
		),
	)
})

// writeCompatibilityImageFixture writes the target CRD to the compatibility image filesystem fixture.
// It creates a temporary directory and writes the target CRD to the compatibility image filesystem fixture.
// It returns the path to the temporary directory.
func writeCompatibilityImageFixture(target *apiextensionsv1.CustomResourceDefinition) string {
	GinkgoHelper()

	// Create a temporary directory.
	root := GinkgoT().TempDir()
	// Write the images.yaml file to the temporary directory.
	Expect(os.WriteFile(filepath.Join(root, "images.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\ndata:\n  core: provider:target\n"), 0600)).To(Succeed())
	// Create the profile directory.
	profile := filepath.Join(root, providerimages.VolumeNameForImageRef("provider:target"), "capi-operator-manifests", "default")
	Expect(os.MkdirAll(profile, 0700)).To(Succeed())

	// Write the target CRD to the profile directory.
	data, err := yaml.Marshal(target)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(profile, "manifests.yaml"), data, 0600)).To(Succeed())

	return root
}

// completeCompatibilityAssessmentPod marks a compatibility assessment pod as complete.
// It gets the pod, sets the phase to succeeded, and updates the pod status.
func completeCompatibilityAssessmentPod(ctx context.Context, name string) {
	GinkgoHelper()

	// Envtest has no kubelet so we need to mark the pod as complete manually.
	// Execute the real step against image fixtures before
	// marking its pod complete, so pod status cannot conceal a missing output.
	// This is a workaround to ensure that the pod is marked as complete even if the step fails.

	// Get the pod.
	pod := &corev1.Pod{}
	Expect(assessmentClient.Get(ctx, client.ObjectKey{Namespace: assessment.Namespace, Name: name}, pod)).To(Succeed())
	// Set the phase to succeeded.
	pod.Status.Phase = corev1.PodSucceeded
	// Update the pod status.
	Expect(assessmentClient.Status().Update(ctx, pod)).To(Succeed())
}
