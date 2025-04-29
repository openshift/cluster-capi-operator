package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	"k8s.io/klog"

	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	SynchronizedCondition machinev1beta1.ConditionType = "Synchronized"
	PausedCondition       machinev1beta1.ConditionType = "Paused"
	CAPIPausedCondition                                = capiv1beta1.PausedV1Beta2Condition
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Machineset Migration Tests", Ordered, func() {
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this only support on aws", platform))
		}

		if !framework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	var _ = Describe("Create MAPI MachineSet", Ordered, func() {
		var mapiMachineSet *machinev1beta1.MachineSet
		var machineSetNameMAPI = "ms-auth-mapi"
		var machineSetNameCAPI = "ms-auth-capi"
		//var capiMachineSetNameMAPI = "capi-machineset-mapi"
		//var capiMachineSetNameCAPI = "capi-machineset-capi"
		var err error

		AfterAll(func() {
			By("Cleaning up all test resources")
			cleanupErrors := CleanupTestResources(cl, ctx, machineSetNameMAPI, machineSetNameCAPI, "")

			if len(cleanupErrors) > 0 {
				klog.Errorf("Cleanup completed with %d errors:", len(cleanupErrors))
				for _, e := range cleanupErrors {
					klog.Error(e)
				}
			} else {
				klog.Info("All test resources cleaned up successfully")
			}
		})

		Context("with specAPI: MAPI and EXISTING CAPI MSet with that name", func() {
			// https://issues.redhat.com/browse/OCPCLOUD-2641
			/*
				It("should be rejected by a VAP/webhook when creating same name MAPI MachineSet", func() {
					By("Creating a CAPI MachineSet")
					_, mapiDefaultProviderSpec := getDefaultAWSMAPIProviderSpec(cl)
					createAWSClient(mapiDefaultProviderSpec.Placement.Region)
					awsMachineTemplate = newAWSMachineTemplate(mapiDefaultProviderSpec)
					if err := cl.Create(ctx, awsMachineTemplate); err != nil && !apierrors.IsAlreadyExists(err) {
						Expect(err).ToNot(HaveOccurred())
					}

					machineSet := framework.CreateMachineSet(cl, framework.NewMachineSetParams(
						capiMachineSetNameMAPI,
						clusterName,
						"",
						0,
						corev1.ObjectReference{
							Kind:       "AWSMachineTemplate",
							APIVersion: infraAPIVersion,
							Name:       capiMachineSetNameCAPI,
						},
						"worker-user-data",
					))

					framework.WaitForMachineSet(cl, machineSet.Name)

					By("Creating a same name MAPI MachineSet")
					mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, capiMachineSetNameMAPI, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
					Expect(err).To(HaveOccurred(), "this should be rejected ")
				})
			*/
		})

		Context("with specAPI: MAPI and when no existing CAPI MachineSet with that name", func() {
			BeforeAll(func() {
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, machineSetNameMAPI, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
				Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet creation should succeed")
				Expect(mapiMachineSet).NotTo(BeNil())
			})

			It("should MAPI MachineSet is authoritative and create the CAPI MachineSet mirror", func() {
				verifyMachineSetAuthoritative(ctx, cl, machineSetNameMAPI, machinev1beta1.MachineAuthorityMachineAPI)
				verifySynchronizedCondition(ctx, cl, machineSetNameMAPI, machinev1beta1.MachineAuthorityMachineAPI)
				verifyMAPIPausedCondition(ctx, cl, machineSetNameMAPI, machinev1beta1.MachineAuthorityMachineAPI)
				verifyMAPIHasCAPIMirror(cl, machineSetNameMAPI)
				verifyCAPIPausedCondition(cl, machineSetNameMAPI, machinev1beta1.MachineAuthorityMachineAPI)
			})
		})
		/*
			Context("With specAPI: CAPI and EXISTING CAPI MSet with that name", func() {
				// bug https://issues.redhat.com/browse/OCPBUGS-55337
				It("should MAPI Spec be updated to reflect existing CAPI mirror", func() {
					By("Creating a CAPI MachineSet")
					_, mapiDefaultProviderSpec := getDefaultAWSMAPIProviderSpec(cl)
					createAWSClient(mapiDefaultProviderSpec.Placement.Region)
					awsMachineTemplate = newAWSMachineTemplate(mapiDefaultProviderSpec)
					awsMachineTemplate.Spec.Template.Spec.InstanceType = "m5.large"

					if err := cl.Create(ctx, awsMachineTemplate); err != nil && !apierrors.IsAlreadyExists(err) {
						Expect(err).ToNot(HaveOccurred())
					}

					machineSet := framework.CreateMachineSet(cl, framework.NewMachineSetParams(
						capiMachineSetNameCAPI,
						clusterName,
						"",
						0,
						corev1.ObjectReference{
							Kind:       "AWSMachineTemplate",
							APIVersion: infraAPIVersion,
							Name:       capiMachineSetNameCAPI,
						},
						"worker-user-data",
					))

					framework.WaitForMachineSet(cl, machineSet.Name)

					//By("Creating a same name MAPI MachineSet")
					mapiMachineSetCAPI, err := createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, capiMachineSetNameCAPI, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
					Expect(err).ToNot(HaveOccurred(), "failed to create mapiMachineSet %s", mapiMachineSetCAPI)

					By("Verify the MAPI MachineSet is Paused")
					verifyMAPIPausedCondition(ctx, cl, capiMachineSetNameCAPI, machinev1beta1.MachineAuthorityClusterAPI)

					By("Verify the MAPI Spec be updated to reflect existing CAPI mirror")
					mapiMachineSetCAPI, err = mapiframework.GetMachineSet(ctx, cl, machineSetNameCAPI)
					Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet %s", machineSetNameCAPI)

					providerSpec := mapiMachineSetCAPI.Spec.Template.Spec.ProviderSpec
					Expect(providerSpec.Value).NotTo(BeNil())
					Expect(providerSpec.Value.Raw).NotTo(BeEmpty())

					var awsConfig machinev1beta1.AWSMachineProviderConfig
					err = json.Unmarshal(providerSpec.Value.Raw, &awsConfig)
					Expect(err).NotTo(HaveOccurred(), "Failed to unmarshal ProviderSpec.Value")

					Expect(awsConfig.InstanceType).To(
						SatisfyAny(
							BeEmpty(),
							Equal("m5.large"),
						),
						"Unexpected instanceType: %s",
						awsConfig.InstanceType,
					)
				})
			})
		*/

		Context("With specAPI: CAPI and when no existing CAPI MachineSet with that name", func() {
			BeforeAll(func() {
				machineSetNameCAPI, err := createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, machineSetNameCAPI, machinev1beta1.MachineAuthorityClusterAPI, machinev1beta1.MachineAuthorityClusterAPI)
				Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet creation should succeed %s", machineSetNameCAPI)
			})

			It("should CAPI MachineSet is authoritative and MAPI MachineSet gets paused", func() {
				verifyMachineSetAuthoritative(ctx, cl, machineSetNameCAPI, machinev1beta1.MachineAuthorityClusterAPI)
				verifySynchronizedCondition(ctx, cl, machineSetNameCAPI, machinev1beta1.MachineAuthorityClusterAPI)
				verifyMAPIPausedCondition(ctx, cl, machineSetNameCAPI, machinev1beta1.MachineAuthorityClusterAPI)
				verifyMAPIHasCAPIMirror(cl, machineSetNameCAPI)
				verifyCAPIPausedCondition(cl, machineSetNameCAPI, machinev1beta1.MachineAuthorityClusterAPI)
			})
		})
	})

	var _ = Describe("Scale MAPI MachineSet", Ordered, func() {
		var mapiMachineSet *machinev1beta1.MachineSet
		var capiMachineSet *capiv1beta1.MachineSet
		var machineSetNameMAPI = "ms-auth-mapi"
		var machineSetNameCAPI = "ms-auth-capi"
		var machineSetNameMAPICAPI = "ms-mapi-machine-capi"
		var err error

		AfterAll(func() {
			By("Cleaning up all test resources")
			cleanupErrors := CleanupTestResources(cl, ctx, machineSetNameMAPI, machineSetNameCAPI, machineSetNameMAPICAPI)

			if len(cleanupErrors) > 0 {
				klog.Errorf("Cleanup completed with %d errors:", len(cleanupErrors))
				for _, e := range cleanupErrors {
					klog.Error(e)
				}
			} else {
				klog.Info("All test resources cleaned up successfully")
			}
		})

		Context("MAPI authority scaling", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, machineSetNameMAPI, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
				Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet creation should succeed")

				By("Check for CAPI mirrors of MachineSet and Machine")
				verifyMAPIHasCAPIMirror(cl, machineSetNameMAPI)

				mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI machines from machineset")
				capiMachineSet, err = framework.GetMachineSet(cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to get CAPI machineset")
				capiMachines, err := framework.GetMachinesFromMachineSet(cl, capiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get CAPI machines from machineset")
				Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name))
			})

			It("should scale MAPI MachineSet to 2 replicas succeed", func() {
				By("Scale up MAPI MachineSet to 2 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMachineSet.GetName(), 2)).To(Succeed(), "should be able to scale up MAPI MachineSet")
				mapiframework.WaitForMachineSet(ctx, cl, machineSetNameMAPI)

				Eventually(func() int32 {
					capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameMAPI)
					Expect(err).ToNot(HaveOccurred(), "failed to get capiMachineSet %s", machineSetNameMAPI)
					return *capiMachineSet.Spec.Replicas
				}, framework.WaitShort, framework.RetryShort).Should(Equal(int32(2)), "CAPI MachineSet replicas should change to 2")

				By("Check new Machine is created and unpaused")
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

				By("Verify there is a mirrored paused CAPI Machine")
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
			})

			It("should switch specAPI to ClusterAPI succeed", func() {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to get MachineSet")
				patch := client.MergeFrom(mapiMachineSet.DeepCopy())
				mapiMachineSet.Spec.AuthoritativeAPI = "ClusterAPI"
				mapiMachineSet.Spec.Template.Spec.AuthoritativeAPI = "ClusterAPI"
				Expect(cl.Patch(ctx, mapiMachineSet, patch)).To(Succeed(), "failed to patch MachineSet")
				Eventually(func() string {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameMAPI)
					if err != nil {
						return ""
					}
					return string(mapiMachineSet.Status.AuthoritativeAPI)
				}, framework.WaitMedium, framework.RetryMedium).Should(Equal("ClusterAPI"), "change AuthoritativeAPI to ClusterAPI failed")

				verifySynchronizedCondition(ctx, cl, machineSetNameMAPI, machinev1beta1.MachineAuthorityClusterAPI)
				verifyMAPIPausedCondition(ctx, cl, machineSetNameMAPI, machinev1beta1.MachineAuthorityClusterAPI)
				verifyCAPIPausedCondition(cl, machineSetNameMAPI, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should scale up CAPI MachineSet to 3 succeed when switching specAPI to ClusterAPI", func() {
				Expect(framework.ScaleMachineSet(machineSetNameMAPI, 3)).To(Succeed(), "should be able to scale up CAPI MachineSet")

				By("Verify a new CAPI Machine is running and unpaused")
				Eventually(func() *capiv1beta1.Machine {
					capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameMAPI)
					Expect(err).ToNot(HaveOccurred(), "failed to get CAPI MachineSet")
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
						HaveField("Type", Equal(CAPIPausedCondition)),
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

			It("should scale down CAPI MachineSet to 1 succeed when switching specAPI to ClusterAPI", func() {
				Expect(framework.ScaleMachineSet(machineSetNameMAPI, 1)).To(Succeed(), "should be able to scale down CAPI MachineSet")
				Eventually(func() int32 {
					capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameMAPI)
					Expect(err).ToNot(HaveOccurred(), "failed to get capiMachineSet %s", machineSetNameMAPI)
					return *capiMachineSet.Spec.Replicas
				}, framework.WaitShort, framework.RetryShort).Should(Equal(int32(1)), "CAPI MachineSet replicas should change to 1")

				Eventually(func() int32 {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameMAPI)
					Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet %s", machineSetNameMAPI)
					return *mapiMachineSet.Spec.Replicas
				}, framework.WaitShort, framework.RetryShort).Should(Equal(int32(1)), "MAPI MachineSet replicas should change to 1")
			})

			It("should switch specAPI to MachineAPI succeed when switching specAPI to ClusterAPI", func() {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to get MachineSet")
				patch := client.MergeFrom(mapiMachineSet.DeepCopy())
				mapiMachineSet.Spec.AuthoritativeAPI = "MachineAPI"
				mapiMachineSet.Spec.Template.Spec.AuthoritativeAPI = "MachineAPI"
				Expect(cl.Patch(ctx, mapiMachineSet, patch)).To(Succeed(), "failed to patch MachineSet")
				Eventually(func() string {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameMAPI)
					if err != nil {
						return ""
					}
					return string(mapiMachineSet.Status.AuthoritativeAPI)
				}, framework.WaitMedium, framework.RetryMedium).Should(Equal("MachineAPI"), "change AuthoritativeAPI back to MachineAPI failed")

				verifySynchronizedCondition(ctx, cl, machineSetNameMAPI, machinev1beta1.MachineAuthorityMachineAPI)
				verifyMAPIPausedCondition(ctx, cl, machineSetNameMAPI, machinev1beta1.MachineAuthorityMachineAPI)
				verifyCAPIPausedCondition(cl, machineSetNameMAPI, machinev1beta1.MachineAuthorityMachineAPI)
			})

			It("should delete both MAPI and CAPI MachineSets/Machines and InfraMachineTemplate when deleting MAPI machineset", func() {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet")

				capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "Failed to get capiMachineSet %s", machineSetNameMAPI)

				Expect(mapiframework.DeleteMachineSets(cl, mapiMachineSet)).To(Succeed(), "Should be able to delete test Machineset")
				framework.WaitForMachineSetsDeleted(cl, capiMachineSet)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
				// bug https://issues.redhat.com/browse/OCPBUGS-57195
				/*
					Eventually(func() bool {
						awsMachineTemplate, err = framework.GetAWSMachineTemplateByPrefix(cl, machineSetNameMAPI)
						return apierrors.IsNotFound(err)
					}, framework.WaitMedium, framework.RetryMedium).Should(BeTrue(), "InfraMachineTemplate should be deleted")
				*/
			})
		})

		Context("CAPI authority scaling", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, machineSetNameCAPI, machinev1beta1.MachineAuthorityClusterAPI, machinev1beta1.MachineAuthorityClusterAPI)
				Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet creation should succeed")

				By("Check for CAPI mirrors of MachineSet and Machine")
				verifyMAPIHasCAPIMirror(cl, machineSetNameCAPI)

				mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI machines from machineset")
				capiMachineSet, err = framework.GetMachineSet(cl, machineSetNameCAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to get CAPI machineset")
				capiMachines, err := framework.GetMachinesFromMachineSet(cl, capiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get CAPI machines from machineset")
				Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name))
			})

			It("should scale CAPI MachineSet to 2 replicas succeed", func() {
				By("Scale up CAPI MachineSet to 2 replicas")
				framework.ScaleMachineSet(machineSetNameCAPI, 2)
				capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameCAPI)
				Expect(err).ToNot(HaveOccurred(), "Failed to get capiMachineSet %s", machineSetNameCAPI)

				framework.WaitForMachineSet(cl, machineSetNameCAPI)
	
				Eventually(func() int32 {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameCAPI)
					Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet %s", machineSetNameCAPI)
					return *mapiMachineSet.Spec.Replicas
				}, framework.WaitShort, framework.RetryShort).Should(Equal(int32(2)), "MAPI MachineSet replicas should change to 12")

				By("Check a new CAPI Machine is created and unpaused")
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
						HaveField("Type", Equal(CAPIPausedCondition)),
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

			It("should switch specAPI to MachineAPI succeed", func() {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameCAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to get MachineSet")
				patch := client.MergeFrom(mapiMachineSet.DeepCopy())
				mapiMachineSet.Spec.AuthoritativeAPI = "MachineAPI"
				mapiMachineSet.Spec.Template.Spec.AuthoritativeAPI = "MachineAPI"
				Expect(cl.Patch(ctx, mapiMachineSet, patch)).To(Succeed(), "failed to patch MachineSet")
				Eventually(func() string {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameCAPI)
					if err != nil {
						return ""
					}
					return string(mapiMachineSet.Status.AuthoritativeAPI)
				}, framework.WaitMedium, framework.RetryMedium).Should(Equal("MachineAPI"), "change AuthoritativeAPI to MachineAPI failed")

				verifySynchronizedCondition(ctx, cl, machineSetNameCAPI, machinev1beta1.MachineAuthorityMachineAPI)
				verifyMAPIPausedCondition(ctx, cl, machineSetNameCAPI, machinev1beta1.MachineAuthorityMachineAPI)
				verifyCAPIPausedCondition(cl, machineSetNameCAPI, machinev1beta1.MachineAuthorityMachineAPI)
			})

			It("should scale up MAPI MachineSet to 3 succeed when switching specAPI to MachineAPI", func() {
				Expect(mapiframework.ScaleMachineSet(machineSetNameCAPI, 3)).To(Succeed(), "should be able to scale up MAPI MachineSet")
				Eventually(func() int32 {
					capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameCAPI)
					Expect(err).ToNot(HaveOccurred(), "failed to get capiMachineSet %s", machineSetNameCAPI)
					return *capiMachineSet.Spec.Replicas
				}, framework.WaitShort, framework.RetryShort).Should(Equal(int32(3)), "CAPI MachineSet replicas should change to 3")

				By("Check new MAPI Machine is created and unpaused")
				Eventually(func() *machinev1beta1.Machine {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameCAPI)
					Expect(err).ToNot(HaveOccurred(), "failed to get MAPI MachineSet")
					mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
					if err != nil {
						return nil
					}
					return mapiMachine
				}, framework.WaitLong, framework.RetryLong).Should(SatisfyAll(
					WithTransform(func(m *machinev1beta1.Machine) []machinev1beta1.Condition {
						return m.Status.Conditions
					}, ContainElement(SatisfyAll(
						HaveField("Type", Equal(PausedCondition)),
						HaveField("Status", Equal(corev1.ConditionFalse)),
					))),

					WithTransform(func(m *machinev1beta1.Machine) machinev1beta1.MachineAuthority {
						return m.Status.AuthoritativeAPI
					}, Equal(machinev1beta1.MachineAuthorityMachineAPI)),
					WithTransform(func(m *machinev1beta1.Machine) string {
						if m.Status.Phase == nil || *m.Status.Phase == "" {
							return ""
						}
						return *m.Status.Phase
					}, Equal(string(machinev1beta1.PhaseRunning))),
				))

				By("Verify there is a mirrored paused CAPI Machine")
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
			})

			It("should scale down MAPI MachineSet to 1 succeed when switching specAPI to MachineAPI", func() {
				Expect(mapiframework.ScaleMachineSet(machineSetNameCAPI, 1)).To(Succeed(), "should be able to scale down MAPI MachineSet")
				Eventually(func() int32 {
					capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameCAPI)
					Expect(err).ToNot(HaveOccurred(), "failed to get capiMachineSet %s", machineSetNameCAPI)
					return *capiMachineSet.Spec.Replicas
				}, framework.WaitShort, framework.RetryShort).Should(Equal(int32(1)), "CAPI MachineSet replicas should change to 1")
			})

			It("should switch specAPI to ClusterAPI succeed when switching specAPI to MachineAPI", func() {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameCAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to get MachineSet")
				patch := client.MergeFrom(mapiMachineSet.DeepCopy())
				mapiMachineSet.Spec.AuthoritativeAPI = "ClusterAPI"
				mapiMachineSet.Spec.Template.Spec.AuthoritativeAPI = "ClusterAPI"
				Expect(cl.Patch(ctx, mapiMachineSet, patch)).To(Succeed(), "failed to patch MachineSet")
				Eventually(func() string {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameCAPI)
					if err != nil {
						return ""
					}
					return string(mapiMachineSet.Status.AuthoritativeAPI)
				}, framework.WaitMedium, framework.RetryMedium).Should(Equal("ClusterAPI"), "change AuthoritativeAPI to ClusterAPI failed")

				Eventually(func() string {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameCAPI)
					if err != nil {
						return ""
					}
					return string(mapiMachineSet.Status.AuthoritativeAPI)
				}, framework.WaitMedium, framework.RetryMedium).Should(Equal("ClusterAPI"), "change AuthoritativeAPI back to ClusterAPI failed")

				verifySynchronizedCondition(ctx, cl, machineSetNameCAPI, machinev1beta1.MachineAuthorityClusterAPI)
				verifyMAPIPausedCondition(ctx, cl, machineSetNameCAPI, machinev1beta1.MachineAuthorityClusterAPI)
				verifyCAPIPausedCondition(cl, machineSetNameCAPI, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should delete both MAPI and CAPI MachineSets/Machines and InfraMachineTemplate when deleting CAPI machineset", func() {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameCAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet")

				capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameCAPI)
				Expect(err).ToNot(HaveOccurred(), "Failed to get capiMachineSet %s", machineSetNameCAPI)

				framework.DeleteMachineSets(cl, capiMachineSet)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
				framework.WaitForMachineSetsDeleted(cl, capiMachineSet)

				// bug https://issues.redhat.com/browse/OCPBUGS-57195
				/*
					Eventually(func() bool {
						awsMachineTemplate, err = framework.GetAWSMachineTemplateByPrefix(cl, machineSetNameMAPI)
						return apierrors.IsNotFound(err)
					}, framework.WaitMedium, framework.RetryMedium).Should(BeTrue(), "InfraMachineTemplate should be deleted")
				*/
			})
		})

		Context("MAPI authority, scale as CAPI", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, machineSetNameMAPICAPI, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityClusterAPI)
				Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet creation should succeed")
				verifyMAPIHasCAPIMirror(cl, machineSetNameMAPICAPI)
			})

			It("should create CAPI Machine when scaling to 1 replicas", func() {
				By("Scale up MAPI MachineSet to 1 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMachineSet.GetName(), 1)).To(Succeed(), "should be able to scale up MAPI MachineSet")
				framework.WaitForMachineSet(cl, machineSetNameMAPICAPI)
				Eventually(func() int32 {
					capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameMAPICAPI)
					Expect(err).ToNot(HaveOccurred(), "failed to get capiMachineSet %s", machineSetNameMAPICAPI)
					return *capiMachineSet.Spec.Replicas
				}, framework.WaitShort, framework.RetryShort).Should(Equal(int32(1)), "CAPI MachineSet replicas should change to 1")

				By("Verify MAPI Machine is created and specAPI is ClusterAPI")
				Eventually(func() *machinev1beta1.Machine {
					mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
					if err != nil {
						return nil
					}
					return mapiMachine
				}, framework.WaitMedium, framework.RetryMedium).Should(SatisfyAll(
					WithTransform(func(m *machinev1beta1.Machine) []machinev1beta1.Condition {
						return m.Status.Conditions
					}, ContainElement(SatisfyAll(
						HaveField("Type", Equal(PausedCondition)),
						HaveField("Status", Equal(corev1.ConditionTrue)),
					))),

					WithTransform(func(m *machinev1beta1.Machine) machinev1beta1.MachineAuthority {
						return m.Status.AuthoritativeAPI
					}, Equal(machinev1beta1.MachineAuthorityClusterAPI)),
				))

				By("Verify CAPI Machine is created and unpaused and provisions a running Machine")
				capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameMAPICAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to get capiMachineSet %s", machineSetNameMAPICAPI)
				Eventually(func() *capiv1beta1.Machine {
					capiMachine, err := framework.GetLatestMachineFromMachineSet(cl, capiMachineSet)
					if err != nil {
						return nil
					}
					return capiMachine
				}, framework.WaitMedium, framework.RetryMedium).Should(SatisfyAll(
					WithTransform(func(m *capiv1beta1.Machine) string {
						if m.Status.Phase == "" {
							return ""
						}
						return m.Status.Phase
					}, Equal(string(capiv1beta1.MachinePhaseRunning))),

					WithTransform(func(m *capiv1beta1.Machine) []metav1.Condition {
						return m.Status.V1Beta2.Conditions
					}, ContainElement(SatisfyAll(
						HaveField("Type", Equal(CAPIPausedCondition)),
						HaveField("Status", Equal(metav1.ConditionFalse)),
						HaveField("Reason", Equal("NotPaused")),
					))),
				))
			})

			It("should delete both MAPI and CAPI MachineSets/Machines and InfraMachineTemplate when deleting MAPI machineset", func() {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameMAPICAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet")

				capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameMAPICAPI)
				Expect(err).ToNot(HaveOccurred(), "Failed to get capiMachineSet %s", machineSetNameMAPICAPI)

				Expect(mapiframework.DeleteMachineSets(cl, mapiMachineSet)).To(Succeed(), "Should be able to delete test Machineset")
				framework.WaitForMachineSetsDeleted(cl, capiMachineSet)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
				// bug https://issues.redhat.com/browse/OCPBUGS-57195
				/*
					Eventually(func() bool {
						awsMachineTemplate, err = framework.GetAWSMachineTemplateByPrefix(cl, machineSetNameMAPI)
						return apierrors.IsNotFound(err)
					}, framework.WaitMedium, framework.RetryMedium).Should(BeTrue(), "InfraMachineTemplate should be deleted")
				*/
			})
		})
	})

	var _ = Describe("Delete MachineSet", Ordered, func() {
		var mapiMachineSet *machinev1beta1.MachineSet
		var capiMachineSet *capiv1beta1.MachineSet
		var machineSetNameMAPI = "ms-auth-mapi"
		var machineSetNameCAPI = "ms-auth-capi"
		var err error

		AfterAll(func() {
			By("Cleaning up all test resources")
			cleanupErrors := CleanupTestResources(cl, ctx, machineSetNameMAPI, machineSetNameCAPI, "")

			if len(cleanupErrors) > 0 {
				klog.Errorf("Cleanup completed with %d errors:", len(cleanupErrors))
				for _, e := range cleanupErrors {
					klog.Error(e)
				}
			} else {
				klog.Info("All test resources cleaned up successfully")
			}
		})

		Context("Removing non-authoritative MAPI MachineSet", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, machineSetNameMAPI, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
				Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet creation should succeed")

				By("Check for CAPI mirrors of MachineSet and Machine")
				verifyMAPIHasCAPIMirror(cl, machineSetNameMAPI)

				mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI machines from machineset")
				capiMachineSet, err = framework.GetMachineSet(cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to get CAPI machineset")
				capiMachines, err := framework.GetMachinesFromMachineSet(cl, capiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get CAPI machines from machineset")
				Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name))
			})

			It("shouldn't delete CAPI Machineset when deleting MAPI Machineset when specAPI is ClusterAPI", func() {
				By("Switch specAPI to ClusterAPI")
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to get MachineSet")
				patch := client.MergeFrom(mapiMachineSet.DeepCopy())
				mapiMachineSet.Spec.AuthoritativeAPI = "ClusterAPI"
				mapiMachineSet.Spec.Template.Spec.AuthoritativeAPI = "ClusterAPI"
				Expect(cl.Patch(ctx, mapiMachineSet, patch)).To(Succeed(), "failed to patch MachineSet")
				Eventually(func() string {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameMAPI)
					if err != nil {
						return ""
					}
					return string(mapiMachineSet.Status.AuthoritativeAPI)
				}, framework.WaitMedium, framework.RetryMedium).Should(Equal("ClusterAPI"), "change AuthoritativeAPI back to ClusterAPI failed")

				By("Scale up CAPI MachineSet to 2 replicas")
				Expect(framework.ScaleMachineSet(capiMachineSet.GetName(), 2)).To(Succeed(), "should be able to scale up CAPI MachineSet")
				Eventually(func() int32 {
					mapiMachineSet, err = mapiframework.GetMachineSet(ctx, cl, machineSetNameMAPI)
					Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet %s", machineSetNameMAPI)
					return *mapiMachineSet.Spec.Replicas
				}, framework.WaitShort, framework.RetryShort).Should(Equal(int32(2)), "replicas should change to 2")

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

				By("Delete MAPI MachineSet")
				mapiMachineSet, err = mapiframework.GetMachineSet(ctx, cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet")
				mapiframework.DeleteMachineSets(cl, mapiMachineSet)

				By("Check CAPI MachineSet not removed")
				capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameMAPI)
				Expect(err).NotTo(HaveOccurred(), "CAPI MachineSet should not be deleted")
				Expect(capiMachineSet).NotTo(BeNil())

				By("Check both Machines and Mirrors remain")
				capiMachines, err := framework.GetMachinesFromMachineSet(cl, capiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "Failed to get capiMachineSet %s", machineSetNameMAPI)
				Expect(capiMachines).NotTo(BeEmpty(), "Machines should remain")

				mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "Failed to get mapiMachineSet %s", machineSetNameMAPI)
				Expect(mapiMachines).NotTo(BeEmpty(), "Machines should remain")

				// bug https://issues.redhat.com/browse/OCPBUGS-56897
				/*
					By("Verifying no owner references on MAPI Machines")
					for _, machine := range mapiMachines {
						Expect(machine.GetOwnerReferences()).To(BeEmpty(),
							"MAPI Machine %s should have no owner references", machine.Name)
					}
				*/
			})
		})
	})

	var _ = Describe("Update MachineSet", Ordered, func() {
		var mapiMachineSet *machinev1beta1.MachineSet
		var awsMachineTemplate *capav1.AWSMachineTemplate
		var machineSetNameMAPI = "ms-auth-mapi"
		var machineSetNameCAPI = "ms-auth-capi"
		var err error

		AfterAll(func() {
			By("Cleaning up all test resources")
			cleanupErrors := CleanupTestResources(cl, ctx, machineSetNameMAPI, machineSetNameCAPI, "")

			if len(cleanupErrors) > 0 {
				klog.Errorf("Cleanup completed with %d errors:", len(cleanupErrors))
				for _, e := range cleanupErrors {
					klog.Error(e)
				}
			} else {
				klog.Info("All test resources cleaned up successfully")
			}
		})

		Context("Create MAPI MachineSet with specAPI MachineAPI and replicas 0", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, machineSetNameMAPI, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
				Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet creation should succeed")
				Expect(mapiMachineSet).NotTo(BeNil())
				verifyMAPIHasCAPIMirror(cl, machineSetNameMAPI)
			})

			It("should be rejected when scaling CAPI mirror", func() {
				By("Scaling up CAPI MachineSet to 1")
				framework.ScaleMachineSet(machineSetNameMAPI, 1)
				capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "Failed to get capiMachineSet %s", capiMachineSet)

				Eventually(func() int32 {
					capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameMAPI)
					Expect(err).ToNot(HaveOccurred(), "Failed to get capiMachineSet %s", machineSetNameMAPI)
					return *capiMachineSet.Spec.Replicas
				}, framework.WaitShort, framework.RetryShort).Should(Equal(int32(0)), "replicas should eventually revert to 0")
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "Failed to get mapiMachineSet %s", machineSetNameMAPI)
				Expect(*mapiMachineSet.Spec.Replicas).To(Equal(int32(0)), "replicas should remain 0")
			})

			It("should be rejected when updating CAPI mirror spec", func() {
				By("Updating CAPI mirror spec (such as DeletePolicy)")
				capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to get capiMachineSet %s", machineSetNameMAPI)

				capiMachineSet = capiMachineSet.DeepCopy()
				capiMachineSet.Spec.DeletePolicy = "Oldest"

				err = cl.Patch(ctx, capiMachineSet, client.MergeFrom(capiMachineSet))
				Expect(err).NotTo(HaveOccurred(), "failed to patch CAPI MachineSet deletePolicy")

				capiMachineSet, err = framework.GetMachineSet(cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to get updated CAPI MachineSet")
				Expect(capiMachineSet.Spec.DeletePolicy).To(Equal("Random"), "CAPI MachineSet deletePolicy should remain 'Random'")

				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet %s", machineSetNameMAPI)

				Expect(mapiMachineSet.Spec.DeletePolicy).To(
					SatisfyAny(
						BeEmpty(),
						Equal("Random"),
					),
					"MAPI MachineSet deletePolicy should be empty or 'Random', but got: %s",
					mapiMachineSet.Spec.DeletePolicy,
				)
			})

			It("should create a new InfraTemplate when update MAPI providerSpec", func() {
				By("Updating MAPI MachineSet providerSpec InstanceType to m5.large")
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "Failed to get mapiMachineSet %s", mapiMachineSet)

				capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "Failed to get mapiMachineSet %s", capiMachineSet)

				originalAWSMachineTemplateName := capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name
				originalAWSMachineTemplate, err := framework.GetAWSMachineTemplateByPrefix(cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "Failed to get original awsMachineTemplate  %s", originalAWSMachineTemplate)

				patch := client.MergeFrom(mapiMachineSet.DeepCopy())
				var awsProviderSpec machinev1beta1.AWSMachineProviderConfig
				Expect(json.Unmarshal(mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, &awsProviderSpec)).To(Succeed())
				awsProviderSpec.InstanceType = "m5.xlarge"
				updatedSpec, err := json.Marshal(awsProviderSpec)
				Expect(err).NotTo(HaveOccurred())
				mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw = updatedSpec
				Expect(cl.Patch(ctx, mapiMachineSet, patch)).To(Succeed())

				By("Waiting for new InfrastructureTemplate to be created")
				var newInfraTemplateName string
				Eventually(func() bool {
					capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameMAPI)
					if err != nil {
						return false
					}
					newInfraTemplateName = capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name
					return newInfraTemplateName != originalAWSMachineTemplateName
				}, framework.WaitMedium, framework.RetryMedium).Should(BeTrue(), "New InfrastructureTemplate should be created")

				By("Verifying new InfrastructureTemplate has the updated InstanceType")
				newAWSMachineTemplate, err := framework.GetAWSMachineTemplateByPrefix(cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "Failed to get new awsMachineTemplate  %s", newAWSMachineTemplate)
				Expect(newAWSMachineTemplate.Spec.Template.Spec.InstanceType).To(Equal("m5.xlarge"))

				By("Verifying old InfrastructureTemplate is deleted")
				Eventually(func() bool {
					awsMachineTemplate, err = framework.GetAWSMachineTemplateByName(cl, originalAWSMachineTemplateName)
					return apierrors.IsNotFound(err)
				}, framework.WaitMedium, framework.RetryMedium).Should(BeTrue(), "Old InfrastructureTemplate should be deleted")
				Expect(awsMachineTemplate).To(BeNil())
			})
		})

		Context("Switch MAPI MachineSet with specAPI ClusterAPI", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to get MachineSet")
				patch := client.MergeFrom(mapiMachineSet.DeepCopy())
				mapiMachineSet.Spec.AuthoritativeAPI = "ClusterAPI"
				mapiMachineSet.Spec.Template.Spec.AuthoritativeAPI = "ClusterAPI"
				Expect(cl.Patch(ctx, mapiMachineSet, patch)).To(Succeed(), "failed to patch MachineSet")
				Eventually(func() string {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameMAPI)
					if err != nil {
						return ""
					}
					return string(mapiMachineSet.Status.AuthoritativeAPI)
				}, framework.WaitMedium, framework.RetryMedium).Should(Equal("ClusterAPI"), "change AuthoritativeAPI to ClusterAPI failed")

				verifySynchronizedCondition(ctx, cl, machineSetNameMAPI, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should be rejected when scaling MAPI MachineSet", func() {
				By("Scaling up MAPI MachineSet to 1")
				mapiframework.ScaleMachineSet(machineSetNameMAPI, 1)

				Eventually(func() int32 {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetNameMAPI)
					Expect(err).ToNot(HaveOccurred(), "Failed to get mapiMachineSet %s", mapiMachineSet)
					return *mapiMachineSet.Spec.Replicas
				}, framework.WaitShort, framework.RetryShort).Should(Equal(int32(0)), "replicas should eventually revert to 0")
				capiMachineSet, err := framework.GetMachineSet(cl, machineSetNameMAPI)
				Expect(err).ToNot(HaveOccurred(), "Failed to get capiMachineSet %s", machineSetNameMAPI)
				Expect(*capiMachineSet.Spec.Replicas).To(Equal(int32(0)), "replicas should remain 0")
			})
		})
	})
})

// createMAPIMachineSetWithAuthoritativeAPI create a machineset with AuthoritativeAPI with replicas
func createMAPIMachineSetWithAuthoritativeAPI(ctx context.Context, cl client.Client, replicas int, machineSetName string, machinesetAuthority machinev1beta1.MachineAuthority, machineAuthority machinev1beta1.MachineAuthority) (*machinev1beta1.MachineSet, error) {
	By(fmt.Sprintf("Create a MAPI MachineSet with specAPI %s and templateAPI %s and replicas %d", machinesetAuthority, machineAuthority, replicas))
	var err error
	machineSetParams := mapiframework.BuildMachineSetParams(ctx, cl, replicas)
	machineSetParams.Name = machineSetName
	machineSetParams.MachinesetAuthoritativeAPI = machinesetAuthority
	machineSetParams.MachineAuthoritativeAPI = machineAuthority
	// now CAPI machineset doesn't support taint. card https://issues.redhat.com/browse/OCPCLOUD-2861
	machineSetParams.Taints = []corev1.Taint{}
	mapiMachineSet, err := mapiframework.CreateMachineSet(cl, machineSetParams)
	Expect(err).ToNot(HaveOccurred(), "MAPI machineSet creation should succeed")
	// sleep for 30s to make sure mirror machineset be created
	time.Sleep(30 * time.Second)
	if machineAuthority == machinev1beta1.MachineAuthorityMachineAPI {
		mapiframework.WaitForMachineSet(ctx, cl, machineSetName)
	}
	if machineAuthority == machinev1beta1.MachineAuthorityClusterAPI {
		framework.WaitForMachineSet(cl, machineSetName)
	}
	return mapiMachineSet, nil
}

func verifyMachineSetAuthoritative(ctx context.Context, cl client.Client, machineSetName string, auth machinev1beta1.MachineAuthority) {
	By(fmt.Sprintf("Verify the MachineSet authoritative is %s", auth))
	Eventually(func() machinev1beta1.MachineAuthority {
		mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetName)
		if err != nil {
			return ""
		}
		return mapiMachineSet.Status.AuthoritativeAPI
	}, framework.WaitMedium, framework.RetryMedium).Should(
		Equal(auth),
		"MAPI MachineSet status.AuthoritativeAPI should be %s", auth)
}

func verifySynchronizedCondition(ctx context.Context, cl client.Client, machineSetName string, auth machinev1beta1.MachineAuthority) {
	By("Verify the MAPI MachineSet synchronized condition is True")
	var expectedMessage string
	switch auth {
	case machinev1beta1.MachineAuthorityMachineAPI:
		expectedMessage = "Successfully synchronized MAPI MachineSet to CAPI"
	case machinev1beta1.MachineAuthorityClusterAPI:
		expectedMessage = "Successfully synchronized CAPI MachineSet to MAPI"
	default:
		Fail(fmt.Sprintf("unknown authoritative API type: %v", auth))
	}

	Eventually(func() []machinev1beta1.Condition {
		mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetName)
		if err != nil {
			return nil
		}
		return mapiMachineSet.Status.Conditions
	}, framework.WaitMedium, framework.RetryMedium).Should(
		ContainElement(
			SatisfyAll(
				HaveField("Type", Equal(SynchronizedCondition)),
				HaveField("Status", Equal(corev1.ConditionTrue)),
				HaveField("Reason", Equal("ResourceSynchronized")),
				HaveField("Message", Equal(expectedMessage)),
			),
		),
		fmt.Sprintf("Expected Synchronized condition for %s not found or incorrect", auth))
}

func verifyMAPIPausedCondition(ctx context.Context, cl client.Client, machineSetName string, auth machinev1beta1.MachineAuthority) {
	var conditionMatcher types.GomegaMatcher

	switch auth {
	case machinev1beta1.MachineAuthorityMachineAPI:
		By("Verify the MAPI MachineSet is Unpaused")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(PausedCondition)),
			HaveField("Status", Equal(corev1.ConditionFalse)),
			HaveField("Reason", Equal("AuthoritativeAPIMachineAPI")),
			HaveField("Message", Equal("The AuthoritativeAPI is set to MachineAPI")),
		)
	case machinev1beta1.MachineAuthorityClusterAPI:
		By("Verify the MAPI MachineSet is Paused")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(PausedCondition)),
			HaveField("Status", Equal(corev1.ConditionTrue)),
			HaveField("Reason", Equal("AuthoritativeAPINotMachineAPI")),
			HaveField("Message", Equal("The AuthoritativeAPI is set to ClusterAPI")),
		)
	default:
		Fail(fmt.Sprintf("unknown authoritative API type: %v", auth))
	}

	Eventually(func() []machinev1beta1.Condition {
		mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetName)
		if err != nil {
			return nil
		}
		return mapiMachineSet.Status.Conditions
	}, framework.WaitMedium, framework.RetryMedium).Should(
		ContainElement(conditionMatcher),
		fmt.Sprintf("Expected paused condition for %s not found", auth),
	)
}

func verifyMAPIHasCAPIMirror(cl client.Client, machineSetNameMAPI string) {
	By("Check MAPI MachineSet has a CAPI MachineSet mirror")
	var err error
	var capiMachineSet *capiv1beta1.MachineSet
	var awsMachineTemplate *capav1.AWSMachineTemplate

	Eventually(func() error {
		capiMachineSet, err = framework.GetMachineSet(cl, machineSetNameMAPI)
		return err
	}, framework.WaitMedium, framework.RetryMedium).Should(Succeed(), "CAPI MachineSet should exist")
	Expect(capiMachineSet).NotTo(BeNil())

	Eventually(func() error {
		awsMachineTemplate, err = framework.GetAWSMachineTemplateByPrefix(cl, machineSetNameMAPI)
		if err != nil {
			return err
		}
		if awsMachineTemplate == nil {
			return fmt.Errorf("AWSMachineTemplate is nil")
		}
		if !strings.Contains(awsMachineTemplate.Name, machineSetNameMAPI) {
			return fmt.Errorf("AWSMachineTemplate name %q does not contain %q", awsMachineTemplate.Name, machineSetNameMAPI)
		}
		return nil
	}, framework.WaitMedium, framework.RetryMedium).Should(Succeed(), "AWSMachineTemplate should exist and match expected name")
}

func verifyCAPIPausedCondition(cl client.Client, machineSetName string, auth machinev1beta1.MachineAuthority) {
	var conditionMatcher types.GomegaMatcher

	switch auth {
	case machinev1beta1.MachineAuthorityClusterAPI:
		By("Verify the CAPI MachineSet is Unpaused")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(CAPIPausedCondition)),
			HaveField("Status", Equal(metav1.ConditionFalse)),
			HaveField("Reason", Equal("NotPaused")),
		)
	case machinev1beta1.MachineAuthorityMachineAPI:
		By("Verify the CAPI MachineSet is Paused")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(CAPIPausedCondition)),
			HaveField("Status", Equal(metav1.ConditionTrue)),
			HaveField("Reason", Equal("Paused")),
		)
	default:
		Fail(fmt.Sprintf("unknown authoritative API type: %v", auth))
	}

	Eventually(func() []metav1.Condition {
		capiMachineSet, err := framework.GetMachineSet(cl, machineSetName)
		if err != nil {
			return nil
		}
		return capiMachineSet.Status.V1Beta2.Conditions
	}, framework.WaitMedium, framework.RetryMedium).Should(
		ContainElement(conditionMatcher),
		fmt.Sprintf("Expected paused condition for %s not found", auth),
	)
}

func CleanupTestResources(
	cl client.Client,
	ctx context.Context,
	machineSetNameMAPI string,
	machineSetNameCAPI string,
	machineSetNameMAPICAPI string,
) []error {
	var cleanupErrors []error
	var capiMachineSetMAPI *capiv1beta1.MachineSet
	var capiMachineSetCAPI *capiv1beta1.MachineSet

	capiMachineSetMAPI, err := framework.GetMachineSet(cl, machineSetNameMAPI)
	if err != nil && !apierrors.IsNotFound(err) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("failed to get MAPI CAPI MachineSet: %w", err))
	}

	if capiMachineSetMAPI != nil {
		By(fmt.Sprintf("Deleting MAPI MachineSet %s", machineSetNameMAPI))
		framework.DeleteMachineSets(cl, capiMachineSetMAPI)
		framework.WaitForMachineSetsDeleted(cl, capiMachineSetMAPI)
	}

	capiMachineSetCAPI, err = framework.GetMachineSet(cl, machineSetNameCAPI)
	if err != nil && !apierrors.IsNotFound(err) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("failed to get CAPI MachineSet: %w", err))
	}

	if capiMachineSetCAPI != nil {
		By(fmt.Sprintf("Deleting CAPI MachineSet %s", machineSetNameCAPI))
		framework.DeleteMachineSets(cl, capiMachineSetCAPI)
		framework.WaitForMachineSetsDeleted(cl, capiMachineSetCAPI)
	}

	awsMachineTemplateMAPICAPI, err := framework.GetAWSMachineTemplateByPrefix(cl, machineSetNameMAPICAPI)
	if err != nil && !strings.Contains(err.Error(), "no AWSMachineTemplate found") {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("failed to get mapi-capi AWSMachineTemplate: %w", err))
	}

	if awsMachineTemplateMAPICAPI != nil {
		By(fmt.Sprintf("Deleting AWSMachineTemplate with prefix %s", machineSetNameMAPICAPI))
		if err := framework.DeleteAWSMachineTemplateByPrefix(cl, machineSetNameMAPICAPI); err != nil {
			if !strings.Contains(err.Error(), fmt.Sprintf("no templates found with prefix %q", machineSetNameMAPICAPI)) {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("failed to delete CAPI AWSMachineTemplate: %w", err))
			}
		}
	}

	awsMachineTemplateMAPI, err := framework.GetAWSMachineTemplateByPrefix(cl, machineSetNameMAPI)
	if err != nil && !strings.Contains(err.Error(), "no AWSMachineTemplate found") {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("failed to get MAPI AWSMachineTemplate: %w", err))
	}

	if awsMachineTemplateMAPI != nil {
		By(fmt.Sprintf("Deleting AWSMachineTemplate with prefix %s", machineSetNameMAPI))
		if err := framework.DeleteAWSMachineTemplateByPrefix(cl, machineSetNameMAPI); err != nil {
			if !strings.Contains(err.Error(), fmt.Sprintf("no templates found with prefix %q", machineSetNameMAPI)) {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("failed to delete MAPI AWSMachineTemplate: %w", err))
			}
		}
	}

	awsMachineTemplateCAPI, err := framework.GetAWSMachineTemplateByPrefix(cl, machineSetNameCAPI)
	if err != nil && !strings.Contains(err.Error(), "no AWSMachineTemplate found") {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("failed to get CAPI AWSMachineTemplate: %w", err))
	}

	if awsMachineTemplateCAPI != nil {
		By(fmt.Sprintf("Deleting AWSMachineTemplate with prefix %s", machineSetNameCAPI))
		if err := framework.DeleteAWSMachineTemplateByPrefix(cl, machineSetNameCAPI); err != nil {
			if !strings.Contains(err.Error(), fmt.Sprintf("no templates found with prefix %q", machineSetNameCAPI)) {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("failed to delete CAPI AWSMachineTemplate: %w", err))
			}
		}
	}

	return cleanupErrors
}
