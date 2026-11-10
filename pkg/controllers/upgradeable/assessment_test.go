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
	"os"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openshiftapiextensionsv1 "github.com/openshift/api/apiextensions/v1"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	assessment "github.com/openshift/cluster-capi-operator/pkg/controllers/upgradeable/pods"
	createrequirements "github.com/openshift/cluster-capi-operator/pkg/controllers/upgradeable/pods/create-requirements"
	extractimages "github.com/openshift/cluster-capi-operator/pkg/controllers/upgradeable/pods/extract-images"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

var (
	assessmentClient client.Client
	assessmentOwner  *configv1.ClusterOperator
	assessmentConfig *rest.Config
)

var _ = BeforeSuite(func() {
	env := &envtest.Environment{}
	cfg, c, err := test.StartEnvTest(env)
	Expect(err).NotTo(HaveOccurred())

	assessmentClient = c
	assessmentConfig = cfg

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
		Expect(assessmentClient.Create(ctx, &operatorv1alpha1.ClusterAPI{
			ObjectMeta: metav1.ObjectMeta{Name: clusterAPIName},
			Spec:       &operatorv1alpha1.ClusterAPISpec{},
		})).To(Succeed())
	})
	AfterEach(func() {
		Expect(assessmentClient.DeleteAllOf(ctx, &corev1.Pod{}, client.InNamespace(assessment.Namespace), client.GracePeriodSeconds(0))).To(Succeed())
		Expect(assessmentClient.DeleteAllOf(ctx, &corev1.ConfigMap{}, client.InNamespace(assessment.Namespace))).To(Succeed())
		Expect(assessmentClient.DeleteAllOf(ctx, &openshiftapiextensionsv1.CompatibilityRequirement{})).To(Succeed())
		Expect(test.CleanupAndWait(ctx, assessmentClient, &operatorv1alpha1.ClusterAPI{ObjectMeta: metav1.ObjectMeta{Name: clusterAPIName}})).To(Succeed())
	})

	It("should run dedicated assessment binaries from the operator image", func() {
		target := &configv1.Release{Version: "4.17.0", Image: "release:latest"}
		extractor := r.extractorPod("4.16.0", target, assessmentOwner)
		Expect(extractor.Spec.Containers).To(ConsistOf(SatisfyAll(
			HaveField("Image", Equal(r.ContainerImage)),
			HaveField("Command", Equal([]string{"/upgradeable-payload-images-extractor"})),
			HaveField("Args", Equal([]string{"4.16.0", "4.17.0"})),
		)))

		requirements := r.requirementsPod("4.16.0", target.Version, &corev1.ConfigMap{Data: map[string]string{"core": "provider:latest"}}, assessmentOwner)
		Expect(requirements.Spec.Containers).To(ConsistOf(SatisfyAll(
			HaveField("Image", Equal(r.ContainerImage)),
			HaveField("Command", Equal([]string{"/upgradeable-compatibility-requirements"})),
			HaveField("Args", Equal([]string{"4.16.0", "4.17.0"})),
		)))

		By("running the same binaries without a target payload to inspect")

		withoutTarget := r.extractorPod("4.16.0", nil, assessmentOwner)
		Expect(withoutTarget.Spec.Containers).To(ConsistOf(SatisfyAll(
			HaveField("Image", Equal(r.ContainerImage)),
			HaveField("Command", Equal([]string{"/upgradeable-payload-images-extractor"})),
			HaveField("Args", Equal([]string{"4.16.0", ""})),
		)))
		Expect(withoutTarget.Spec.Volumes).To(BeEmpty())

		withoutImages := r.requirementsPod("4.16.0", "", &corev1.ConfigMap{Data: nil}, assessmentOwner)
		Expect(withoutImages.Spec.Containers).To(ConsistOf(SatisfyAll(
			HaveField("Image", Equal(r.ContainerImage)),
			HaveField("Command", Equal([]string{"/upgradeable-compatibility-requirements"})),
			HaveField("Args", Equal([]string{"4.16.0", ""})),
		)))
		Expect(withoutImages.Spec.Volumes).To(BeEmpty())
	})

	It("should clear payload images when there is no target", func() {
		root := GinkgoT().TempDir()
		manifest := filepath.Join(root, "images.yaml")
		Expect(os.WriteFile(manifest, []byte("apiVersion: v1\nkind: ConfigMap\ndata:\n  core: provider:latest\n"), 0600)).To(Succeed())
		Expect(extractimages.Step(ctx, assessmentClient, "4.15.0", "4.16.0", manifest)).To(Succeed())

		key := client.ObjectKey{Namespace: assessment.Namespace, Name: assessment.ImagesConfigMapName}
		images := &corev1.ConfigMap{}
		Expect(assessmentClient.Get(ctx, key, images)).To(Succeed())
		uid := images.UID

		By("applying an empty payload without reading one")

		Expect(extractimages.Step(ctx, assessmentClient, "4.16.0", "", filepath.Join(root, "absent.yaml"))).To(Succeed())

		Expect(assessmentClient.Get(ctx, key, images)).To(Succeed())
		Expect(images.Data).To(BeEmpty())
		Expect(images.Labels).To(SatisfyAll(
			HaveKeyWithValue(assessment.CurrentVersionLabel, "4.16.0"),
			HaveKeyWithValue(assessment.TargetVersionLabel, ""),
			HaveKeyWithValue(assessment.ManagedLabel, assessment.ManagedLabelValue),
		))
		Expect(images.OwnerReferences).To(ContainElement(HaveField("UID", Equal(assessmentOwner.UID))))
		Expect(images.UID).To(Equal(uid))

		By("replaying the empty payload without a new write")

		resourceVersion := images.ResourceVersion

		Expect(extractimages.Step(ctx, assessmentClient, "4.16.0", "", filepath.Join(root, "absent.yaml"))).To(Succeed())
		Expect(assessmentClient.Get(ctx, key, images)).To(Succeed())
		Expect(images.ResourceVersion).To(Equal(resourceVersion))
	})

	It("should preserve API defaulted pods across image changes and recreate them for a new assessment", func() {
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

		By("accepting the completed pod after the operator image changes")

		r.ContainerImage = "operator:new"
		Expect(r.ensureAssessmentPod(ctx, r.extractorPod("4.16.0", target, assessmentOwner))).To(BeNil())
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(desired), pod)).To(Succeed())
		Expect(pod.UID).To(Equal(uid))

		By("recreating the pod when the assessed versions change")

		changed := r.extractorPod("4.15.0", &configv1.Release{Version: "4.16.0", Image: "release:old"}, assessmentOwner)
		Expect(r.ensureAssessmentPod(ctx, changed)).NotTo(BeNil())
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

	It("should converge the requirement set on the target CRDs", func() {
		target := test.GenerateCRD(schema.GroupVersionKind{Group: "target.example.com", Version: "v1beta1", Kind: "Target"})
		root := writeCompatibilityImageFixture(target)

		By("leaving requirements from an earlier assessment in place")

		stale, err := createrequirements.BuildRequirement(test.GenerateCRD(schema.GroupVersionKind{Group: "stale.example.com", Version: "v1beta1", Kind: "Stale"}))
		Expect(err).NotTo(HaveOccurred())

		stale.Labels = assessment.AssessmentLabels("4.15.0", "4.16.0")
		Expect(assessmentClient.Create(ctx, stale)).To(Succeed())

		edited, err := createrequirements.BuildRequirement(target)
		Expect(err).NotTo(HaveOccurred())

		edited.Labels = assessment.AssessmentLabels("4.15.0", "4.16.0")
		Expect(assessmentClient.Create(ctx, edited)).To(Succeed())

		By("converging the target requirement set without deleting the target requirement")

		Expect(createrequirements.Step(ctx, assessmentClient, "4.16.0", "4.17.0", root)).To(Succeed())

		Expect(apierrors.IsNotFound(assessmentClient.Get(ctx, client.ObjectKeyFromObject(stale), stale))).To(BeTrue())

		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(edited), edited)).To(Succeed())
		Expect(edited.Labels).To(HaveKeyWithValue(assessment.CurrentVersionLabel, "4.16.0"))
		Expect(edited.Labels).To(HaveKeyWithValue(assessment.TargetVersionLabel, "4.17.0"))
		Expect(edited.Labels).To(HaveKeyWithValue(assessment.ManagedLabel, assessment.ManagedLabelValue))
		Expect(edited.OwnerReferences).To(ContainElement(HaveField("UID", Equal(assessmentOwner.UID))))

		By("reaping the whole set when there is no target")

		installer, err := createrequirements.BuildRequirement(test.GenerateCRD(schema.GroupVersionKind{Group: "installer.example.com", Version: "v1", Kind: "Installer"}))
		Expect(err).NotTo(HaveOccurred())

		installer.Name = "ccapio-widgets.example.com-1"
		installer.Labels = map[string]string{revisiongenerator.ManagedLabelKey: "compatibility-requirements"}
		Expect(assessmentClient.Create(ctx, installer)).To(Succeed())

		Expect(createrequirements.Step(ctx, assessmentClient, "4.16.0", "", filepath.Join(root, "absent"))).To(Succeed())

		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(stale), stale)).To(test.BeK8SNotFound())
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(edited), edited)).To(test.BeK8SNotFound())
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(installer), installer)).To(Succeed())

		Expect(createrequirements.Step(ctx, assessmentClient, "4.16.0", "", filepath.Join(root, "absent"))).To(Succeed())
	})

	It("should converge on an empty requirement set when the target ships only placeholder images", func() {
		root := GinkgoT().TempDir()
		stubProviderImage(root, "placeholder:target")

		Expect(createrequirements.Step(ctx, assessmentClient, "4.16.0", "4.17.0", root)).To(Succeed())

		requirements := &openshiftapiextensionsv1.CompatibilityRequirementList{}
		Expect(assessmentClient.List(ctx, requirements)).To(Succeed())
		Expect(requirements.Items).To(BeEmpty())
	})

	It("should not apply a requirement whose previous deletion is still pending", func() {
		target := test.GenerateCRD(schema.GroupVersionKind{Group: "pending.example.com", Version: "v1beta1", Kind: "Pending"})
		root := writeCompatibilityImageFixture(target)

		pending, err := createrequirements.BuildRequirement(target)
		Expect(err).NotTo(HaveOccurred())

		pending.Labels = assessment.AssessmentLabels("4.16.0", "4.17.0")
		pending.Finalizers = []string{holdFinalizer}
		Expect(assessmentClient.Create(ctx, pending)).To(Succeed())
		Expect(assessmentClient.Delete(ctx, pending)).To(Succeed())

		stepCtx, stopStep := context.WithCancel(ctx)
		done := make(chan struct{})

		var stepErr error

		go func() {
			defer GinkgoRecover()
			defer close(done)

			stepErr = createrequirements.Step(stepCtx, assessmentClient, "4.16.0", "4.17.0", root)
		}()

		DeferCleanup(func() {
			stopStep()
			Eventually(done, "5s").Should(BeClosed())
			releaseHold(ctx, pending)
		})

		By("waiting while the previous requirement is terminating")

		Consistently(done, "1s").ShouldNot(BeClosed())

		terminating := &openshiftapiextensionsv1.CompatibilityRequirement{}
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(pending), terminating)).To(Succeed())
		Expect(terminating.DeletionTimestamp).NotTo(BeNil())

		By("applying the requirement only once its previous deletion completed")

		releaseHold(ctx, pending)

		Eventually(done, "5s").Should(BeClosed())
		Expect(stepErr).NotTo(HaveOccurred())

		applied := &openshiftapiextensionsv1.CompatibilityRequirement{}
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(pending), applied)).To(Succeed())
		Expect(applied.DeletionTimestamp).To(BeNil())
		Expect(applied.Labels).To(SatisfyAll(
			HaveKeyWithValue(assessment.CurrentVersionLabel, "4.16.0"),
			HaveKeyWithValue(assessment.TargetVersionLabel, "4.17.0"),
		))
		Expect(applied.Spec.CustomResourceDefinitionSchemaValidation.Action).To(Equal(openshiftapiextensionsv1.CRDAdmitActionWarn))
	})

	It("should fail terminally naming requirements that never finalize", func() {
		target := test.GenerateCRD(schema.GroupVersionKind{Group: "stuck.example.com", Version: "v1beta1", Kind: "Stuck"})
		root := writeCompatibilityImageFixture(target)

		stuck, err := createrequirements.BuildRequirement(target)
		Expect(err).NotTo(HaveOccurred())

		stuck.Labels = assessment.AssessmentLabels("4.16.0", "4.17.0")
		stuck.Finalizers = []string{holdFinalizer}
		Expect(assessmentClient.Create(ctx, stuck)).To(Succeed())
		Expect(assessmentClient.Delete(ctx, stuck)).To(Succeed())

		stepCtx, stopStep := context.WithTimeout(ctx, 500*time.Millisecond)
		DeferCleanup(stopStep)
		DeferCleanup(func() { releaseHold(ctx, stuck) })

		stepErr := createrequirements.Step(stepCtx, assessmentClient, "4.16.0", "4.17.0", root)
		Expect(stepErr).To(HaveOccurred())
		Expect(assessment.IsTerminal(stepErr)).To(BeTrue())
		Expect(stepErr.Error()).To(ContainSubstring(stuck.Name))

		terminating := &openshiftapiextensionsv1.CompatibilityRequirement{}
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(stuck), terminating)).To(Succeed())
		Expect(terminating.DeletionTimestamp).NotTo(BeNil())
	})

	It("should surface failed pods as ephemeral errors and recreate them", func() {
		events := record.NewFakeRecorder(10)
		r.Recorder = events

		target := &configv1.Release{Version: "4.17.0", Image: "release:latest"}
		newPod := func() *corev1.Pod {
			return r.extractorPod("4.16.0", target, assessmentOwner)
		}

		By("creating the extractor pod of the current assessment")

		Expect(r.ensureAssessmentPod(ctx, newPod())).NotTo(BeNil())

		By("failing the pod, as a crashed assessment binary would")

		failAssessmentPod(ctx, newPod(), assessmentFailureDetail)

		By("reporting an ephemeral error with the failure detail")

		result := r.ensureAssessmentPod(ctx, newPod())
		Expect(result).NotTo(BeNil())
		Expect(result.Error()).To(HaveOccurred())
		Expect(errors.Is(result.Error(), errAssessmentPodFailed)).To(BeTrue(), "the error should wrap errAssessmentPodFailed")
		Expect(nextEvent(events)).To(SatisfyAll(
			ContainSubstring(corev1.EventTypeWarning),
			ContainSubstring(assessmentPodFailedReason),
			ContainSubstring("exit code 1 (Error): "+assessmentFailureDetail),
		))
		Expect(result.WriteClusterOperatorStatus(ctx, logr.Discard(), assessmentClient)).To(Succeed())
		expectUpgradeable(ctx, configv1.ConditionUnknown, "EphemeralError", "exit code 1 (Error): "+assessmentFailureDetail)

		By("recreating the pod for the next attempt")

		pod := newPod()
		Expect(apierrors.IsNotFound(assessmentClient.Get(ctx, client.ObjectKeyFromObject(pod), pod))).To(BeTrue(), "the failed pod should be deleted so the next attempt recreates it")
		Expect(r.ensureAssessmentPod(ctx, newPod())).NotTo(BeNil())
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(pod), pod)).To(Succeed())
	})

	Describe("reporting assessment pod failures", func() {
		const (
			currentVersion = "4.16.0"
			targetVersion  = "4.17.0"
		)

		var (
			events *record.FakeRecorder
			target *configv1.Release
		)

		newExtractorPod := func() *corev1.Pod {
			return r.extractorPod(currentVersion, target, assessmentOwner)
		}

		// crashOnce fails the current incarnation of the extractor pod, lets the
		// controller report and delete it, and leaves a new incarnation of the same
		// fixed name with a new UID in place, as the backoff of a crash loop does.
		crashOnce := func(detail string) {
			failAssessmentPod(ctx, newExtractorPod(), detail)

			result := r.ensureAssessmentPod(ctx, newExtractorPod())
			Expect(result).NotTo(BeNil())
			Expect(result.Error()).To(HaveOccurred())

			Expect(r.ensureAssessmentPod(ctx, newExtractorPod())).NotTo(BeNil())
		}

		BeforeEach(func() {
			target = &configv1.Release{Version: targetVersion, Image: "release:latest"}
			events = record.NewFakeRecorder(10)
			r.Recorder = events
			Expect(r.ensureAssessmentPod(ctx, newExtractorPod())).NotTo(BeNil())
		})

		It("should not report a repeated failure of a recreated pod", func() {
			crashOnce(assessmentFailureDetail)
			Expect(nextEvent(events)).To(ContainSubstring(assessmentFailureDetail))

			By("failing a new incarnation of the same pod for the same reason")

			crashOnce(assessmentFailureDetail)
			Expect(events.Events).To(HaveLen(0), "a repeated failure of the same pod should not be reported again")
		})

		It("should report a different failure of the same pod", func() {
			crashOnce(assessmentFailureDetail)
			Expect(nextEvent(events)).To(ContainSubstring(assessmentFailureDetail))

			const otherDetail = "boom: cannot mount provider image"
			crashOnce(otherDetail)
			Expect(nextEvent(events)).To(ContainSubstring(otherDetail))
		})

		It("should report the same failure again after the requirements pod succeeded", func() {
			newRequirementsPod := func() *corev1.Pod {
				return r.requirementsPod(currentVersion, targetVersion, &corev1.ConfigMap{}, assessmentOwner)
			}

			By("failing the requirements pod")

			Expect(r.ensureAssessmentPod(ctx, newRequirementsPod())).NotTo(BeNil())
			failAssessmentPod(ctx, newRequirementsPod(), assessmentFailureDetail)
			result := r.ensureAssessmentPod(ctx, newRequirementsPod())
			Expect(result).NotTo(BeNil())
			Expect(result.Error()).To(HaveOccurred())
			Expect(nextEvent(events)).To(ContainSubstring(assessmentFailureDetail))

			By("completing a new incarnation of the requirements pod")

			Expect(r.ensureAssessmentPod(ctx, newRequirementsPod())).NotTo(BeNil())

			succeeded := &corev1.Pod{}
			Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(newRequirementsPod()), succeeded)).To(Succeed())
			succeeded.Status.Phase = corev1.PodSucceeded
			Expect(assessmentClient.Status().Update(ctx, succeeded)).To(Succeed())
			Expect(r.ensureAssessmentPod(ctx, newRequirementsPod())).To(BeNil())

			By("failing another requirements pod for the same reason")

			replaceAssessmentPod(ctx, succeeded)
			Expect(r.ensureAssessmentPod(ctx, newRequirementsPod())).NotTo(BeNil())
			failAssessmentPod(ctx, newRequirementsPod(), assessmentFailureDetail)
			result = r.ensureAssessmentPod(ctx, newRequirementsPod())
			Expect(result).NotTo(BeNil())
			Expect(result.Error()).To(HaveOccurred())
			Expect(nextEvent(events)).To(ContainSubstring(assessmentFailureDetail))
		})

		It("should report the same failure again in a later assessment", func() {
			crashOnce(assessmentFailureDetail)
			Expect(nextEvent(events)).To(ContainSubstring(assessmentFailureDetail))

			By("failing the first pod of an assessment of another target for the same reason")

			// A new target replaces the pod, so the same detail recurs in a new assessment.
			target = &configv1.Release{Version: "4.18.0", Image: "release:next"}
			later := newExtractorPod()

			Eventually(func(g Gomega) {
				g.Expect(r.ensureAssessmentPod(ctx, later)).NotTo(BeNil())

				pod := &corev1.Pod{}
				g.Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(later), pod)).To(Succeed())
				g.Expect(pod.Labels).To(HaveKeyWithValue(assessment.TargetVersionLabel, target.Version))
				g.Expect(pod.DeletionTimestamp).To(BeNil())
			}, "10s", "100ms").Should(Succeed())

			failAssessmentPod(ctx, later, assessmentFailureDetail)

			result := r.ensureAssessmentPod(ctx, later)
			Expect(result).NotTo(BeNil())
			Expect(result.Error()).To(HaveOccurred())
			Expect(nextEvent(events)).To(ContainSubstring(assessmentFailureDetail))
		})

		It("should not emit events without a recorder", func() {
			r.Recorder = nil

			failAssessmentPod(ctx, newExtractorPod(), assessmentFailureDetail)
			result := r.ensureAssessmentPod(ctx, newExtractorPod())
			Expect(result).NotTo(BeNil())
			Expect(result.Error()).To(HaveOccurred())
			Expect(events.Events).To(HaveLen(0), "no event should be emitted without a recorder")
		})
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
		Expect(os.WriteFile(filepath.Join(profileDir, "metadata.yaml"), profileMetadata("provider:latest"), 0600)).To(Succeed())
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

		crd := &apiextensionsv1.CustomResourceDefinition{}
		Expect(yaml.Unmarshal([]byte(testCRD), crd)).To(Succeed())
		Expect(assessmentClient.Create(ctx, crd)).To(Succeed())
		DeferCleanup(func() { Expect(test.CleanupAndWait(ctx, assessmentClient, crd)).To(Succeed()) })

		clusterAPI := &operatorv1alpha1.ClusterAPI{}
		Expect(assessmentClient.Get(ctx, client.ObjectKey{Name: clusterAPIName}, clusterAPI)).To(Succeed())
		clusterAPI.Spec.UnmanagedCustomResourceDefinitions = []string{crd.Name}
		Expect(assessmentClient.Update(ctx, clusterAPI)).To(Succeed())

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
		By("reporting an upgradeable verdict when there is no target")

		result = r.processRequirements(ctx, assessment.AssessmentLabels("4.16.0", ""), "")
		Expect(result.WriteClusterOperatorStatus(ctx, logr.Discard(), assessmentClient)).To(Succeed())
		expectUpgradeable(ctx, configv1.ConditionTrue, "NoUpdate", "next minor")

		co := &configv1.ClusterOperator{}
		Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(assessmentOwner), co)).To(Succeed())
		Expect(co.Status.Conditions).To(ContainElement(HaveField("Type", Equal(configv1.OperatorUpgradeable))))

		By("ignoring a conclusive incompatible verdict when there is no target")

		blockedCRD := test.GenerateCRD(schema.GroupVersionKind{Group: "blocked.example.com", Version: "v1", Kind: "Blocked"})
		blocked, err := createrequirements.BuildRequirement(blockedCRD)
		Expect(err).NotTo(HaveOccurred())

		blocked.Labels = assessment.AssessmentLabels("4.16.0", "")
		Expect(assessmentClient.Create(ctx, blocked)).To(Succeed())
		blocked.Status.CRDName = blockedCRD.Name
		blocked.Status.Conditions = []metav1.Condition{
			{Type: "Admitted", Status: metav1.ConditionTrue, Reason: "Admitted", LastTransitionTime: metav1.Now(), ObservedGeneration: blocked.Generation},
			{Type: "Compatible", Status: metav1.ConditionFalse, Reason: "RequirementsNotMet", LastTransitionTime: metav1.Now(), ObservedGeneration: blocked.Generation},
		}
		Expect(assessmentClient.Status().Update(ctx, blocked)).To(Succeed())

		result = r.processRequirements(ctx, assessment.AssessmentLabels("4.16.0", ""), "")
		Expect(result.WriteClusterOperatorStatus(ctx, logr.Discard(), assessmentClient)).To(Succeed())
		expectUpgradeable(ctx, configv1.ConditionTrue, "NoUpdate", "next minor")
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

const holdFinalizer = "test.openshift.io/hold"

// releaseHold clears the test hold finalizer so a terminating requirement can be collected.
func releaseHold(ctx context.Context, requirement *openshiftapiextensionsv1.CompatibilityRequirement) {
	GinkgoHelper()

	held := &openshiftapiextensionsv1.CompatibilityRequirement{}
	if err := assessmentClient.Get(ctx, client.ObjectKeyFromObject(requirement), held); err != nil {
		Expect(err).To(test.BeK8SNotFound())

		return
	}

	if len(held.Finalizers) == 0 {
		return
	}

	held.Finalizers = nil
	Expect(client.IgnoreNotFound(assessmentClient.Update(ctx, held))).To(Succeed())
}

func expectUpgradeable(ctx context.Context, status configv1.ConditionStatus, reason, message string) {
	co := &configv1.ClusterOperator{}
	Expect(assessmentClient.Get(ctx, client.ObjectKeyFromObject(assessmentOwner), co)).To(Succeed())
	Expect(co.Status.Conditions).To(ContainElement(SatisfyAll(HaveField("Type", Equal(configv1.OperatorUpgradeable)), HaveField("Status", Equal(status)), HaveField("Reason", Equal(reason)), HaveField("Message", ContainSubstring(message)))))
}

// assessmentFailureDetail is a representative container termination message of a
// failed assessment pod.
const assessmentFailureDetail = "boom: cannot read provider image"

// failAssessmentPod marks the fixed-name pod as failed with the given container
// termination message, as a failed assessment binary would.
func failAssessmentPod(ctx context.Context, pod *corev1.Pod, detail string) {
	failed := &corev1.Pod{}
	ExpectWithOffset(1, assessmentClient.Get(ctx, client.ObjectKeyFromObject(pod), failed)).To(Succeed())
	failed.Status.Phase = corev1.PodFailed
	failed.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name: failed.Spec.Containers[0].Name,
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			ExitCode: 1, Reason: "Error", Message: detail,
		}},
	}}
	ExpectWithOffset(1, assessmentClient.Status().Update(ctx, failed)).To(Succeed())
}

// replaceAssessmentPod removes a completed fixed-name pod, so the next assessment
// step creates a new incarnation with a new UID.
func replaceAssessmentPod(ctx context.Context, pod *corev1.Pod) {
	ExpectWithOffset(1, client.IgnoreNotFound(assessmentClient.Delete(ctx, pod))).To(Succeed())
	EventuallyWithOffset(1, func() error {
		return assessmentClient.Get(ctx, client.ObjectKeyFromObject(pod), &corev1.Pod{})
	}, "10s", "100ms").Should(test.BeK8SNotFound())
}

// nextEvent returns the single event pending on the fake recorder.
func nextEvent(recorder *record.FakeRecorder) string {
	ExpectWithOffset(1, recorder.Events).To(HaveLen(1), "expected exactly one event")

	return <-recorder.Events
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
