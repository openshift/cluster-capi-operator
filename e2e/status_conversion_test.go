package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Status Conversion Tests", Ordered, func() {
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this only support on aws", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	var _ = Describe("MAPI to CAPI MachineSet Status Conversion", Ordered, func() {
		var mapiMachineSetName = "status-conversion-mapi-machineset"
		var mapiMachineSet *mapiv1beta1.MachineSet
		var capiMachineSet *clusterv1.MachineSet
		var awsMachineTemplate *awsv1.AWSMachineTemplate
		var mapiMachine *mapiv1beta1.Machine
		var capiMachine *clusterv1.Machine
		var err error

		Context("when converting MAPI MachineSet status to CAPI", func() {
			BeforeAll(func() {
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, mapiMachineSetName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				capiMachineSet, awsMachineTemplate = waitForMAPIMachineSetMirrors(cl, mapiMachineSetName)

				DeferCleanup(func() {
					By("Cleaning up MachineSet status conversion test resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should convert status.replicas from MAPI to CAPI", func() {
				Eventually(komega.Object(capiMachineSet), capiframework.WaitLong, capiframework.RetryLong).Should(
					HaveField("Status.Replicas", Equal(int32(1))),
					"Should have CAPI MachineSet status.replicas equal to 1",
				)
			})

			It("should convert status.readyReplicas from MAPI to CAPI status and status.v1beta2", func() {
				By("Waiting for MAPI MachineSet to have readyReplicas")
				Eventually(komega.Object(mapiMachineSet), capiframework.WaitLong, capiframework.RetryLong).Should(
					HaveField("Status.ReadyReplicas", BeNumerically(">", 0)),
					"Should have MAPI MachineSet with readyReplicas > 0",
				)

				By("Verifying CAPI MachineSet status.readyReplicas is synchronized")
				Eventually(komega.Object(capiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.ReadyReplicas", Equal(mapiMachineSet.Status.ReadyReplicas)),
					"Should have CAPI MachineSet status.readyReplicas match MAPI",
				)

				By("Verifying CAPI MachineSet status.v1beta2.readyReplicas is synchronized")
				Eventually(komega.Object(capiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.V1Beta2.ReadyReplicas", HaveValue(Equal(mapiMachineSet.Status.ReadyReplicas))),
					"Should have CAPI MachineSet status.v1beta2.readyReplicas match MAPI",
				)
			})

			It("should convert status.availableReplicas from MAPI to CAPI status and status.v1beta2", func() {
				By("Verifying CAPI MachineSet status.availableReplicas is synchronized")
				Eventually(komega.Object(capiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.AvailableReplicas", Equal(mapiMachineSet.Status.AvailableReplicas)),
					"Should have CAPI MachineSet status.availableReplicas match MAPI",
				)

				By("Verifying CAPI MachineSet status.v1beta2.availableReplicas is synchronized")
				Eventually(komega.Object(capiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.V1Beta2.AvailableReplicas", HaveValue(Equal(mapiMachineSet.Status.AvailableReplicas))),
					"Should have CAPI MachineSet status.v1beta2.availableReplicas match MAPI",
				)
			})

			It("should convert status.fullyLabeledReplicas from MAPI to CAPI", func() {
				Eventually(komega.Object(capiMachineSet), capiframework.WaitLong, capiframework.RetryLong).Should(
					HaveField("Status.FullyLabeledReplicas", Equal(mapiMachineSet.Status.FullyLabeledReplicas)),
					"Should have CAPI MachineSet status.fullyLabeledReplicas match MAPI",
				)
			})

			It("should convert status.ObservedGeneration from MAPI to CAPI", func() {
				Eventually(komega.Object(capiMachineSet), capiframework.WaitLong, capiframework.RetryLong).Should(
					HaveField("Status.ObservedGeneration", Equal(mapiMachineSet.Status.ObservedGeneration)),
					"Should have CAPI MachineSet status.ObservedGeneration match MAPI",
				)
			})

			It("should convert MAPI status to CAPI MachineSet conditions", func() {
				Eventually(komega.Object(capiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					SatisfyAll(
						HaveField("Status.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.ReadyCondition)),
							HaveField("Status", Equal(corev1.ConditionTrue)),
						))),
						HaveField("Status.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachinesReadyCondition)),
							HaveField("Status", Equal(corev1.ConditionTrue)),
						))),
						HaveField("Status.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachinesCreatedCondition)),
							HaveField("Status", Equal(corev1.ConditionTrue)),
						))),
						HaveField("Status.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.ResizedCondition)),
							HaveField("Status", Equal(corev1.ConditionTrue)),
						))),
					),
					"Should have correct CAPI MachineSet conditions",
				)
			})

			It("should convert MAPI status to CAPI MachineSet V1Beta2 conditions", func() {
				Eventually(komega.Object(capiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					SatisfyAll(
						HaveField("Status.V1Beta2.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachineSetMachinesReadyV1Beta2Condition)),
							HaveField("Status", Equal(metav1.ConditionTrue)),
							HaveField("Reason", Equal(clusterv1.MachineSetMachinesReadyV1Beta2Reason)),
						))),
						HaveField("Status.V1Beta2.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachineSetMachinesUpToDateV1Beta2Condition)),
							HaveField("Status", Equal(metav1.ConditionTrue)),
							HaveField("Reason", Equal(clusterv1.MachineSetMachinesUpToDateV1Beta2Reason)),
						))),
						HaveField("Status.V1Beta2.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachineSetScalingUpV1Beta2Condition)),
							HaveField("Status", Equal(metav1.ConditionFalse)),
							HaveField("Reason", Equal(clusterv1.MachineSetNotScalingUpV1Beta2Reason)),
						))),
						HaveField("Status.V1Beta2.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachineSetScalingDownV1Beta2Condition)),
							HaveField("Status", Equal(metav1.ConditionFalse)),
							HaveField("Reason", Equal(clusterv1.MachineSetNotScalingDownV1Beta2Reason)),
						))),
						HaveField("Status.V1Beta2.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachineSetDeletingV1Beta2Condition)),
							HaveField("Status", Equal(metav1.ConditionFalse)),
							HaveField("Reason", Equal(clusterv1.MachineSetNotDeletingV1Beta2Reason)),
						))),
					),
					"Should have correct CAPI MachineSet V1Beta2 conditions",
				)
			})

			It("should NOT have MAPI Synchronized condition in CAPI MachineSet", func() {
				By("Verifying MAPI MachineSet has Synchronized condition")
				Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Conditions", ContainElement(HaveField("Type", Equal(SynchronizedCondition)))),
					"Should have MAPI MachineSet with Synchronized condition",
				)

				By("Verifying CAPI MachineSet does NOT have Synchronized condition")
				Consistently(komega.Object(capiMachineSet), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal("Synchronized"))))),
					"Should NOT have Synchronized condition in CAPI MachineSet",
				)
			})

			// bug https://issues.redhat.com/browse/OCPBUGS-63232
			PIt("should convert spec.selector to CAPI MachineSet status.selector", func() {
				Eventually(komega.Object(capiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Selector", Not(BeEmpty())),
					"Should have CAPI MachineSet status.selector set",
				)
			})
		})

		Context("when MAPI MachineSet has error status with invalid configuration", func() {
			BeforeAll(func() {
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMachineSetName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				capiMachineSet, awsMachineTemplate = waitForMAPIMachineSetMirrors(cl, mapiMachineSetName)

				By("Updating MAPI Machine with invalid instanceType to trigger error")
				updateAWSMachineSetProviderSpec(ctx, cl, mapiMachineSet, func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
					providerSpec.InstanceType = "invalid"
				})

				mapiframework.ScaleMachineSet(mapiMachineSetName, 1)
				mapiMachine, err = mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				capiMachine = capiframework.GetMachine(cl, mapiMachine.Name, capiframework.CAPINamespace)

				DeferCleanup(func() {
					By("Cleaning up error Machine test resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should convert MAPI Machine error status to CAPI Machine failureReason and failureMessage", func() {
				By("Waiting for MAPI Machine to have error status")
				Eventually(komega.Object(mapiMachine), capiframework.WaitLong, capiframework.RetryLong).Should(
					SatisfyAny(
						HaveField("Status.ErrorReason", Not(BeNil())),
						HaveField("Status.ErrorMessage", Not(BeNil())),
					),
					"Should have MAPI Machine with error status",
				)

				By("Verifying CAPI Machine has matching failureReason")
				if mapiMachine.Status.ErrorReason != nil {
					Eventually(komega.Object(capiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
						HaveField("Status.FailureReason", HaveValue(BeEquivalentTo(*mapiMachine.Status.ErrorReason))),
						"Should have CAPI Machine failureReason match MAPI Machine errorReason",
					)
				}

				By("Verifying CAPI Machine has matching failureMessage")
				if mapiMachine.Status.ErrorMessage != nil {
					Eventually(komega.Object(capiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
						HaveField("Status.FailureMessage", HaveValue(Equal(*mapiMachine.Status.ErrorMessage))),
						"Should have CAPI Machine failureMessage match MAPI Machine errorMessage",
					)
				}
			})

			It("should convert MAPI Machine phase to Failed in CAPI Machine", func() {
				By("Waiting for MAPI Machine to be in Failed phase")
				Eventually(komega.Object(mapiMachine), capiframework.WaitLong, capiframework.RetryLong).Should(
					HaveField("Status.Phase", HaveValue(Equal("Failed"))),
					"Should have MAPI Machine in Failed phase",
				)

				By("Verifying CAPI Machine has Failed phase")
				Eventually(komega.Object(capiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Phase", Equal("Failed")),
					"Should have CAPI Machine phase be Failed",
				)
			})
		})
	})

	var _ = Describe("CAPI to MAPI MachineSet Status Conversion", Ordered, func() {
		var machineSetName = "status-conversion-capi-auth-machineset"
		var mapiMachineSet *mapiv1beta1.MachineSet
		var capiMachineSet *clusterv1.MachineSet
		var awsMachineTemplate *awsv1.AWSMachineTemplate
		var newAWSMachineTemplate *awsv1.AWSMachineTemplate
		var mapiMachine *mapiv1beta1.Machine
		var capiMachine *clusterv1.Machine
		var err error

		Context("when converting CAPI MachineSet status to MAPI", func() {
			BeforeAll(func() {
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, machineSetName, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				capiMachineSet, awsMachineTemplate = waitForMAPIMachineSetMirrors(cl, machineSetName)
				capiframework.WaitForMachineSet(cl, machineSetName, capiframework.CAPINamespace)

				DeferCleanup(func() {
					By("Cleaning up CAPI to MAPI MachineSet status conversion test resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should convert status.replicas from CAPI to MAPI", func() {
				Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Replicas", Equal(capiMachineSet.Status.Replicas)),
					"Should have MAPI MachineSet status.replicas match CAPI",
				)
			})

			It("should convert status.readyReplicas from CAPI to MAPI", func() {
				By("Waiting for CAPI MachineSet to have readyReplicas")
				Eventually(komega.Object(capiMachineSet), capiframework.WaitLong, capiframework.RetryLong).Should(
					HaveField("Status.ReadyReplicas", BeNumerically(">", 0)),
					"Should have CAPI MachineSet with readyReplicas > 0",
				)

				By("Verifying MAPI MachineSet status.readyReplicas is synchronized")
				Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.ReadyReplicas", Equal(capiMachineSet.Status.ReadyReplicas)),
					"Should have MAPI MachineSet status.readyReplicas match CAPI",
				)
			})

			It("should convert status.availableReplicas from CAPI to MAPI", func() {
				Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.AvailableReplicas", Equal(capiMachineSet.Status.AvailableReplicas)),
					"Should have MAPI MachineSet status.availableReplicas match CAPI",
				)
			})

			It("should convert status.fullyLabeledReplicas from CAPI to MAPI", func() {
				Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.FullyLabeledReplicas", Equal(capiMachineSet.Status.FullyLabeledReplicas)),
					"Should have MAPI MachineSet status.fullyLabeledReplicas match CAPI",
				)
			})

			It("should convert status.ObservedGeneration from CAPI to MAPI", func() {
				Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.ObservedGeneration", Equal(capiMachineSet.Status.ObservedGeneration)),
					"Should have MAPI MachineSet status.ObservedGeneration match CAPI",
				)
			})

			It("should have MAPI MachineSet Synchronized condition", func() {
				Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", Equal(SynchronizedCondition)),
						HaveField("Status", Equal(corev1.ConditionTrue)),
					))),
					"Should have MAPI MachineSet with Synchronized condition",
				)
			})

			It("should NOT have CAPI-specific conditions in MAPI MachineSet", func() {
				By("Verifying MAPI MachineSet does NOT have CAPI-specific conditions")
				Consistently(komega.Object(mapiMachineSet), capiframework.WaitShort, capiframework.RetryShort).Should(
					SatisfyAll(
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.ReadyCondition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.MachinesReadyCondition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.ResizedCondition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.MachinesCreatedCondition))))),
					),
					"Should NOT have CAPI conditions in MAPI MachineSet",
				)
			})
		})

		Context("when CAPI MachineSet exists and MAPI MachineSet with CAPI authority is created with same name", func() {
			BeforeAll(func() {
				capiMachineSet = createCAPIMachineSet(ctx, cl, 1, machineSetName, "")

				By("Creating a same name MAPI MachineSet")
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, machineSetName, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				capiMachineSet, awsMachineTemplate = waitForMAPIMachineSetMirrors(cl, machineSetName)

				DeferCleanup(func() {
					By("Cleaning up same-name CAPI-first MachineSet test resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should convert status.replicas from CAPI to MAPI", func() {
				Eventually(komega.Object(capiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Replicas", BeNumerically(">", 0)),
					"Should have CAPI MachineSet status.replicas > 0",
				)

				Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Replicas", Equal(capiMachineSet.Status.Replicas)),
					"Should have MAPI MachineSet status.replicas match CAPI",
				)
			})

			It("should convert status.readyReplicas from CAPI to MAPI", func() {
				By("Waiting for CAPI MachineSet to have readyReplicas")
				Eventually(komega.Object(capiMachineSet), capiframework.WaitLong, capiframework.RetryLong).Should(
					HaveField("Status.ReadyReplicas", BeNumerically(">", 0)),
					"Should have CAPI MachineSet with readyReplicas > 0",
				)

				By("Verifying MAPI MachineSet status.readyReplicas is synchronized")
				Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.ReadyReplicas", Equal(capiMachineSet.Status.ReadyReplicas)),
					"Should have MAPI MachineSet status.readyReplicas match CAPI",
				)
			})

			It("should convert status.availableReplicas from CAPI to MAPI", func() {
				Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.AvailableReplicas", Equal(capiMachineSet.Status.AvailableReplicas)),
					"Should have MAPI MachineSet status.availableReplicas match CAPI",
				)
			})

			It("should convert status.fullyLabeledReplicas from CAPI to MAPI", func() {
				Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.FullyLabeledReplicas", Equal(capiMachineSet.Status.FullyLabeledReplicas)),
					"Should have MAPI MachineSet status.fullyLabeledReplicas match CAPI",
				)
			})

			It("should convert status.ObservedGeneration from CAPI to MAPI", func() {
				Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.ObservedGeneration", Equal(capiMachineSet.Status.ObservedGeneration)),
					"Should have MAPI MachineSet status.ObservedGeneration match CAPI",
				)
			})

			It("should have MAPI MachineSet Synchronized condition", func() {
				Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", Equal(SynchronizedCondition)),
						HaveField("Status", Equal(corev1.ConditionTrue)),
					))),
					"Should have MAPI MachineSet with Synchronized condition",
				)
			})

			It("should NOT have CAPI-specific conditions in MAPI MachineSet", func() {
				By("Verifying MAPI MachineSet does NOT have CAPI-specific conditions")
				Consistently(komega.Object(mapiMachineSet), capiframework.WaitShort, capiframework.RetryShort).Should(
					SatisfyAll(
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.ReadyCondition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.MachinesReadyCondition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.ResizedCondition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.MachinesCreatedCondition))))),
					),
					"Should NOT have CAPI conditions in MAPI MachineSet",
				)
			})
		})

		Context("when CAPI MachineSet has failure status with invalid configuration", func() {
			BeforeAll(func() {
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, machineSetName, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				capiMachineSet, awsMachineTemplate = waitForMAPIMachineSetMirrors(cl, machineSetName)

				newAWSMachineTemplate = createAWSMachineTemplate(ctx, cl, awsMachineTemplate.Name, func(spec *awsv1.AWSMachineSpec) {
					spec.InstanceType = "invalid"
				})

				By("Updating CAPI MachineSet to point to the new InfraTemplate")
				updateCAPIMachineSetInfraTemplate(capiMachineSet, newAWSMachineTemplate.Name)
				capiframework.ScaleCAPIMachineSet(machineSetName, 1, capiframework.CAPINamespace)
				capiMachine = capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
				mapiMachine, err = mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")

				DeferCleanup(func() {
					By("Cleaning up CAPI error Machine test resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate, newAWSMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			// bug https://issues.redhat.com/browse/OCPBUGS-63340
			PIt("should convert CAPI Machine failureReason and failureMessage to MAPI Machine error status", func() {
				By("Verifying CAPI Machine has failure status set")
				Expect(capiMachine.Status.FailureReason).NotTo(BeNil(), "CAPI Machine should have failureReason")
				Expect(capiMachine.Status.FailureMessage).NotTo(BeNil(), "CAPI Machine should have failureMessage")

				By("Verifying MAPI Machine has matching errorReason")
				Eventually(komega.Object(mapiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.ErrorReason", HaveValue(BeEquivalentTo(*capiMachine.Status.FailureReason))),
					"Should have MAPI Machine errorReason match CAPI Machine failureReason",
				)

				By("Verifying MAPI Machine has matching errorMessage")
				Eventually(komega.Object(mapiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.ErrorMessage", HaveValue(Equal(*capiMachine.Status.FailureMessage))),
					"Should have MAPI Machine errorMessage match CAPI Machine failureMessage",
				)
			})

			// bug https://issues.redhat.com/browse/OCPBUGS-63340
			PIt("should convert CAPI Machine Failed phase to MAPI Machine", func() {
				By("Verifying CAPI Machine is in Failed phase")
				Expect(capiMachine.Status.Phase).To(Equal("Failed"), "CAPI Machine should be in Failed phase")

				By("Verifying MAPI Machine has Failed phase")
				Eventually(komega.Object(mapiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Phase", HaveValue(Equal("Failed"))),
					"Should have MAPI Machine phase be Failed",
				)
			})
		})
	})

	var _ = Describe("MAPI to CAPI Machine Status Conversion", Ordered, func() {
		var mapiMachineName = "status-conversion-mapi-machine1"
		var mapiMachine *mapiv1beta1.Machine
		var capiMachine *clusterv1.Machine
		Context("when converting MAPI Machine status to CAPI", func() {
			BeforeAll(func() {
				mapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineName, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineRunning(cl, mapiMachine)
				capiMachine = capiframework.GetMachine(cl, mapiMachine.Name, capiframework.CAPINamespace)

				DeferCleanup(func() {
					By("Cleaning up Machine status conversion test resources")
					cleanupMachineResources(ctx, cl, []*clusterv1.Machine{}, []*mapiv1beta1.Machine{mapiMachine})
				})
			})

			It("should convert MAPI Machine phase to CAPI Machine phase", func() {
				By("Verifying CAPI Machine has matching phase")
				mapiMachine, err := mapiframework.GetMachine(cl, mapiMachineName)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machine")

				Eventually(komega.Object(capiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Phase", Equal(ptr.Deref(mapiMachine.Status.Phase, ""))),
					"Should have CAPI Machine phase match MAPI Machine phase",
				)
			})

			It("should convert MAPI Machine nodeRef to CAPI Machine nodeRef", func() {
				By("Waiting for MAPI Machine to have nodeRef")
				Eventually(komega.Object(mapiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.NodeRef", Not(BeNil())),
					"Should have MAPI Machine with nodeRef set",
				)

				By("Verifying CAPI Machine has matching nodeRef")
				Eventually(komega.Object(capiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.NodeRef", Equal(mapiMachine.Status.NodeRef)),
					"Should have CAPI Machine nodeRef match MAPI Machine nodeRef",
				)
			})

			It("should convert MAPI Machine lastUpdated to CAPI Machine lastUpdated", func() {
				By("Verifying MAPI Machine has lastUpdated")
				Expect(mapiMachine.Status.LastUpdated).NotTo(BeNil(), "MAPI Machine should have lastUpdated")

				By("Verifying CAPI Machine has matching lastUpdated")
				Eventually(komega.Object(capiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.LastUpdated", Equal(mapiMachine.Status.LastUpdated)),
					"Should have CAPI Machine lastUpdated match MAPI Machine lastUpdated",
				)
			})

			It("should convert MAPI Machine addresses to CAPI Machine addresses", func() {
				By("Waiting for MAPI Machine to have addresses")
				Eventually(komega.Object(mapiMachine), capiframework.WaitLong, capiframework.RetryLong).Should(
					HaveField("Status.Addresses", Not(BeEmpty())),
					"Should have MAPI Machine with addresses",
				)

				By("Verifying CAPI Machine has matching addresses")
				Eventually(func() bool {
					if len(capiMachine.Status.Addresses) != len(mapiMachine.Status.Addresses) {
						return false
					}
					for i, mapiAddr := range mapiMachine.Status.Addresses {
						capiAddr := capiMachine.Status.Addresses[i]
						if string(capiAddr.Type) != string(mapiAddr.Type) || capiAddr.Address != mapiAddr.Address {
							return false
						}
					}
					return true
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(
					BeTrue(),
					"Should have CAPI Machine addresses match MAPI Machine addresses",
				)
			})

			It("should convert MAPI Machine conditions to CAPI Machine conditions", func() {
				By("Waiting for MAPI Machine to have conditions")
				Eventually(komega.Object(mapiMachine), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.Conditions", Not(BeEmpty())),
					"Should have MAPI Machine with conditions",
				)

				By("Verifying CAPI Machine has standard conditions set")
				Eventually(komega.Object(capiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					SatisfyAll(
						HaveField("Status.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.ReadyCondition)),
							HaveField("Status", Equal(corev1.ConditionTrue)),
						))),
						HaveField("Status.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.BootstrapReadyCondition)),
							HaveField("Status", Equal(corev1.ConditionTrue)),
						))),
						HaveField("Status.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.InfrastructureReadyCondition)),
							HaveField("Status", Equal(corev1.ConditionTrue)),
						))),
					),
					"Should have CAPI Machine standard conditions set",
				)
			})

			It("should convert MAPI Machine conditions to CAPI Machine V1Beta2 conditions", func() {
				By("Verifying CAPI Machine has V1Beta2 conditions set")
				Eventually(komega.Object(capiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					SatisfyAll(
						HaveField("Status.V1Beta2.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachineAvailableV1Beta2Condition)),
							HaveField("Status", Equal(metav1.ConditionTrue)),
						))),
						HaveField("Status.V1Beta2.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachineReadyV1Beta2Condition)),
							HaveField("Status", Equal(metav1.ConditionTrue)),
						))),
						HaveField("Status.V1Beta2.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachineBootstrapConfigReadyV1Beta2Condition)),
							HaveField("Status", Equal(metav1.ConditionTrue)),
						))),
						HaveField("Status.V1Beta2.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachineInfrastructureReadyV1Beta2Condition)),
							HaveField("Status", Equal(metav1.ConditionTrue)),
						))),
						HaveField("Status.V1Beta2.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachineNodeReadyV1Beta2Condition)),
							HaveField("Status", Equal(metav1.ConditionTrue)),
						))),
						HaveField("Status.V1Beta2.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachineDeletingV1Beta2Condition)),
							HaveField("Status", Equal(metav1.ConditionFalse)),
						))),
					),
					"Should have CAPI Machine V1Beta2 conditions set",
				)
			})

			It("should NOT have MAPI Synchronized condition in CAPI Machine", func() {
				By("Verifying MAPI Machine has Synchronized condition")
				Eventually(komega.Object(mapiMachine), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.Conditions", ContainElement(HaveField("Type", Equal(SynchronizedCondition)))),
					"Should have MAPI Machine with Synchronized condition",
				)

				By("Verifying CAPI Machine does NOT have Synchronized condition in V1Beta2 conditions")
				Consistently(komega.Object(capiMachine), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.V1Beta2.Conditions", Not(ContainElement(HaveField("Type", Equal("Synchronized"))))),
					"Should NOT have Synchronized condition in CAPI Machine V1Beta2 conditions",
				)
			})
		})
	})

	var _ = Describe("CAPI to MAPI Machine Status Conversion", Ordered, func() {
		var capiMachineName = "status-conversion-capi-machine4"
		var mapiMachine *mapiv1beta1.Machine
		var capiMachine *clusterv1.Machine
		Context("when converting CAPI Machine status to MAPI", func() {
			BeforeAll(func() {
				mapiMachine = createMAPIMachineWithAuthority(ctx, cl, capiMachineName, mapiv1beta1.MachineAuthorityClusterAPI)
				capiMachine = capiframework.GetMachine(cl, mapiMachine.Name, capiframework.CAPINamespace)
				verifyMachineRunning(cl, capiMachine)

				DeferCleanup(func() {
					By("Cleaning up CAPI to MAPI Machine status conversion test resources")
					cleanupMachineResources(ctx, cl, []*clusterv1.Machine{capiMachine}, []*mapiv1beta1.Machine{mapiMachine})
				})
			})

			It("should convert CAPI Machine phase to MAPI Machine phase", func() {
				By("Verifying MAPI Machine has matching phase from CAPI")
				mapiMachine, err := mapiframework.GetMachine(cl, capiMachineName)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machine")
				capiMachine = capiframework.GetMachine(cl, mapiMachine.Name, capiframework.CAPINamespace)

				Eventually(komega.Object(mapiMachine), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.Phase", HaveValue(Equal(capiMachine.Status.Phase))),
					"Should have MAPI Machine phase match CAPI Machine phase",
				)
			})

			It("should convert CAPI Machine nodeRef to MAPI Machine nodeRef", func() {
				By("Waiting for CAPI Machine to have nodeRef")
				Eventually(komega.Object(capiMachine), capiframework.WaitLong, capiframework.RetryShort).Should(
					HaveField("Status.NodeRef", Not(BeNil())),
					"Should have CAPI Machine with nodeRef set",
				)

				By("Verifying MAPI Machine has matching nodeRef from CAPI")
				Eventually(komega.Object(mapiMachine), capiframework.WaitShort, capiframework.RetryShort).Should(
					SatisfyAll(
						HaveField("Status.NodeRef.Name", Equal(capiMachine.Status.NodeRef.Name)),
						HaveField("Status.NodeRef.UID", Equal(capiMachine.Status.NodeRef.UID)),
					),
					"Should have MAPI Machine nodeRef match CAPI Machine nodeRef",
				)
			})

			It("should convert CAPI Machine lastUpdated to MAPI Machine lastUpdated", func() {
				By("Verifying CAPI Machine has lastUpdated")
				Expect(capiMachine.Status.LastUpdated).NotTo(BeNil(), "CAPI Machine should have lastUpdated")

				By("Verifying MAPI Machine has matching lastUpdated from CAPI")
				Eventually(komega.Object(mapiMachine), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.LastUpdated", Equal(capiMachine.Status.LastUpdated)),
					"Should have MAPI Machine lastUpdated match CAPI Machine lastUpdated",
				)
			})

			It("should convert CAPI Machine addresses to MAPI Machine addresses", func() {
				By("Waiting for CAPI Machine to have addresses")
				Eventually(komega.Object(capiMachine), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.Addresses", Not(BeEmpty())),
					"Should have CAPI Machine with addresses",
				)

				By("Verifying MAPI Machine has matching addresses from CAPI")
				Eventually(func() bool {
					if len(mapiMachine.Status.Addresses) != len(capiMachine.Status.Addresses) {
						return false
					}
					for i, capiAddr := range capiMachine.Status.Addresses {
						mapiAddr := mapiMachine.Status.Addresses[i]
						if string(mapiAddr.Type) != string(capiAddr.Type) || mapiAddr.Address != capiAddr.Address {
							return false
						}
					}
					return true
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(
					BeTrue(),
					"Should have MAPI Machine addresses match CAPI Machine addresses",
				)
			})

			It("should NOT have CAPI-specific conditions in MAPI Machine", func() {
				By("Verifying MAPI Machine does NOT have CAPI-specific conditions")
				Consistently(komega.Object(mapiMachine), capiframework.WaitShort, capiframework.RetryShort).Should(
					SatisfyAll(
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.ReadyCondition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.MachinesReadyCondition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.ResizedCondition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.MachinesCreatedCondition))))),
					),
					"Should NOT have CAPI conditions in MAPI Machine",
				)
			})
		})

		Context("when CAPI Machine exists and MAPI Machine with CAPI authority is created with same name", func() {
			BeforeAll(func() {
				capiMachine = createCAPIMachine(ctx, cl, capiMachineName)
				mapiMachine = createMAPIMachineWithAuthority(ctx, cl, capiMachineName, mapiv1beta1.MachineAuthorityClusterAPI)

				DeferCleanup(func() {
					By("Cleaning up same-name CAPI-first Machine test resources")
					cleanupMachineResources(ctx, cl, []*clusterv1.Machine{capiMachine}, []*mapiv1beta1.Machine{mapiMachine})
				})
			})

			// bug https://issues.redhat.com/browse/OCPBUGS-63183
			PIt("should convert CAPI Machine phase to MAPI Machine phase", func() {
				By("Verifying MAPI Machine has matching phase from CAPI")
				mapiMachine, err := mapiframework.GetMachine(cl, capiMachineName)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machine")
				capiMachine = capiframework.GetMachine(cl, mapiMachine.Name, capiframework.CAPINamespace)

				Eventually(komega.Object(mapiMachine), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.Phase", HaveValue(Equal(capiMachine.Status.Phase))),
					"Should have MAPI Machine phase match CAPI Machine phase",
				)
			})

			It("should convert CAPI Machine nodeRef to MAPI Machine nodeRef", func() {
				By("Waiting for CAPI Machine to have nodeRef")
				Eventually(komega.Object(capiMachine), capiframework.WaitLong, capiframework.RetryShort).Should(
					HaveField("Status.NodeRef", Not(BeNil())),
					"Should have CAPI Machine with nodeRef set",
				)

				By("Verifying MAPI Machine has matching nodeRef from CAPI")
				Eventually(komega.Object(mapiMachine), capiframework.WaitShort, capiframework.RetryShort).Should(
					SatisfyAll(
						HaveField("Status.NodeRef.Name", Equal(capiMachine.Status.NodeRef.Name)),
						HaveField("Status.NodeRef.UID", Equal(capiMachine.Status.NodeRef.UID)),
					),
					"Should have MAPI Machine nodeRef match CAPI Machine nodeRef",
				)
			})

			// bug https://issues.redhat.com/browse/OCPBUGS-63183
			PIt("should convert CAPI Machine lastUpdated to MAPI Machine lastUpdated", func() {
				By("Verifying CAPI Machine has lastUpdated")
				Expect(capiMachine.Status.LastUpdated).NotTo(BeNil(), "CAPI Machine should have lastUpdated")

				By("Verifying MAPI Machine has matching lastUpdated from CAPI")
				Eventually(komega.Object(mapiMachine), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.LastUpdated", Equal(capiMachine.Status.LastUpdated)),
					"Should have MAPI Machine lastUpdated match CAPI Machine lastUpdated",
				)
			})

			// bug https://issues.redhat.com/browse/OCPBUGS-63183
			PIt("should convert CAPI Machine addresses to MAPI Machine addresses", func() {
				By("Waiting for CAPI Machine to have addresses")
				Eventually(komega.Object(capiMachine), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.Addresses", Not(BeEmpty())),
					"Should have CAPI Machine with addresses",
				)

				By("Verifying MAPI Machine has matching addresses from CAPI")
				Eventually(func() bool {
					if len(mapiMachine.Status.Addresses) != len(capiMachine.Status.Addresses) {
						return false
					}
					for i, capiAddr := range capiMachine.Status.Addresses {
						mapiAddr := mapiMachine.Status.Addresses[i]
						if string(mapiAddr.Type) != string(capiAddr.Type) || mapiAddr.Address != capiAddr.Address {
							return false
						}
					}
					return true
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(
					BeTrue(),
					"Should have MAPI Machine addresses match CAPI Machine addresses",
				)
			})

			It("should NOT have CAPI-specific conditions in MAPI Machine", func() {
				By("Verifying MAPI Machine does NOT have CAPI-specific conditions")
				Consistently(komega.Object(mapiMachine), capiframework.WaitShort, capiframework.RetryShort).Should(
					SatisfyAll(
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.ReadyCondition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.MachinesReadyCondition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.ResizedCondition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.MachinesCreatedCondition))))),
					),
					"Should NOT have CAPI conditions in MAPI Machine",
				)
			})
		})
	})
})
