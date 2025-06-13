package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/klog"

	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	SynchronizedCondition machinev1beta1.ConditionType = "Synchronized"
	PausedCondition       machinev1beta1.ConditionType = "Paused"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] A MachineSet with MachineAPI Authority", Ordered, func() {
	var mapiMachineSet *machinev1beta1.MachineSet
	var capiMachineSet *capiv1beta1.MachineSet
	var awsMachineTemplate *capav1.AWSMachineTemplate
	var machinesetName = "machineset-auth-mapi"
	var err error

	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this only support on aws", platform))
		}

		if !framework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}

		By("Creating a MAPI MachineSet with AuthoritativeAPI MachineAPI")
		mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, machinesetName, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
		Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet creation should succeed")
	})

	AfterAll(func() {
		mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
		if err != nil {
			klog.Warningf("Skip MAPI MachineSet cleanup: failed to get MAPI MachineSet: %v", err)
			return
		}

		if mapiMachineSet == nil {
			klog.Infof("No MAPI MachineSet found, nothing to clean up.")
			return
		}

		By("Deleting the created MachineSet")
		mapiframework.DeleteMachineSets(cl, mapiMachineSet)
		mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
		framework.WaitForMachineSetsDeleted(cl, capiMachineSet)
		framework.DeleteObjects(cl, awsMachineTemplate)
	})

	Context("when no existing CAPI MachineSet with that name", func() {
		// OCP-78490 - [CAPI][Migration] When MachineAPI is authoritative - create machineset and convert MAPI to CAPI should work
		It("should create the CAPI MachineSet and Machine and template", func() {
			var err error
			Eventually(func() error {
				capiMachineSet, err = framework.GetMachineSet(cl, machinesetName)
				return err
			}, framework.WaitMedium, framework.RetryMedium).Should(Succeed(), "it should be able to get the CAPI machineset")
			Eventually(func() error {
				awsMachineTemplate, err = framework.GetAWSMachineTemplate(cl, machinesetName)
				return err
			}, framework.WaitMedium, framework.RetryMedium).Should(Succeed(), "it should be able to get the awsmachinetemplate")

			mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
			Expect(err).ToNot(HaveOccurred(), "failed to get MAPI machines from machineset")
			capiMachines, err := framework.GetMachinesFromMachineSet(cl, capiMachineSet)
			Expect(err).ToNot(HaveOccurred(), "failed to get CAPI machines from machineset")
			Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name))

			By("should set the MAPI MachineSet status AuthoritativeAPI to 'MachineAPI'")
			Eventually(func() machinev1beta1.MachineAuthority {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
				if err != nil {
					return ""
				}
				return mapiMachineSet.Status.AuthoritativeAPI
			}, framework.WaitShort, framework.RetryShort).Should(Equal(machinev1beta1.MachineAuthorityMachineAPI), "MAPI MachineSet status.AuthoritativeAPI should be MachineAPI")

			By("should update the synchronized condition on the MAPI MachineSet to True")
			Eventually(func() []machinev1beta1.Condition {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
				if err != nil {
					return nil
				}
				return mapiMachineSet.Status.Conditions
			}, framework.WaitShort, framework.RetryShort).Should(
				ContainElement(
					SatisfyAll(
						HaveField("Type", Equal(SynchronizedCondition)),
						HaveField("Status", Equal(corev1.ConditionTrue)),
						HaveField("Reason", Equal("ResourceSynchronized")),
						HaveField("Message", Equal("Successfully synchronized MAPI MachineSet to CAPI")),
					),
				),
			)

			By("should update the Paused condition on the MAPI MachineSet to False")
			Eventually(func() []machinev1beta1.Condition {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
				if err != nil {
					return nil
				}
				return mapiMachineSet.Status.Conditions
			}, framework.WaitShort, framework.RetryShort).Should(
				ContainElement(
					SatisfyAll(
						HaveField("Type", Equal(PausedCondition)),
						HaveField("Status", Equal(corev1.ConditionFalse)),
						HaveField("Reason", Equal("AuthoritativeAPIMachineAPI")),
						HaveField("Message", Equal("The AuthoritativeAPI is set to MachineAPI")),
					),
				),
			)

			By("should update the Paused condition on the CAPI MachineSet to True")
			Eventually(func() []metav1.Condition {
				capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
				if err != nil {
					return nil
				}
				return capiMachineSet.Status.V1Beta2.Conditions
			}, framework.WaitMedium, framework.RetryMedium).Should(
				ContainElement(
					SatisfyAll(
						HaveField("Type", Equal(capiv1beta1.PausedV1Beta2Condition)),
						HaveField("Status", Equal(metav1.ConditionTrue)),
						HaveField("Reason", Equal("Paused")),
						HaveField("Message", Equal("MachineSet has the cluster.x-k8s.io/paused annotation")),
					),
				),
			)

			By("should create CAPI MachineSet and InfraMachineTemplate with CAPI Cluster OwnerReference")
			Eventually(func() []metav1.OwnerReference {
				capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
				if err != nil {
					return nil
				}
				return capiMachineSet.ObjectMeta.OwnerReferences
			}, framework.WaitShort, framework.RetryShort).Should(
				ContainElement(
					SatisfyAll(
						HaveField("BlockOwnerDeletion", ptr.To(true)),
						HaveField("Controller", ptr.To(false)),
						HaveField("Kind", Equal(capiv1beta1.ClusterKind)),
					),
				),
			)

			Eventually(func() []metav1.OwnerReference {
				awsMachineTemplate, err := framework.GetAWSMachineTemplate(cl, machinesetName)
				if err != nil {
					return nil
				}
				return awsMachineTemplate.ObjectMeta.OwnerReferences
			}, framework.WaitShort, framework.RetryShort).Should(
				ContainElement(
					SatisfyAll(
						HaveField("BlockOwnerDeletion", ptr.To(true)),
						HaveField("Controller", ptr.To(false)),
						HaveField("Kind", Equal(capiv1beta1.ClusterKind)),
					),
				),
			)
		})

		// OCP-81819 - [CAPI][Migration] When MachineAPI is authoritative - scale up MAPI MachineSet should work
		It("should scale up MAPI MachineSet from 1 to 2 succeed", func() {
			By("should not take affect when scaling up CAPI MachineSet from 1 to 2")
			framework.ScaleMachineSet(machinesetName, 2)
			capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "Failed to get capiMachineSet %s", machinesetName)

			Eventually(func() int32 {
				capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
				Expect(err).ToNot(HaveOccurred(), "Failed to get capiMachineSet %s", machinesetName)
				return *capiMachineSet.Spec.Replicas
			}, framework.WaitShort, framework.RetryShort).Should(Equal(int32(1)), "replicas should eventually revert to 1")
			mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "Failed to get mapiMachineSet %s", machinesetName)
			Expect(*mapiMachineSet.Spec.Replicas).To(Equal(int32(1)), "replicas should remain 1")

			By("should take affect when scaling up MAPI MachineSet")
			Expect(mapiframework.ScaleMachineSet(mapiMachineSet.GetName(), 2)).To(Succeed(), "should be able to scale up MAPI MachineSet")
			mapiframework.WaitForMachineSet(ctx, cl, machinesetName)
			capims, err := framework.GetMachineSet(cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "failed to get capiMachineSet %s", machinesetName)
			Expect(*capims.Spec.Replicas).To(Equal(int32(2)), "replicas should change to 2")

			By("Verify a new child MAPI Machine is running and unpaused")
			Eventually(func() *machinev1beta1.Machine {
				mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				if err != nil {
					return nil
				}
				return mapiMachine
			}, framework.WaitMedium, framework.RetryMedium).Should(SatisfyAll(
				WithTransform(func(m *machinev1beta1.Machine) string {
					if m.Status.Phase == nil || *m.Status.Phase == "" {
						return ""
					}
					return *m.Status.Phase
				}, Equal(string(machinev1beta1.PhaseRunning))),

				WithTransform(func(m *machinev1beta1.Machine) []machinev1beta1.Condition {
					return m.Status.Conditions
				}, ContainElement(SatisfyAll(
					HaveField("Type", Equal(PausedCondition)),
					HaveField("Status", Equal(corev1.ConditionFalse)),
				))),

				WithTransform(func(m *machinev1beta1.Machine) machinev1beta1.MachineAuthority {
					return m.Status.AuthoritativeAPI
				}, Equal(machinev1beta1.MachineAuthorityMachineAPI)),
			))

			By("Verify there is a non-authoritative, paused CAPI Machine mirror for the new MAPI Machine")
			Eventually(func(g Gomega) {
				capiMachine, err := framework.GetLatestMachineFromMachineSet(cl, capiMachineSet)
				g.Expect(err).ToNot(HaveOccurred(), "error getting CAPI machine")
				g.Expect(capiMachine.Status.Conditions).ToNot(BeEmpty(), "CAPI Machine should have conditions")

				var pausedConditionFound bool
				for _, cond := range capiMachine.Status.Conditions {
					if string(cond.Type) == string(PausedCondition) && cond.Status == corev1.ConditionTrue {
						pausedConditionFound = true
						break
					}
				}
				g.Expect(pausedConditionFound).To(BeTrue(), "Expected Paused=True condition in CAPI Machine")
			}, framework.WaitShort, framework.RetryShort)

			By("Scale down MAPI machineset to 1")
			Expect(mapiframework.ScaleMachineSet(mapiMachineSet.GetName(), 1)).To(Succeed(), "should be able to scale down MAPI MachineSet")
			mapiframework.WaitForMachineSet(ctx, cl, machinesetName)
			capims, err = framework.GetMachineSet(cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "failed to get capiMachineSet %s", machinesetName)
			Expect(*capims.Spec.Replicas).To(Equal(int32(1)), "replicas should change to 1")
		})

		// OCP-81820 - [CAPI][Migration] When MachineAPI is authoritative - update MAPI MachineSet spec the value can be passed to CAPI
		It("should update MAPI machineset's spec (such as DeletePolicy) succeed", func() {
			By("should not take affect when updating CAPI machineset's spec (such as DeletePolicy)")
			capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "failed to get capiMachineSet %s", machinesetName)

			capiMachineSet = capiMachineSet.DeepCopy()
			capiMachineSet.Spec.DeletePolicy = "Oldest"

			err = cl.Patch(ctx, capiMachineSet, client.MergeFrom(capiMachineSet))
			Expect(err).NotTo(HaveOccurred(), "failed to patch CAPI MachineSet deletePolicy")

			capiMachineSet, err = framework.GetMachineSet(cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "failed to get updated CAPI MachineSet")
			Expect(capiMachineSet.Spec.DeletePolicy).To(Equal("Random"), "CAPI MachineSet deletePolicy should remain 'Random'")

			mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet %s", machinesetName)

			Expect(mapiMachineSet.Spec.DeletePolicy).To(
				SatisfyAny(
					BeEmpty(),
					Equal("Random"),
				),
				"MAPI MachineSet deletePolicy should be empty or 'Random', but got: %s",
				mapiMachineSet.Spec.DeletePolicy,
			)

			By("should take affect when updating MAPI machineset's spec (such as DeletePolicy)")
			mapiMachineSet, err = mapiframework.GetMachineSet(ctx, cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet %s", machinesetName)

			mapiMachineSet.Spec.DeletePolicy = "Newest"
			err = cl.Update(ctx, mapiMachineSet)
			Expect(err).NotTo(HaveOccurred(), "failed to update MAPI MachineSet DeletePolicy")

			Eventually(func() string {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
				if err != nil {
					return ""
				}
				return string(mapiMachineSet.Spec.DeletePolicy)
			}, framework.WaitShort, framework.RetryShort).Should(Equal("Newest"), "MAPI MachineSet DeletePolicy did not sync")

			Eventually(func() string {
				capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
				if err != nil {
					return ""
				}
				return string(capiMachineSet.Spec.DeletePolicy)
			}, framework.WaitShort, framework.RetryShort).Should(Equal("Newest"), "CAPI MachineSet DeletePolicy did not sync")
		})

		//Todo
		//bug https://issues.redhat.com/browse/OCPBUGS-54705
		/*
			It("should create a new Machine template when updating MAPI machineset's template(such as InstanceType)", func() {
			})
		*/

		// OCP-81822 - [CAPI][Migration] When MachineAPI is authoritative - labels/annotations in MAPI MachineSet can be passed to CAPI
		It("should pass labels/annotations from MAPI to CAPI machineset", func() {
			By("check that pass labels/annotations from MAPI to CAPI machineset succeed")
			mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "Failed to get mapiMachineSet %s", machinesetName)

			patch := client.MergeFrom(mapiMachineSet.DeepCopy())
			if mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels == nil {
				mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels = make(map[string]string)
			}
			if mapiMachineSet.Spec.Template.Spec.ObjectMeta.Annotations == nil {
				mapiMachineSet.Spec.Template.Spec.ObjectMeta.Annotations = make(map[string]string)
			}
			mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels["mapi-label1"] = "mapi-label1"
			mapiMachineSet.Spec.Template.Spec.ObjectMeta.Annotations["mapi-ano1"] = "mapi-ano1"

			err = cl.Patch(ctx, mapiMachineSet, patch)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
				if err != nil {
					return false
				}
				_, hasLabel := mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels["mapi-label1"]
				_, hasAnno := mapiMachineSet.Spec.Template.Spec.ObjectMeta.Annotations["mapi-ano1"]
				return hasLabel && hasAnno
			}, framework.WaitShort, framework.RetryShort).Should(BeTrue(), "MAPI MachineSet should have new labels/annotations")

			Eventually(func() bool {
				capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
				if err != nil {
					return false
				}
				_, hasLabel := capiMachineSet.Spec.Template.ObjectMeta.Labels["mapi-label1"]
				_, hasAnno := capiMachineSet.Spec.Template.ObjectMeta.Annotations["mapi-ano1"]
				return hasLabel && hasAnno
			}, framework.WaitShort, framework.RetryShort).Should(BeTrue(), "CAPI MachineSet should have new labels/annotations")

			By("check that propagate labels/annotations from CAPI to MAPI machineset is restored")
			capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "failed to get capiMachineSet %s", machinesetName)

			patch = client.MergeFrom(capiMachineSet.DeepCopy())
			if capiMachineSet.Spec.Template.ObjectMeta.Labels == nil {
				capiMachineSet.Spec.Template.ObjectMeta.Labels = make(map[string]string)
			}
			if capiMachineSet.Spec.Template.ObjectMeta.Annotations == nil {
				capiMachineSet.Spec.Template.ObjectMeta.Annotations = make(map[string]string)
			}
			capiMachineSet.Spec.Template.ObjectMeta.Labels["capi-label1"] = "capi-label1"
			capiMachineSet.Spec.Template.ObjectMeta.Annotations["capi-ano1"] = "capi-ano1"

			err = cl.Patch(ctx, capiMachineSet, patch)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
				if err != nil {
					return false
				}
				_, hasLabel := capiMachineSet.Spec.Template.ObjectMeta.Labels["capi-label1"]
				_, hasAnno := capiMachineSet.Spec.Template.ObjectMeta.Annotations["capi-ano1"]
				return !hasLabel && !hasAnno
			}, framework.WaitShort, framework.RetryShort).Should(BeTrue(), "CAPI MachineSet should not have new labels/annotations")

			Eventually(func() bool {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
				if err != nil {
					return false
				}
				_, hasLabel := mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels["capi-label1"]
				_, hasAnno := mapiMachineSet.Spec.Template.Spec.ObjectMeta.Annotations["capi-ano1"]
				return !hasLabel && !hasAnno
			}, framework.WaitShort, framework.RetryShort).Should(BeTrue(), "MAPI MachineSet should not have CAPI labels/annotations")
		})

		// OCP-81949 - [CAPI][Migration] When MachineAPI is authoritative - delete a MAPI/CAPI Machine a new MAPI-authoritative Machine should be created
		It("should create a new MachineAPI authoritative Machine when deleting a machine", func() {
			By("check that a new MachineAPI authoritative Machine is created when deleting a MAPI machine")
			mapiMachine, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
			Expect(err).ToNot(HaveOccurred(), "Failed to get mapiMachineSet %s", machinesetName)

			Expect(mapiframework.DeleteMachines(ctx, cl, mapiMachine...)).To(Succeed(), "Should be able to delete test Machines")
			mapiframework.WaitForMachinesDeleted(cl, mapiMachine...)

			capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "Failed to get capiMachineSet %s", machinesetName)
			Expect(*capiMachineSet.Spec.Replicas).To(Equal(int32(1)), "capiMachineSet replicas should remain 1")

			mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "Failed to get mapiMachineSet %s", machinesetName)
			Expect(*mapiMachineSet.Spec.Replicas).To(Equal(int32(1)), "mapiMachineSet replicas should remain 1")
			mapiframework.WaitForMachineSet(ctx, cl, machinesetName)

			Eventually(func() string {
				mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				if err != nil {
					return ""
				}
				return string(mapiMachine.Status.AuthoritativeAPI)
			}, framework.WaitMedium, framework.RetryMedium).Should(Equal("MachineAPI"), "the new created Machine should be MachineAPI")

			By("check that a new MachineAPI authoritative Machine is created when deleting a CAPI machine")
			capiMachine, err := framework.GetMachinesFromMachineSet(cl, capiMachineSet)
			Expect(err).ToNot(HaveOccurred(), "Failed to get mapiMachineSet %s", machinesetName)

			Expect(framework.DeleteMachines(cl, capiMachine...)).To(Succeed(), "Should be able to delete test Machines")
			framework.WaitForMachinesDeleted(cl, capiMachine...)

			capiMachineSet, err = framework.GetMachineSet(cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "Failed to get capiMachineSet %s", machinesetName)
			Expect(*capiMachineSet.Spec.Replicas).To(Equal(int32(1)), "capiMachineSet replicas should remain 1")

			mapiMachineSet, err = mapiframework.GetMachineSet(ctx, cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "Failed to get mapiMachineSet %s", machinesetName)
			Expect(*mapiMachineSet.Spec.Replicas).To(Equal(int32(1)), "mapiMachineSet replicas should remain 1")
			mapiframework.WaitForMachineSet(ctx, cl, machinesetName)

			Eventually(func() string {
				mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				if err != nil {
					return ""
				}
				return string(mapiMachine.Status.AuthoritativeAPI)
			}, framework.WaitMedium, framework.RetryMedium).Should(Equal("MachineAPI"), "the new created Machine should be MachineAPI")
		})

		// OCP-81826 - [CAPI][Migration] When MachineAPI is authoritative - change MachineSet authoritativeAPI to ClusterAPI should work
		When("change AuthoritativeAPI from MachineAPI to ClusterAPI", func() {
			It("should change AuthoritativeAPI to ClusterAPI succeed", func() {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
				Expect(err).ToNot(HaveOccurred(), "failed to get MachineSet")

				mapiMachineSet.Spec.AuthoritativeAPI = "ClusterAPI"
				mapiMachineSet.Spec.Template.Spec.AuthoritativeAPI = "ClusterAPI"

				err = cl.Update(ctx, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to update MachineSet")
				Eventually(func() string {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
					if err != nil {
						return ""
					}
					return string(mapiMachineSet.Status.AuthoritativeAPI)
				}, framework.WaitMedium, framework.RetryMedium).Should(Equal("ClusterAPI"), "change AuthoritativeAPI to ClusterAPI failed")

				By("check that the synchronized condition on the MAPI MachineSet is updated to True")
				Eventually(func() []machinev1beta1.Condition {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
					if err != nil {
						return nil
					}
					return mapiMachineSet.Status.Conditions
				}, framework.WaitShort, framework.RetryShort).Should(
					ContainElement(
						SatisfyAll(
							HaveField("Type", Equal(SynchronizedCondition)),
							HaveField("Status", Equal(corev1.ConditionTrue)),
							HaveField("Reason", Equal("ResourceSynchronized")),
							HaveField("Message", Equal("Successfully synchronized CAPI MachineSet to MAPI")),
						),
					),
				)

				By("check that the Paused condition on the MAPI MachineSet is updated to True")
				Eventually(func() []machinev1beta1.Condition {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
					if err != nil {
						return nil
					}
					return mapiMachineSet.Status.Conditions
				}, framework.WaitShort, framework.RetryShort).Should(
					ContainElement(
						SatisfyAll(
							HaveField("Type", Equal(PausedCondition)),
							HaveField("Status", Equal(corev1.ConditionTrue)),
							HaveField("Reason", Equal("AuthoritativeAPINotMachineAPI")),
							HaveField("Message", Equal("The AuthoritativeAPI is set to ClusterAPI")),
						),
					),
				)

				By("check that the Paused condition on the CAPI MachineSet is updated to False")
				Eventually(func() []metav1.Condition {
					capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
					if err != nil {
						return nil
					}
					return capiMachineSet.Status.V1Beta2.Conditions
				}, framework.WaitMedium, framework.RetryMedium).Should(
					ContainElement(
						SatisfyAll(
							HaveField("Type", Equal(capiv1beta1.PausedV1Beta2Condition)),
							HaveField("Status", Equal(metav1.ConditionFalse)),
							HaveField("Reason", Equal("NotPaused")),
						),
					),
				)
			})

			It("should scale up CAPI MachineSet from 1 to 2 succeed", func() {
				Expect(framework.ScaleMachineSet(machinesetName, 2)).To(Succeed(), "should be able to scale up CAPI MachineSet")

				By("Verify a new child CAPI Machine is running and unpaused")
				Eventually(func() *capiv1beta1.Machine {
					capiMachine, err := framework.GetLatestMachineFromMachineSet(cl, capiMachineSet)
					if err != nil {
						return nil
					}
					return capiMachine
				}, framework.WaitLong, framework.RetryLong).Should(SatisfyAll(
					WithTransform(func(m *capiv1beta1.Machine) string {
						if m.Status.Phase == "" {
							return ""
						}
						return m.Status.Phase
					}, Equal(string(capiv1beta1.MachinePhaseRunning))),

					WithTransform(func(m *capiv1beta1.Machine) []metav1.Condition {
						return m.Status.V1Beta2.Conditions
					}, ContainElement(SatisfyAll(
						HaveField("Type", Equal(capiv1beta1.PausedV1Beta2Condition)),
						HaveField("Status", Equal(metav1.ConditionFalse)),
						HaveField("Reason", Equal("NotPaused")),
					))),
				))

				By("Verify there is a non-authoritative, paused MAPI Machine mirror for the new CAPI Machine")
				Eventually(func() *machinev1beta1.Machine {
					mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
					if err != nil {
						return nil
					}
					return mapiMachine
				}, framework.WaitShort, framework.RetryShort).Should(SatisfyAll(
					WithTransform(func(m *machinev1beta1.Machine) machinev1beta1.MachineAuthority {
						return m.Status.AuthoritativeAPI
					}, Equal(machinev1beta1.MachineAuthorityClusterAPI)),

					WithTransform(func(m *machinev1beta1.Machine) []machinev1beta1.Condition {
						return m.Status.Conditions
					}, ContainElement(SatisfyAll(
						HaveField("Type", Equal(PausedCondition)),
						HaveField("Status", Equal(corev1.ConditionTrue)),
						HaveField("Reason", Equal("AuthoritativeAPINotMachineAPI")),
						HaveField("Message", Equal("The AuthoritativeAPI is set to ClusterAPI")),
					))),
				))
			})
		})

		// OCP-81941 - [CAPI][Migration] When ClusterAPI is authoritative - change machineset authoritativeAPI to MachineAPI should work
		When("change AuthoritativeAPI from ClusterAPI back to MachineAPI", func() {
			It("should change AuthoritativeAPI back to MachineAPI succeed", func() {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
				Expect(err).ToNot(HaveOccurred(), "failed to get MachineSet")

				mapiMachineSet.Spec.AuthoritativeAPI = "MachineAPI"
				mapiMachineSet.Spec.Template.Spec.AuthoritativeAPI = "MachineAPI"

				err = cl.Update(ctx, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to update MachineSet")
				Eventually(func() string {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
					if err != nil {
						return ""
					}
					return string(mapiMachineSet.Status.AuthoritativeAPI)
				}, framework.WaitMedium, framework.RetryMedium).Should(Equal("MachineAPI"), "change AuthoritativeAPI back to MachineAPI failed")

				By("check that the synchronized condition on the MAPI MachineSet is updated to True")
				Eventually(func() []machinev1beta1.Condition {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
					if err != nil {
						return nil
					}
					return mapiMachineSet.Status.Conditions
				}, framework.WaitShort, framework.RetryShort).Should(
					ContainElement(
						SatisfyAll(
							HaveField("Type", Equal(SynchronizedCondition)),
							HaveField("Status", Equal(corev1.ConditionTrue)),
							HaveField("Reason", Equal("ResourceSynchronized")),
							HaveField("Message", Equal("Successfully synchronized MAPI MachineSet to CAPI")),
						),
					),
				)

				By("check that the Paused condition on the MAPI MachineSet is updated to False")
				Eventually(func() []machinev1beta1.Condition {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
					if err != nil {
						return nil
					}
					return mapiMachineSet.Status.Conditions
				}, framework.WaitShort, framework.RetryShort).Should(
					ContainElement(
						SatisfyAll(
							HaveField("Type", Equal(PausedCondition)),
							HaveField("Status", Equal(corev1.ConditionFalse)),
							HaveField("Reason", Equal("AuthoritativeAPIMachineAPI")),
							HaveField("Message", Equal("The AuthoritativeAPI is set to MachineAPI")),
						),
					),
				)

				By("check that the Paused condition on the CAPI MachineSet is updated to True")
				Eventually(func() []metav1.Condition {
					capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
					if err != nil {
						return nil
					}
					return capiMachineSet.Status.V1Beta2.Conditions
				}, framework.WaitMedium, framework.RetryMedium).Should(
					ContainElement(
						SatisfyAll(
							HaveField("Type", Equal(capiv1beta1.PausedV1Beta2Condition)),
							HaveField("Status", Equal(metav1.ConditionTrue)),
							HaveField("Reason", Equal("Paused")),
							HaveField("Message", Equal("MachineSet has the cluster.x-k8s.io/paused annotation")),
						),
					),
				)
			})

			It("should scale down MAPI MachineSet succeed", func() {
				Expect(mapiframework.ScaleMachineSet(machinesetName, 1)).To(Succeed(), "should be able to scale down MAPI MachineSet")

				By("Verify that the child machine and its mirror got deleted")
				capiMachine, err := framework.GetLatestMachineFromMachineSet(cl, capiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get the latest capiMachine %s", capiMachine)
				Eventually(func() bool {
					_, err := mapiframework.GetMachine(cl, capiMachine.Name)
					return apierrors.IsNotFound(err)
				}, framework.WaitMedium, framework.RetryMedium).Should(BeTrue(), "expected MAPI machine %s to be deleted", capiMachine.Name)

				Eventually(func() bool {
					_, err := framework.GetMachine(cl, capiMachine.Name)
					return apierrors.IsNotFound(err)
				}, framework.WaitMedium, framework.RetryMedium).Should(BeTrue(), "expected CAPI machine %s to be deleted", capiMachine.Name)
			})
		})

		// OCP-81823 - [CAPI][Migration] When MachineAPI is authoritative - delete mapi/capi MachineSet should work
		It("should delete mapi/capi MachineSet and InfraMachineTemplate when deleting MAPI machineset", func() {
			mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet")

			capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "Failed to get capiMachineSet %s", machinesetName)

			Expect(mapiframework.DeleteMachineSets(cl, mapiMachineSet)).To(Succeed(), "Should be able to delete test Machineset")
			framework.WaitForMachineSetsDeleted(cl, capiMachineSet)
			mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
			// bug https://issues.redhat.com/browse/OCPBUGS-57195
			/*
				Eventually(func() bool {
					awsMachineTemplate, err = framework.GetAWSMachineTemplate(cl, machinesetName)
					return apierrors.IsNotFound(err)
				}, framework.WaitMedium, framework.RetryMedium).Should(BeTrue(), "InfraMachineTemplate %s should be deleted", awsMachineTemplate)
			*/
		})

		// OCP-81823 - [CAPI][Migration] When MachineAPI is authoritative - delete mapi/capi MachineSet should work
		It("should delete mapi/capi MachineSet and InfraMachineTemplate when deleting CAPI machineset", func() {
			mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, machinesetName, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
			Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet creation should succeed")

			mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet")

			capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
			Expect(err).ToNot(HaveOccurred(), "Failed to get capiMachineSet %s", machinesetName)

			framework.DeleteMachineSets(cl, capiMachineSet)
			framework.WaitForMachineSetsDeleted(cl, capiMachineSet)
			mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
			// bug https://issues.redhat.com/browse/OCPBUGS-57195
			/*
				Eventually(func() bool {
					awsMachineTemplate, err = framework.GetAWSMachineTemplate(cl, machinesetName)
					return apierrors.IsNotFound(err)
				}, framework.WaitMedium, framework.RetryMedium).Should(BeTrue(), "InfraMachineTemplate %s should be deleted", awsMachineTemplate)
			*/
		})
	})

	Context("when EXISTING CAPI MachineSet with that name", func() {
		// https://issues.redhat.com/browse/OCPCLOUD-2641
		/*
			It("should be rejected by a VAP/webhook when creating same name MAPI MachineSet", func() {
				By("Creating a CAPI MachineSet")
				mapiDefaultMS, mapiDefaultProviderSpec := getDefaultAWSMAPIProviderSpec(cl)
				awsClient := createAWSClient(mapiDefaultProviderSpec.Placement.Region)
				awsMachineTemplate = newAWSMachineTemplate(mapiDefaultProviderSpec)
				if err := cl.Create(ctx, awsMachineTemplate); err != nil && !apierrors.IsAlreadyExists(err) {
					Expect(err).ToNot(HaveOccurred())
				}

				machineSet := framework.CreateMachineSet(cl, framework.NewMachineSetParams(
					machinesetName,
					clusterName,
					"",
					1,
					corev1.ObjectReference{
						Kind:       "AWSMachineTemplate",
						APIVersion: infraAPIVersion,
						Name:       awsMachineTemplateName,
					},
					"worker-user-data",
				))

				framework.WaitForMachineSet(cl, machineSet.Name)
				compareInstances(awsClient, mapiDefaultMS.Name, "aws-machineset")
				By("Creating a same name MAPI MachineSet")
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, machinesetName, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
				Expect(err).To(HaveOccurred(), "this should be rejected ")
			})
		*/
	})
})

// createMAPIMachineSetWithAuthoritativeAPI create a machineset with AuthoritativeAPI
func createMAPIMachineSetWithAuthoritativeAPI(ctx context.Context, cl client.Client, machinesetName string, machinesetAuthority machinev1beta1.MachineAuthority, machineAuthority machinev1beta1.MachineAuthority) (*machinev1beta1.MachineSet, error) {
	var err error
	machineSetParams := mapiframework.BuildMachineSetParams(ctx, cl, 1)
	machineSetParams.Name = machinesetName
	machineSetParams.MachinesetAuthoritativeAPI = machinesetAuthority
	machineSetParams.MachineAuthoritativeAPI = machineAuthority
	// now CAPI machineset doesn't support taint. card https://issues.redhat.com/browse/OCPCLOUD-2861
	machineSetParams.Taints = []corev1.Taint{}
	mapiMachineSet, err := mapiframework.CreateMachineSet(cl, machineSetParams)
	Expect(err).ToNot(HaveOccurred(), "MAPI machineSet creation should succeed")
	time.Sleep(framework.WaitShort)
	mapiframework.WaitForMachineSet(ctx, cl, machinesetName)
	return mapiMachineSet, nil
}
