package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] MachineSet Migration CAPI Authoritative Tests", Ordered, func() {
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this only support on aws", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	var _ = Describe("Create MAPI MachineSets", Ordered, func() {
		var mapiMSAuthCAPIName = "ms-authoritativeapi-capi"
		var existingCAPIMSAuthorityCAPIName = "capi-machineset-authoritativeapi-capi"

		var awsMachineTemplate *awsv1.AWSMachineTemplate
		var capiMachineSet *clusterv1.MachineSet
		var mapiMachineSet *mapiv1beta1.MachineSet

		Context("with spec.authoritativeAPI: ClusterAPI and existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				capiMachineSet = createCAPIMachineSet(ctx, cl, 0, existingCAPIMSAuthorityCAPIName, "m5.large")

				By("Creating a same name MAPI MachineSet")
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, existingCAPIMSAuthorityCAPIName, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				awsMachineTemplate = waitForAWSMachineTemplate(cl, existingCAPIMSAuthorityCAPIName)

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: ClusterAPI and existing CAPI MachineSet with same name' resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should verify that MAPI MachineSet has Paused condition True", func() {
				verifyMAPIMachineSetPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			// bug https://issues.redhat.com/browse/OCPBUGS-55337
			PIt("should verify that the non-authoritative MAPI MachineSet providerSpec has been updated to reflect the authoritative CAPI MachineSet mirror values", func() {
				verifyMAPIMachineSetInstanceType(ctx, cl, mapiMachineSet, "m5.large")
			})
		})

		Context("with spec.authoritativeAPI: ClusterAPI and no existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthCAPIName, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				capiMachineSet = waitForCAPIMachineSetMirror(cl, mapiMSAuthCAPIName)
				awsMachineTemplate = waitForAWSMachineTemplate(cl, mapiMSAuthCAPIName)

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: ClusterAPI and no existing CAPI MachineSet with same name' resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should find MAPI MachineSet .status.authoritativeAPI to equal CAPI", func() {
				verifyMachineSetAuthoritative(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that MAPI MachineSet Paused condition is True", func() {
				verifyMAPIMachineSetPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that MAPI MachineSet Synchronized condition is True", func() {
				verifyMachineSetSynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that the non-authoritative MAPI MachineSet has an authoritative CAPI MachineSet mirror", func() {
				waitForCAPIMachineSetMirror(cl, mapiMSAuthCAPIName)
			})

			It("should verify that CAPI MachineSet has Paused condition False", func() {
				verifyCAPIMachineSetPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})
		})
	})

	var _ = Describe("Scale MAPI MachineSets", Ordered, func() {
		var mapiMSAuthCAPIName = "ms-authoritativeapi-capi"

		var awsMachineTemplate *awsv1.AWSMachineTemplate
		var capiMachineSet *clusterv1.MachineSet
		var mapiMachineSet *mapiv1beta1.MachineSet
		var firstMAPIMachine *mapiv1beta1.Machine
		var secondMAPIMachine *mapiv1beta1.Machine

		Context("with spec.authoritativeAPI: ClusterAPI", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, mapiMSAuthCAPIName, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				capiMachineSet, awsMachineTemplate = verifyMAPIMachineSetHasCAPIMirror(cl, mapiMSAuthCAPIName)
				
				mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				capiMachines := capiframework.GetMachinesFromMachineSet(cl, capiMachineSet)
				Expect(mapiMachines).ToNot(BeEmpty(), "no MAPI Machines found")
				Expect(capiMachines).ToNot(BeEmpty(), "no CAPI Machines found")
				Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name))
				firstMAPIMachine = mapiMachines[0]

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: ClusterAPI' resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should succeed scaling CAPI MachineSet to 2 replicas", func() {
				By("Scaling up CAPI MachineSet to 2 replicas")
				capiframework.ScaleMachineSet(mapiMSAuthCAPIName, 2, capiframework.CAPINamespace)
				capiMachineSet := capiframework.GetMachineSet(cl, mapiMSAuthCAPIName, capiframework.CAPINamespace)
				// TODO: Uncomment once https://github.com/openshift/cluster-capi-operator/pull/329 is merged
				// verifyMAPIMachinesetReplicas(mapiMachineSet, 2)

				By("Verifying a new CAPI Machine is created and Paused condition is False")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
				verifyMachineRunning(cl, capiMachine.Name, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyCAPIMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying there is a non-authoritative, paused MAPI Machine mirror for the new CAPI Machine")
				var err error
				secondMAPIMachine, err = mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineAuthoritative(secondMAPIMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachinePausedCondition(secondMAPIMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should succeed switching MachineSet's AuthoritativeAPI to MachineAPI", func() {
				switchMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSetSynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachineSetPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyCAPIMachineSetPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should succeed scaling up MAPI MachineSet to 3, after switching AuthoritativeAPI to MachineAPI", func() {
				By("Scaling up MAPI MachineSet to 3 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMSAuthCAPIName, 3)).To(Succeed(), "should be able to scale up MAPI MachineSet")
				// TODO: Uncomment once https://github.com/openshift/cluster-capi-operator/pull/329 is merged
				// verifyMAPIMachinesetReplicas(mapiMachineSet, 3)

				By("Verifying the newly requested MAPI Machine has been created and its status.authoritativeAPI is MachineAPI and its Paused condition is False")
				mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineRunning(cl, mapiMachine.Name, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineAuthoritative(mapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachinePausedCondition(mapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Verifying there is a non-authoritative, paused CAPI Machine mirror for the new MAPI Machine")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
				verifyCAPIMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Verifying old Machines still exist and authority on them is still ClusterAPI")
				verifyMachineAuthoritative(firstMAPIMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineAuthoritative(secondMAPIMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should succeed scaling down MAPI MachineSet to 1, after the switch of AuthoritativeAPI to MachineAPI", func() {
				By("Scaling down MAPI MachineSet to 1 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMSAuthCAPIName, 1)).To(Succeed(), "should be able to scale down MAPI MachineSet")
				// TODO: Uncomment once https://github.com/openshift/cluster-capi-operator/pull/329 is merged
				// verifyMAPIMachinesetReplicas(mapiMachineSet, 1)
			})

			It("should succeed switching back MachineSet's AuthoritativeAPI to ClusterAPI, after the initial switch to AuthoritativeAPI: MachineAPI", func() {
				switchMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSetSynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSetPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyCAPIMachineSetPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should delete both MAPI and CAPI MachineSets/Machines and InfraMachineTemplate when deleting CAPI MachineSet", func() {
				capiframework.DeleteMachineSets(cl, capiMachineSet)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
				capiframework.WaitForMachineSetsDeleted(cl, capiMachineSet)
				verifyAWSMachineTemplateDeleted(awsMachineTemplate.Name)
			})
		})
	})

	var _ = Describe("Delete MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName = "ms-authoritativeapi-mapi"
		var mapiMachineSet *mapiv1beta1.MachineSet
		var capiMachineSet *clusterv1.MachineSet
		var awsMachineTemplate *awsv1.AWSMachineTemplate

		Context("when removing non-authoritative MAPI MachineSet", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, mapiMSAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				capiMachineSet, awsMachineTemplate = verifyMAPIMachineSetHasCAPIMirror(cl, mapiMSAuthMAPIName)
				mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				capiMachines := capiframework.GetMachinesFromMachineSet(cl, capiMachineSet)
				Expect(mapiMachines).ToNot(BeEmpty(), "no MAPI Machines found")
				Expect(capiMachines).ToNot(BeEmpty(), "no CAPI Machines found")
				Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name))

				DeferCleanup(func() {
					By("Cleaning up Context 'when removing non-authoritative MAPI MachineSet' resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("shouldn't delete its authoritative CAPI MachineSet", func() {
				By("Switching AuthoritativeAPI to ClusterAPI")
				switchMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Scaling up CAPI MachineSet to 2 replicas")
				capiframework.ScaleMachineSet(mapiMachineSet.GetName(), 2, capiframework.CAPINamespace)
				// TODO: Uncomment once https://github.com/openshift/cluster-capi-operator/pull/329 is merged
				// verifyMAPIMachinesetReplicas(mapiMachineSet, 2)

				By("Verifying new CAPI Machine is running")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
				verifyMachineRunning(cl, capiMachine.Name, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyCAPIMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying there is a non-authoritative, paused MAPI Machine mirror for the new CAPI Machine")
				mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineAuthoritative(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachinePausedCondition(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Deleting non-authoritative MAPI MachineSet")
				mapiMachineSet, err = mapiframework.GetMachineSet(ctx, cl, mapiMSAuthMAPIName)
				Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet")
				mapiframework.DeleteMachineSets(cl, mapiMachineSet)

				By("Verifying CAPI MachineSet not removed, both MAPI Machines and Mirrors remain")
				// bug https://issues.redhat.com/browse/OCPBUGS-56897
				/*
					Consistently(func() error {
						capiMachineSet := capiframework.GetMachineSet(cl, mapiMSAuthMAPIName, capiframework.CAPINamespace)
						if capiMachineSet == nil {
							return fmt.Errorf("CAPI MachineSet is nil")
						}

						capiMachines := capiframework.GetMachinesFromMachineSet(cl, capiMachineSet)
						if len(capiMachines) == 0 {
							return fmt.Errorf("CAPI Machines were deleted")
						}

						mapiMachine, err :=mapiframework.GetMachine(cl,capiMachines[0].Name)
						if err != nil {
							return fmt.Errorf("failed to get MAPI Machines: %w", err)
						}
						if mapiMachine == nil {
							return fmt.Errorf("MAPI Machine were deleted")
						}

						return nil
					}, capiframework.WaitLong, capiframework.RetryLong).Should(Succeed(), "Both MAPI and CAPI Machines should persist for 15 minutes")

					By("Verifying no owner references on MAPI Machines")
					mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
					Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
					for _, machine := range mapiMachines {
						Expect(machine.GetOwnerReferences()).To(BeEmpty(), "MAPI Machine %s should have no owner references", machine.Name)
					}
				*/
			})
		})
	})
})
