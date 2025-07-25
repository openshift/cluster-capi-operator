package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

const (
	SynchronizedCondition machinev1beta1.ConditionType = "Synchronized"
	MAPIPausedCondition   machinev1beta1.ConditionType = "Paused"
	CAPIPausedCondition                                = clusterv1.PausedV1Beta2Condition
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] MachineSet Migration Tests", Ordered, func() {
	var k komega.Komega

	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this only support on aws", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}

		k = komega.New(k8sClient)
	})

	var _ = Describe("Create MAPI MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName = "ms-authoritativeapi-mapi"
		var mapiMSAuthCAPIName = "ms-authoritativeapi-capi"
		var existingCAPIMSAuthorityMAPIName = "capi-machineset-authoritativeapi-mapi"
		var existingCAPIMSAuthorityCAPIName = "capi-machineset-authoritativeapi-capi"

		var awsMachineTemplate *capav1.AWSMachineTemplate
		var capiMachineSet *clusterv1.MachineSet
		var mapiMachineSet *machinev1beta1.MachineSet

		Context("with spec.authoritativeAPI: MachineAPI and existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				capiMachineSet = createCAPIMachineSet(ctx, cl, 0, existingCAPIMSAuthorityMAPIName, "")
				awsMachineTemplate = waitForAWSMachineTemplate(cl, existingCAPIMSAuthorityMAPIName)

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MachineAPI and existing CAPI MachineSet with same name' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*capav1.AWSMachineTemplate{awsMachineTemplate},
						[]*machinev1beta1.MachineSet{},
					)
				})
			})

			// https://issues.redhat.com/browse/OCPCLOUD-2641
			PIt("should reject creation of MAPI MachineSet with same name as existing CAPI MachineSet", func() {
				By("Creating a same name MAPI MachineSet")
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, existingCAPIMSAuthorityMAPIName, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
			})
		})

		Context("with spec.authoritativeAPI: MachineAPI and when no existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthMAPIName, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
				capiMachineSet = waitForCAPIMachineSetMirror(cl, mapiMSAuthMAPIName)
				awsMachineTemplate = waitForAWSMachineTemplate(cl, mapiMSAuthMAPIName)

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MachineAPI and when no existing CAPI MachineSet with same name' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{},
						[]*capav1.AWSMachineTemplate{awsMachineTemplate},
						[]*machinev1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should find MAPI MachineSet .status.authoritativeAPI to equal MAPI", func() {
				verifyMachineSetAuthoritative(mapiMachineSet, machinev1beta1.MachineAuthorityMachineAPI)
			})

			It("should verify that MAPI MachineSet Paused condition is False", func() {
				verifyMAPIPausedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityMachineAPI)
			})

			It("should verify that MAPI MachineSet Synchronized condition is True", func() {
				verifySynchronizedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityMachineAPI)
			})

			It("should find that MAPI MachineSet has a CAPI MachineSet mirror", func() {
				waitForCAPIMachineSetMirror(cl, mapiMSAuthMAPIName)
			})

			It("should verify that the mirror CAPI MachineSet has Paused condition True", func() {
				verifyCAPIPausedCondition(capiMachineSet, machinev1beta1.MachineAuthorityMachineAPI)
			})
		})

		Context("with spec.authoritativeAPI: ClusterAPI and existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				capiMachineSet = createCAPIMachineSet(ctx, cl, 0, existingCAPIMSAuthorityCAPIName, "m5.large")

				By("Creating a same name MAPI MachineSet")
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, existingCAPIMSAuthorityCAPIName, machinev1beta1.MachineAuthorityClusterAPI, machinev1beta1.MachineAuthorityClusterAPI)
				awsMachineTemplate = waitForAWSMachineTemplate(cl, existingCAPIMSAuthorityCAPIName)

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: ClusterAPI and existing CAPI MachineSet with same name' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*capav1.AWSMachineTemplate{awsMachineTemplate},
						[]*machinev1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should verify that MAPI MachineSet has Paused condition True", func() {
				verifyMAPIPausedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityClusterAPI)
			})

			// bug https://issues.redhat.com/browse/OCPBUGS-55337
			PIt("should verify that the non-authoritative MAPI MachineSet providerSpec has been updated to reflect the authoritative CAPI MachineSet mirror values", func() {
				expectMAPIMachineSetInstanceType(ctx, cl, mapiMSAuthMAPIName, "m5.large")
			})
		})

		Context("with spec.authoritativeAPI: ClusterAPI and no existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthCAPIName, machinev1beta1.MachineAuthorityClusterAPI, machinev1beta1.MachineAuthorityClusterAPI)
				capiMachineSet = waitForCAPIMachineSetMirror(cl, mapiMSAuthCAPIName)
				awsMachineTemplate = waitForAWSMachineTemplate(cl, mapiMSAuthCAPIName)

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: ClusterAPI and no existing CAPI MachineSet with same name' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*capav1.AWSMachineTemplate{awsMachineTemplate},
						[]*machinev1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should find MAPI MachineSet .status.authoritativeAPI to equal CAPI", func() {
				verifyMachineSetAuthoritative(mapiMachineSet, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that MAPI MachineSet Paused condition is True", func() {
				verifyMAPIPausedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that MAPI MachineSet Synchronized condition is True", func() {
				verifySynchronizedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that the non-authoritative MAPI MachineSet has an authoritative CAPI MachineSet mirror", func() {
				waitForCAPIMachineSetMirror(cl, mapiMSAuthCAPIName)
			})

			It("should verify that CAPI MachineSet has Paused condition False", func() {
				verifyCAPIPausedCondition(capiMachineSet, machinev1beta1.MachineAuthorityClusterAPI)
			})
		})
	})

	var _ = Describe("Scale MAPI MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName = "ms-authoritativeapi-mapi"
		var mapiMSAuthCAPIName = "ms-authoritativeapi-capi"
		var mapiMSAuthMAPICAPI = "ms-mapi-machine-capi"

		var awsMachineTemplate *capav1.AWSMachineTemplate
		var capiMachineSet *clusterv1.MachineSet
		var mapiMachineSet *machinev1beta1.MachineSet
		var firstMAPIMachine *machinev1beta1.Machine
		var secondMAPIMachine *machinev1beta1.Machine

		Context("with spec.authoritativeAPI: MachineAPI", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, mapiMSAuthMAPIName, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
				capiMachineSet, awsMachineTemplate = verifyMAPIMachineSetHasCAPIMirror(cl, mapiMSAuthMAPIName)
				mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				capiMachines := capiframework.GetMachinesFromMachineSet(cl, capiMachineSet)
				Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name))
				firstMAPIMachine = mapiMachines[0]

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MachineAPI' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*capav1.AWSMachineTemplate{awsMachineTemplate},
						[]*machinev1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should be able scale MAPI MachineSet to 2 replicas successfully", func() {
				By("Scaling up MAPI MachineSet to 2 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMachineSet.GetName(), 2)).To(Succeed(), "should be able to scale up MAPI MachineSet")
				mapiframework.WaitForMachineSet(ctx, cl, mapiMSAuthMAPIName)
				verifyMAPIMachinesetReplicas(mapiMachineSet, 2)

				By("Verifying a new MAPI Machine is created and Paused condition is False")
				var err error
				secondMAPIMachine, err = mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineRunning(cl, secondMAPIMachine.Name, machinev1beta1.MachineAuthorityMachineAPI)
				verifyMachineAuthoritative(secondMAPIMachine, machinev1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachinePausedCondition(secondMAPIMachine, machinev1beta1.MachineAuthorityMachineAPI)

				By("Verifying there is a non-authoritative CAPI Machine mirror for the MAPI Machine and its Paused condition is True")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
				verifyCAPIMachinePausedCondition(capiMachine, machinev1beta1.MachineAuthorityMachineAPI)
			})

			It("should succeed switching MAPI MachineSet AuthoritativeAPI to ClusterAPI", func() {
				updateMachineSetAuthoritativeAPI(mapiMachineSet, machinev1beta1.MachineAuthorityClusterAPI, machinev1beta1.MachineAuthorityClusterAPI)
				verifySynchronizedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityClusterAPI)
				verifyMAPIPausedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityClusterAPI)
				verifyCAPIPausedCondition(capiMachineSet, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should succeed scaling up CAPI MachineSet to 3, after the switch of AuthoritativeAPI to ClusterAPI", func() {
				By("Scaling up CAPI MachineSet to 3")
				capiframework.ScaleMachineSet(mapiMSAuthMAPIName, 3, capiframework.CAPINamespace)

				By("Verifying a new CAPI Machine is running and Paused condition is False")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
				verifyMachineRunning(cl, capiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
				verifyCAPIMachinePausedCondition(capiMachine, machinev1beta1.MachineAuthorityClusterAPI)

				By("Verifying there is a non-authoritative, paused MAPI Machine mirror for the new CAPI Machine")
				mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineAuthoritative(mapiMachine, machinev1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachinePausedCondition(mapiMachine, machinev1beta1.MachineAuthorityClusterAPI)

				By("Verifying old Machines still exist and authority on them is still MachineAPI")
				verifyMachineAuthoritative(firstMAPIMachine, machinev1beta1.MachineAuthorityMachineAPI)
				verifyMachineAuthoritative(secondMAPIMachine, machinev1beta1.MachineAuthorityMachineAPI)
			})

			It("should succeed scaling down CAPI MachineSet to 1, after the switch of AuthoritativeAPI to ClusterAPI", func() {
				By("Scaling up CAPI MachineSet to 1")
				capiframework.ScaleMachineSet(mapiMSAuthMAPIName, 1, capiframework.CAPINamespace)

				By("Verifying both CAPI MachineSet and its MAPI MachineSet mirror are scaled down to 1")
				// waiting for https://github.com/openshift/cluster-capi-operator/pull/329 gets merged
				//verifyCAPIMachinesetReplicas(capiMachineSet, 1)
				//verifyMAPIMachinesetReplicas(mapiMachineSet, 1)
			})

			It("should succeed in switching back the AuthoritativeAPI to MachineAPI after the initial switch to ClusterAPI", func() {
				updateMachineSetAuthoritativeAPI(mapiMachineSet, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
				verifySynchronizedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityMachineAPI)
				verifyMAPIPausedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityMachineAPI)
				verifyCAPIPausedCondition(capiMachineSet, machinev1beta1.MachineAuthorityMachineAPI)
			})

			It("should delete both MAPI and CAPI MachineSets/Machines and InfraMachineTemplate when deleting MAPI MachineSet", func() {
				Expect(mapiframework.DeleteMachineSets(cl, mapiMachineSet)).To(Succeed(), "Should be able to delete test MachineSet")
				capiframework.WaitForMachineSetsDeleted(cl, capiMachineSet)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
				verifyAWSMachineTemplateDeleted(awsMachineTemplate.Name)
			})
		})

		Context("with spec.authoritativeAPI: ClusterAPI", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, mapiMSAuthCAPIName, machinev1beta1.MachineAuthorityClusterAPI, machinev1beta1.MachineAuthorityClusterAPI)
				capiMachineSet, awsMachineTemplate = verifyMAPIMachineSetHasCAPIMirror(cl, mapiMSAuthCAPIName)
				mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				capiMachines := capiframework.GetMachinesFromMachineSet(cl, capiMachineSet)
				Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name))
				firstMAPIMachine = mapiMachines[0]

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: ClusterAPI' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*capav1.AWSMachineTemplate{awsMachineTemplate},
						[]*machinev1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should succeed scaling CAPI MachineSet to 2 replicas", func() {
				By("Scaling up CAPI MachineSet to 2 replicas")
				capiframework.ScaleMachineSet(mapiMSAuthCAPIName, 2, capiframework.CAPINamespace)
				capiMachineSet := capiframework.GetMachineSet(cl, mapiMSAuthCAPIName, capiframework.CAPINamespace)
				// waiting for https://github.com/openshift/cluster-capi-operator/pull/329 gets merged
				//verifyMAPIMachinesetReplicas(mapiMachineSet, 2)

				By("Verifying a new CAPI Machine is created and Paused condition is False")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
				verifyMachineRunning(cl, capiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
				verifyCAPIMachinePausedCondition(capiMachine, machinev1beta1.MachineAuthorityClusterAPI)

				By("Verifying there is a non-authoritative, paused MAPI Machine mirror for the new CAPI Machine")
				var err error
				secondMAPIMachine, err = mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineAuthoritative(secondMAPIMachine, machinev1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachinePausedCondition(secondMAPIMachine, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should succeed switching MachineSet's AuthoritativeAPI to MachineAPI", func() {
				updateMachineSetAuthoritativeAPI(mapiMachineSet, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
				verifySynchronizedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityMachineAPI)
				verifyMAPIPausedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityMachineAPI)
				verifyCAPIPausedCondition(capiMachineSet, machinev1beta1.MachineAuthorityMachineAPI)
			})

			It("should succeed scaling up MAPI MachineSet to 3, after switching AuthoritativeAPI to MachineAPI", func() {
				By("Scaling up MAPI MachineSet to 3 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMSAuthCAPIName, 3)).To(Succeed(), "should be able to scale up MAPI MachineSet")
				// waiting for https://github.com/openshift/cluster-capi-operator/pull/329 gets merged
				//verifyMAPIMachinesetReplicas(mapiMachineSet, 3)

				By("Verifying the newly requested MAPI Machine has been created and its status.authoritativeAPI is MachineAPI and its Paused condition is False")
				mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineRunning(cl, mapiMachine.Name, machinev1beta1.MachineAuthorityMachineAPI)
				verifyMachineAuthoritative(mapiMachine, machinev1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachinePausedCondition(mapiMachine, machinev1beta1.MachineAuthorityMachineAPI)

				By("Verifying there is a non-authoritative, paused CAPI Machine mirror for the new MAPI Machine")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
				verifyCAPIMachinePausedCondition(capiMachine, machinev1beta1.MachineAuthorityMachineAPI)

				By("Verifying old Machines still exist and authority on them is still ClusterAPI")
				verifyMachineAuthoritative(firstMAPIMachine, machinev1beta1.MachineAuthorityClusterAPI)
				verifyMachineAuthoritative(secondMAPIMachine, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should succeed scaling down MAPI MachineSet to 1, after the switch of AuthoritativeAPI to MachineAPI", func() {
				By("Scaling down MAPI MachineSet to 1 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMSAuthCAPIName, 1)).To(Succeed(), "should be able to scale down MAPI MachineSet")
				// waiting for https://github.com/openshift/cluster-capi-operator/pull/329 gets merged
				//verifyMAPIMachinesetReplicas(mapiMachineSet, 1)
			})

			It("should succeed switching back MachineSet's AuthoritativeAPI to ClusterAPI, after the initial switch to AuthoritativeAPI: MachineAPI", func() {
				updateMachineSetAuthoritativeAPI(mapiMachineSet, machinev1beta1.MachineAuthorityClusterAPI, machinev1beta1.MachineAuthorityClusterAPI)
				verifySynchronizedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityClusterAPI)
				verifyMAPIPausedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityClusterAPI)
				verifyCAPIPausedCondition(capiMachineSet, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should delete both MAPI and CAPI MachineSets/Machines and InfraMachineTemplate when deleting CAPI MachineSet", func() {
				capiframework.DeleteMachineSets(cl, capiMachineSet)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
				capiframework.WaitForMachineSetsDeleted(cl, capiMachineSet)
				verifyAWSMachineTemplateDeleted(awsMachineTemplate.Name)
			})
		})

		Context("with spec.authoritativeAPI: MachineAPI, spec.template.spec.authoritativeAPI: ClusterAPI", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthMAPICAPI, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityClusterAPI)
				capiMachineSet, awsMachineTemplate = verifyMAPIMachineSetHasCAPIMirror(cl, mapiMSAuthMAPICAPI)

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MachineAPI, spec.template.spec.authoritativeAPI: ClusterAPI' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*capav1.AWSMachineTemplate{awsMachineTemplate},
						[]*machinev1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should create an authoritative CAPI Machine when scaling MAPI MachineSet to 1 replicas", func() {
				By("Scaling up MAPI MachineSet to 1 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMachineSet.GetName(), 1)).To(Succeed(), "should be able to scale up MAPI MachineSet")
				capiframework.WaitForMachineSet(cl, mapiMSAuthMAPICAPI, capiframework.CAPINamespace)
				// waiting for https://github.com/openshift/cluster-capi-operator/pull/329 gets merged
				//verifyMAPIMachinesetReplicas(mapiMachineSet, 1)

				By("Verifying MAPI Machine is created and .status.authoritativeAPI to equal CAPI")
				mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineAuthoritative(mapiMachine, machinev1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachinePausedCondition(mapiMachine, machinev1beta1.MachineAuthorityClusterAPI)

				By("Verifying CAPI Machine is created and Paused condition is False and provisions a running Machine")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
				verifyMachineRunning(cl, capiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
				verifyCAPIMachinePausedCondition(capiMachine, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should delete both MAPI and CAPI MachineSets/Machines and InfraMachineTemplate when deleting MAPI MachineSet", func() {
				Expect(mapiframework.DeleteMachineSets(cl, mapiMachineSet)).To(Succeed(), "Should be able to delete test MachineSet")
				capiframework.WaitForMachineSetsDeleted(cl, capiMachineSet)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
				verifyAWSMachineTemplateDeleted(awsMachineTemplate.Name)
			})
		})
	})

	var _ = Describe("Delete MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName = "ms-authoritativeapi-mapi"
		var mapiMachineSet *machinev1beta1.MachineSet
		var capiMachineSet *clusterv1.MachineSet
		var awsMachineTemplate *capav1.AWSMachineTemplate

		Context("when removing non-authoritative MAPI MachineSet", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, mapiMSAuthMAPIName, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
				capiMachineSet, awsMachineTemplate = verifyMAPIMachineSetHasCAPIMirror(cl, mapiMSAuthMAPIName)
				mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				capiMachines := capiframework.GetMachinesFromMachineSet(cl, capiMachineSet)
				Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name))

				DeferCleanup(func() {
					By("Cleaning up Context 'when removing non-authoritative MAPI MachineSet' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*capav1.AWSMachineTemplate{awsMachineTemplate},
						[]*machinev1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("shouldn't delete its authoritative CAPI MachineSet", func() {
				By("Switching AuthoritativeAPI to ClusterAPI")
				updateMachineSetAuthoritativeAPI(mapiMachineSet, machinev1beta1.MachineAuthorityClusterAPI, machinev1beta1.MachineAuthorityClusterAPI)

				By("Scaling up CAPI MachineSet to 2 replicas")
				capiframework.ScaleMachineSet(mapiMachineSet.GetName(), 2, capiframework.CAPINamespace)
				// waiting for https://github.com/openshift/cluster-capi-operator/pull/329 gets merged
				//verifyMAPIMachinesetReplicas(mapiMachineSet, 2)

				By("Verifying new CAPI Machine is running")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
				verifyMachineRunning(cl, capiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
				verifyCAPIMachinePausedCondition(capiMachine, machinev1beta1.MachineAuthorityClusterAPI)

				By("Verifying there is a non-authoritative, paused MAPI Machine mirror for the new CAPI Machine")
				mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineAuthoritative(mapiMachine, machinev1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachinePausedCondition(mapiMachine, machinev1beta1.MachineAuthorityClusterAPI)

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

	var _ = Describe("Update MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName = "ms-authoritativeapi-mapi"
		var mapiMachineSet *machinev1beta1.MachineSet
		var capiMachineSet *clusterv1.MachineSet
		var awsMachineTemplate *capav1.AWSMachineTemplate
		var newAWSMachineTemplate *capav1.AWSMachineTemplate

		BeforeAll(func() {
			mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthMAPIName, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
			capiMachineSet, awsMachineTemplate = verifyMAPIMachineSetHasCAPIMirror(cl, mapiMSAuthMAPIName)

			DeferCleanup(func() {
				By("Cleaning up 'Update MachineSet' resources")
				cleanupTestResources(
					ctx,
					cl,
					[]*clusterv1.MachineSet{capiMachineSet},
					[]*capav1.AWSMachineTemplate{awsMachineTemplate, newAWSMachineTemplate},
					[]*machinev1beta1.MachineSet{mapiMachineSet},
				)
			})
		})

		Context("when MAPI MachineSet with spec.authoritativeAPI: MachineAPI and replicas 0", Ordered, func() {
			It("should reject update when attempting scaling of the CAPI MachineSet mirror", func() {
				By("Scaling up CAPI MachineSet to 1 should be rejected")
				capiframework.ScaleMachineSet(mapiMSAuthMAPIName, 1, capiframework.CAPINamespace)
				capiMachineSet = capiframework.GetMachineSet(cl, mapiMSAuthMAPIName, capiframework.CAPINamespace)
				// verifyCAPIMachinesetReplicas(capiMachineSet, 0)
			})

			It("should reject update when attempting to change the spec of the CAPI MachineSet mirror", func() {
				By("Updating CAPI mirror spec (such as DeletePolicy)")
				Eventually(k.Update(capiMachineSet, func() {
					capiMachineSet.Spec.DeletePolicy = "Oldest"
				}), capiframework.WaitMedium, capiframework.RetryShort).Should(Succeed(), "Failed to update CAPI MachineSet DeletePolicy")

				By("Verifying both MAPI and CAPI MachineSet spec value are restored to original value")
				Eventually(k.Object(mapiMachineSet), capiframework.WaitShort, capiframework.RetryShort).Should(HaveField("Spec.DeletePolicy", SatisfyAny(BeEmpty(), Equal("Random"))), "DeletePolicy should be either empty or 'Random'")
				Eventually(k.Object(capiMachineSet), capiframework.WaitShort, capiframework.RetryShort).Should(HaveField("Spec.DeletePolicy", HaveValue(Equal("Random"))), "DeletePolicy should be 'Random'")
			})

			It("should create a new InfraTemplate when update MAPI MachineSet providerSpec", func() {
				By("Updating MAPI MachineSet providerSpec InstanceType to m5.large")
				newInstanceType := "m5.large"
				//updateMAPIMachineSetInstanceType(ctx, cl, mapiMachineSet, newInstanceType)

				updateAWSMachineSetProviderSpec(mapiMachineSet, func(providerSpec *machinev1beta1.AWSMachineProviderConfig) {
					providerSpec.InstanceType = newInstanceType
				})

				By("Waiting for new InfraTemplate to be created")
				originalAWSMachineTemplateName := capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name
				capiMachineSet = capiframework.GetMachineSet(cl, mapiMSAuthMAPIName, capiframework.CAPINamespace)
				Eventually(k.Object(capiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(HaveField("spec.template.spec.infrastructureref.name", Not(HaveValue(Equal(originalAWSMachineTemplateName)))), "InfraTemplate name should be changed")

				By("Verifying new InfraTemplate has the updated InstanceType")
				newAWSMachineTemplate, err := capiframework.GetAWSMachineTemplateByPrefix(cl, mapiMSAuthMAPIName, capiframework.CAPINamespace)
				Expect(err).ToNot(HaveOccurred(), "Failed to get new awsMachineTemplate  %s", newAWSMachineTemplate)
				Expect(newAWSMachineTemplate.Spec.Template.Spec.InstanceType).To(Equal(newInstanceType))

				By("Verifying the old InfraTemplate is deleted")
				verifyAWSMachineTemplateDeleted(originalAWSMachineTemplateName)
			})
		})

		Context("when switching MAPI MachineSet spec.authoritativeAPI to ClusterAPI", Ordered, func() {
			BeforeAll(func() {
				updateMachineSetAuthoritativeAPI(mapiMachineSet, machinev1beta1.MachineAuthorityClusterAPI, machinev1beta1.MachineAuthorityClusterAPI)
				verifySynchronizedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should be rejected when scaling MAPI MachineSet", func() {
				By("Scaling up MAPI MachineSet to 1")
				mapiframework.ScaleMachineSet(mapiMSAuthMAPIName, 1)

				By("Verifying MAPI MachineSet replicas is restored to original value 0")
				// waiting for https://github.com/openshift/cluster-capi-operator/pull/329 gets merged
				//verifyMAPIMachinesetReplicas(mapiMachineSet, 0)
			})

			It("should be rejected when when updating providerSpec of MAPI MachineSet", func() {
				By("Getting the current MAPI MachineSet providerSpec InstanceType")
				originalInstanceType := getMAPIMachineSetInstanceType(ctx, cl, mapiMSAuthMAPIName)

				By("Updating the MAPI MachineSet providerSpec InstanceType")
				//updateMAPIMachineSetInstanceType(ctx, cl, mapiMachineSet, "m5.xlarge")
				updateAWSMachineSetProviderSpec(mapiMachineSet, func(providerSpec *machinev1beta1.AWSMachineProviderConfig) {
					providerSpec.InstanceType = "m5.xlarge"
				})

				By("Verifying MAPI MachineSet InstanceType is restored to original value")
				expectMAPIMachineSetInstanceType(ctx, cl, mapiMSAuthMAPIName, originalInstanceType)
			})

			It("should update MAPI MachineSet and remove old InfraTemplate when CAPI MachineSet points to new InfraTemplate", func() {
				By("Creating a new awsMachineTemplate with different spec")
				newInstanceType := "m6.xlarge"
				originalAWSMachineTemplateName := capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name
				newAWSMachineTemplate = createAWSMachineTemplateWithInstanceType(ctx, cl, originalAWSMachineTemplateName, newInstanceType)

				By("Updating CAPI MachineSet to point to the new InfraTemplate")
				updateCAPIMachineSetInfraTemplate(capiMachineSet, newAWSMachineTemplate.Name)

				By("Verifying the old InfraTemplate is deleted")
				// bug https://issues.redhat.com/browse/OCPBUGS-61103
				//verifyAWSMachineTemplateDeleted(originalAWSMachineTemplateName)

				By("Verifying the MAPI MachineSet is updated to reflect the new template")
				mapiMachineSet, _ = mapiframework.GetMachineSet(ctx, cl, mapiMSAuthMAPIName)
				/*
				   Eventually(k.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
				   	HaveField("Spec.Template.Spec.ProviderSpec.Value", gbytes.WithJSON(HaveField("InstanceType", Equal(newInstanceType)))),
				   	"MAPI MachineSet providerSpec should be updated to reflect the new InfraTemplate with InstanceType %s", newInstanceType,
				   )*/
				Eventually(k.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Spec.Template.Spec.ProviderSpec.Value.Raw", ContainSubstring(newInstanceType)),
					"MAPI MachineSet providerSpec should be updated to reflect the new InfraTemplate with InstanceType %s", newInstanceType,
				)
				/*
					Eventually(func() string {
						mapiMachineSet, _ = mapiframework.GetMachineSet(ctx, cl, mapiMSAuthMAPIName)
						return string(mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw)
					}, capiframework.WaitMedium, capiframework.RetryMedium).Should(ContainSubstring(newInstanceType), "MAPI MachineSet providerSpec should be updated to reflect the new InfraTemplate with InstanceType %s", newInstanceType)
				*/
			})
		})
	})
})

func createMAPIMachineSetWithAuthoritativeAPI(ctx context.Context, cl client.Client, replicas int, machineSetName string, machineSetAuthority machinev1beta1.MachineAuthority, machineAuthority machinev1beta1.MachineAuthority) *machinev1beta1.MachineSet {
	By(fmt.Sprintf("Creating MAPI MachineSet with spec.authoritativeAPI: %s, spec.template.spec.authoritativeAPI: %s, replicas=%d", machineSetAuthority, machineAuthority, replicas))
	machineSetParams := mapiframework.BuildMachineSetParams(ctx, cl, replicas)
	machineSetParams.Name = machineSetName
	machineSetParams.MachinesetAuthoritativeAPI = machineSetAuthority
	machineSetParams.MachineAuthoritativeAPI = machineAuthority
	// Now CAPI machineSet doesn't support taint, remove it. card https://issues.redhat.com/browse/OCPCLOUD-2861
	machineSetParams.Taints = []corev1.Taint{}
	mapiMachineSet, err := mapiframework.CreateMachineSet(cl, machineSetParams)
	Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet %s creation should succeed", machineSetName)

	capiMachineSet := &clusterv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineSetName,
			Namespace: capiframework.CAPINamespace,
		},
	}
	Eventually(komega.Get(capiMachineSet), capiframework.WaitShort, capiframework.RetryShort).Should(
		Succeed(), "Mirror CAPI MachineSet should be created within 1 minute")

	switch machineAuthority {
	case machinev1beta1.MachineAuthorityMachineAPI:
		mapiframework.WaitForMachineSet(ctx, cl, machineSetName)
	case machinev1beta1.MachineAuthorityClusterAPI:
		capiframework.WaitForMachineSet(cl, machineSetName, capiframework.CAPINamespace)
	}
	return mapiMachineSet
}

func createCAPIMachineSet(ctx context.Context, cl client.Client, replicas int32, machineSetName string, instanceType string) *clusterv1.MachineSet {
	By(fmt.Sprintf("Creating CAPI MachineSet %s with %d replicas", machineSetName, replicas))

	_, mapiDefaultProviderSpec := getDefaultAWSMAPIProviderSpec(cl)
	createAWSClient(mapiDefaultProviderSpec.Placement.Region)
	awsMachineTemplate := newAWSMachineTemplate(mapiDefaultProviderSpec)
	awsMachineTemplate.Name = machineSetName
	if instanceType != "" {
		awsMachineTemplate.Spec.Template.Spec.InstanceType = instanceType
	}

	Eventually(cl.Create(ctx, awsMachineTemplate), capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Failed to create a new awsMachineTemplate %s", awsMachineTemplate.Name)

	machineSet := capiframework.CreateMachineSet(cl, capiframework.NewMachineSetParams(
		machineSetName,
		clusterName,
		"",
		replicas,
		corev1.ObjectReference{
			Kind:       "AWSMachineTemplate",
			APIVersion: infraAPIVersion,
			Name:       machineSetName,
		},
		"worker-user-data",
	))

	capiframework.WaitForMachineSet(cl, machineSet.Name, machineSet.Namespace)
	return machineSet
}

func verifySynchronizedCondition(mapiMachineSet *machinev1beta1.MachineSet, authority machinev1beta1.MachineAuthority) {
	By("Verify the MAPI MachineSet Synchronized condition is True")
	var expectedMessage string

	switch authority {
	case machinev1beta1.MachineAuthorityMachineAPI:
		expectedMessage = "Successfully synchronized MAPI MachineSet to CAPI"
	case machinev1beta1.MachineAuthorityClusterAPI:
		expectedMessage = "Successfully synchronized CAPI MachineSet to MAPI"
	default:
		Fail(fmt.Sprintf("unknown authoritativeAPI type: %v", authority))
	}

	Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		WithTransform(
			func(ms *machinev1beta1.MachineSet) []machinev1beta1.Condition {
				return ms.Status.Conditions
			},
			ContainElement(
				SatisfyAll(
					HaveField("Type", Equal(SynchronizedCondition)),
					HaveField("Status", Equal(corev1.ConditionTrue)),
					HaveField("Reason", Equal("ResourceSynchronized")),
					HaveField("Message", Equal(expectedMessage)),
				),
			),
		),
		fmt.Sprintf("Expected Synchronized condition for %s not found or incorrect", authority),
	)
}

func verifyMachineSetAuthoritative(mapiMachineSet *machinev1beta1.MachineSet, authority machinev1beta1.MachineAuthority) {
	By(fmt.Sprintf("Verify the MachineSet authoritative is %s", authority))
	Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		HaveField("Status.AuthoritativeAPI", Equal(authority)),
		fmt.Sprintf("Expected MachineSet with correct status.AuthoritativeAPI %s", authority),
	)
}

func verifyMAPIPausedCondition(mapiMachineSet *machinev1beta1.MachineSet, authority machinev1beta1.MachineAuthority) {
	var conditionMatcher types.GomegaMatcher

	switch authority {
	case machinev1beta1.MachineAuthorityMachineAPI:
		By("Verifying MAPI MachineSet is unpaused")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(MAPIPausedCondition)),
			HaveField("Status", Equal(corev1.ConditionFalse)),
			HaveField("Reason", Equal("AuthoritativeAPIMachineAPI")),
			HaveField("Message", ContainSubstring("MachineAPI")),
		)
	case machinev1beta1.MachineAuthorityClusterAPI:
		By("Verifying MAPI MachineSet is paused")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(MAPIPausedCondition)),
			HaveField("Status", Equal(corev1.ConditionTrue)),
			HaveField("Reason", Equal("AuthoritativeAPINotMachineAPI")),
			HaveField("Message", ContainSubstring("ClusterAPI")),
		)
	default:
		Fail(fmt.Sprintf("unknown authoritativeAPI type: %v", authority))
	}

	Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		HaveField("Status.Conditions", ContainElement(conditionMatcher)),
		fmt.Sprintf("Expected MAPI MachineSet with correct paused condition for %s", authority),
	)
}

func verifyCAPIPausedCondition(capiMachineSet *clusterv1.MachineSet, authority machinev1beta1.MachineAuthority) {
	var conditionMatcher types.GomegaMatcher

	switch authority {
	case machinev1beta1.MachineAuthorityClusterAPI:
		By("Verifying CAPI MachineSet is unpaused (ClusterAPI is authoritative)")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(CAPIPausedCondition)),
			HaveField("Status", Equal(metav1.ConditionFalse)),
			HaveField("Reason", Equal("NotPaused")),
		)
	case machinev1beta1.MachineAuthorityMachineAPI:
		By("Verifying CAPI MachineSet is paused (MachineAPI is authoritative)")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(CAPIPausedCondition)),
			HaveField("Status", Equal(metav1.ConditionTrue)),
			HaveField("Reason", Equal("Paused")),
		)
	default:
		Fail(fmt.Sprintf("unknown authoritativeAPI type: %v", authority))
	}

	Eventually(komega.Object(capiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		HaveField("Status.V1Beta2.Conditions", ContainElement(conditionMatcher)),
		fmt.Sprintf("Expected CAPI MachineSet with correct paused condition for %s", authority),
	)
}

func verifyMAPIMachineSetHasCAPIMirror(cl client.Client, machineSetNameMAPI string) (*clusterv1.MachineSet, *capav1.AWSMachineTemplate) {
	By("Check MAPI MachineSet has a CAPI MachineSet mirror")
	var err error
	var capiMachineSet *clusterv1.MachineSet
	var awsMachineTemplate *capav1.AWSMachineTemplate

	Eventually(func() error {
		capiMachineSet = capiframework.GetMachineSet(cl, machineSetNameMAPI, capiframework.CAPINamespace)
		if capiMachineSet == nil {
			return fmt.Errorf("CAPI MachineSet %s/%s not found", capiframework.CAPINamespace, machineSetNameMAPI)
		}
		return nil
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "CAPI MachineSet should exist")

	Eventually(func() error {
		awsMachineTemplate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, machineSetNameMAPI, capiframework.CAPINamespace)
		return err
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "awsMachineTemplate should exist")

	return capiMachineSet, awsMachineTemplate
}

func verifyMAPIMachinesetReplicas(mapiMachineSet *machinev1beta1.MachineSet, replicas int) {
	By(fmt.Sprintf("Verify MAPI MachineSet status.Replicas is %d", replicas))
	Eventually(komega.Object(mapiMachineSet), capiframework.WaitLong, capiframework.RetryLong).Should(
		HaveField("Status.Replicas", HaveValue(Equal(int32(replicas)))),
		"MAPI MachineSet %q replicas status should eventually be %d", mapiMachineSet.Name, replicas)
}

func verifyCAPIMachinesetReplicas(capiMachineSet *clusterv1.MachineSet, replicas int) {
	By(fmt.Sprintf("Verify CAPI MachineSet status.Replicas is %d", replicas))
	Eventually(komega.Object(capiMachineSet), capiframework.WaitLong, capiframework.RetryLong).Should(
		HaveField("Status.Replicas", HaveValue(Equal(int32(replicas)))),
		"CAPI MachineSet %q replicas status should eventually be %d", capiMachineSet.Name, replicas)
}

func updateMachineSetAuthoritativeAPI(mapiMachineSet *machinev1beta1.MachineSet, machineSetAuthority machinev1beta1.MachineAuthority, machineAuthority machinev1beta1.MachineAuthority) {
	By(fmt.Sprintf("Update MachineSet %s AuthoritativeAPI to spec.authoritativeAPI: %s, spec.template.spec.authoritativeAPI: %s", mapiMachineSet.Name, machineSetAuthority, machineAuthority))
	Eventually(komega.Update(mapiMachineSet, func() {
		mapiMachineSet.Spec.AuthoritativeAPI = machineSetAuthority
		mapiMachineSet.Spec.Template.Spec.AuthoritativeAPI = machineAuthority
	}), capiframework.WaitShort, capiframework.RetryShort).Should(Succeed())
}

func updateMAPIMachineSetInstanceType(ctx context.Context, cl client.Client, mapiMachineSet *machinev1beta1.MachineSet, newInstanceType string) error {
	By(fmt.Sprintf("Update MachineSet %s instanceType to %s", mapiMachineSet.Name, newInstanceType))
	patch := client.MergeFrom(mapiMachineSet.DeepCopy())

	var awsProviderSpec machinev1beta1.AWSMachineProviderConfig
	Expect(json.Unmarshal(mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, &awsProviderSpec)).Should(Succeed(), "failed to unmarshal provider spec")

	awsProviderSpec.InstanceType = newInstanceType
	updatedSpec, err := json.Marshal(awsProviderSpec)
	Expect(err).ToNot(HaveOccurred(), "failed to unmarshal provider spec")

	mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw = updatedSpec
	return cl.Patch(ctx, mapiMachineSet, patch)
}

func getAWSProviderSpecFromMachineSet(machineSet *machinev1beta1.MachineSet) (*machinev1beta1.AWSMachineProviderConfig, error) {
	if machineSet.Spec.Template.Spec.ProviderSpec.Value == nil {
		return nil, fmt.Errorf("provider spec value is nil")
	}

	// Use the existing AWSProviderSpecFromRawExtension function to avoid code duplication
	providerSpec, err := mapi2capi.AWSProviderSpecFromRawExtension(machineSet.Spec.Template.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to extract AWS provider spec: %w", err)
	}

	return &providerSpec, nil
}

func updateAWSMachineSetProviderSpec(mapiMachineSet *machinev1beta1.MachineSet, updateFunc func(*machinev1beta1.AWSMachineProviderConfig)) {
	By(fmt.Sprintf("Update MachineSet %s providerSpec to %s", mapiMachineSet.Name, updateFunc))
	providerSpec, err := getAWSProviderSpecFromMachineSet(mapiMachineSet)
	Expect(err).ToNot(HaveOccurred(), "failed to get AWS provider spec from MachineSet")

	updateFunc(providerSpec)

	rawProviderSpec, err := json.Marshal(providerSpec)
	Expect(err).ToNot(HaveOccurred(), "failed to marshal updated provider spec")

	mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw = rawProviderSpec
}

func getMAPIMachineSetInstanceType(ctx context.Context, cl client.Client, machineSetName string) string {
	By(fmt.Sprintf("Get MachineSet %s instanceType", machineSetName))
	mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machineSetName)
	Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet")

	var awsConfig machinev1beta1.AWSMachineProviderConfig
	err = json.Unmarshal(mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, &awsConfig)
	Expect(err).ToNot(HaveOccurred(), "failed to unmarshal provider spec")
	Expect(json.Unmarshal(mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, &awsConfig)).To(Succeed(), "failed to unmarshal provider spec")
	return awsConfig.InstanceType
}

func expectMAPIMachineSetInstanceType(ctx context.Context, cl client.Client, machineSetName string, expectedInstanceType string) {
	By(fmt.Sprintf("Verifying MAPI MachineSet %s has instanceType = %s", machineSetName, expectedInstanceType))

	Eventually(func() string {
		return getMAPIMachineSetInstanceType(ctx, cl, machineSetName)
	}, capiframework.WaitMedium, capiframework.RetryShort).Should(
		Equal(expectedInstanceType),
		"MachineSet %s providerSpec.instanceType should be %s",
		machineSetName, expectedInstanceType,
	)
}

func waitForCAPIMachineSetMirror(cl client.Client, machineName string) *clusterv1.MachineSet {
	By(fmt.Sprintf("Verify there is a CAPI MachineSer mirror for  MAPI MachineSet %s", machineName))
	var capiMachineSet *clusterv1.MachineSet
	Eventually(func() error {
		capiMachineSet = capiframework.GetMachineSet(cl, machineName, capiframework.CAPINamespace)
		if capiMachineSet == nil {
			return fmt.Errorf("CAPI MachineSet %s/%s not found", capiframework.CAPINamespace, machineName)
		}
		return nil
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "CAPI MachineSet %s/%s should exist", capiframework.CAPINamespace, machineName)
	return capiMachineSet
}

func waitForAWSMachineTemplate(cl client.Client, prefix string) *capav1.AWSMachineTemplate {
	By(fmt.Sprintf("Verify there is an AWSMachineTemplate with prefix %s", prefix))
	var awsMachineTemplate *capav1.AWSMachineTemplate
	Eventually(func() error {
		var err error
		awsMachineTemplate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, prefix, capiframework.CAPINamespace)
		if err != nil {
			return fmt.Errorf("failed to get AWSMachineTemplate with prefix %s in %s: %w", prefix, capiframework.CAPINamespace, err)
		}
		return nil
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(),
		"AWSMachineTemplate with prefix %s should exist", prefix)
	return awsMachineTemplate
}

func createAWSMachineTemplateWithInstanceType(ctx context.Context, cl client.Client, originalName, instanceType string) *capav1.AWSMachineTemplate {
	By(fmt.Sprintf("Creating a new awsMachineTemplate with spec.instanceType=%s", instanceType))

	_, mapiDefaultProviderSpec := getDefaultAWSMAPIProviderSpec(cl)
	createAWSClient(mapiDefaultProviderSpec.Placement.Region)

	newTemplate := newAWSMachineTemplate(mapiDefaultProviderSpec)
	newTemplate.Name = "new-" + originalName
	newTemplate.Spec.Template.Spec.InstanceType = instanceType
	/*
	   	Eventually(func() error {
	   		return cl.Create(ctx, newTemplate)
	   	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(

	   	Succeed(),
	   	"Failed to create a new awsMachineTemplate %s", newTemplate.Name,

	   )
	*/
	Eventually(cl.Create(ctx, newTemplate), capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Failed to create a new awsMachineTemplate %s", newTemplate.Name)

	return newTemplate
}

func updateCAPIMachineSetInfraTemplate(capiMachineSet *clusterv1.MachineSet, newInfraTemplateName string) {
	By(fmt.Sprintf("Updating CAPI MachineSet %s to point to new InfraTemplate %s", capiMachineSet.Name, newInfraTemplateName))

	Eventually(komega.Update(capiMachineSet, func() {
		capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name = newInfraTemplateName
	}), capiframework.WaitShort, capiframework.RetryShort).Should(
		Succeed(),
		"Failed to update CAPI MachineSet %s to point to new InfraTemplate %s",
		capiMachineSet.Name, newInfraTemplateName,
	)
}

func verifyAWSMachineTemplateDeleted(awsMachineTemplateName string) {
	By(fmt.Sprintf("Verifying the AWSMachineTemplate %s is removed", awsMachineTemplateName))
	Eventually(komega.Get(&capav1.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      awsMachineTemplateName,
			Namespace: capiframework.MAPINamespace,
		},
	}), time.Minute).Should(WithTransform(apierrors.IsNotFound, BeTrue()))
}

func cleanupTestResources(ctx context.Context, cl client.Client, capiMachineSets []*clusterv1.MachineSet, awsMachineTemplates []*capav1.AWSMachineTemplate, mapiMachineSets []*machinev1beta1.MachineSet) {
	for _, ms := range capiMachineSets {
		if ms == nil {
			continue
		}
		By(fmt.Sprintf("Deleting CAPI MachineSet %s", ms.Name))
		capiframework.DeleteMachineSets(cl, ms)
		capiframework.WaitForMachineSetsDeleted(cl, ms)
	}

	for _, ms := range mapiMachineSets {
		if ms == nil {
			continue
		}
		By(fmt.Sprintf("Deleting MAPI MachineSet %s", ms.Name))
		mapiframework.DeleteMachineSets(cl, ms)
		mapiframework.WaitForMachineSetsDeleted(ctx, cl, ms)
	}

	for _, template := range awsMachineTemplates {
		if template == nil {
			continue
		}
		By(fmt.Sprintf("Deleting awsMachineTemplate %s", template.Name))
		capiframework.DeleteAWSMachineTemplates(cl, template)
	}
}
