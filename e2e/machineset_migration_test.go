package e2e

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

const (
	SynchronizedCondition mapiv1beta1.ConditionType = "Synchronized"
	MAPIPausedCondition   mapiv1beta1.ConditionType = "Paused"
	CAPIPausedCondition                             = capiv1beta1.PausedV1Beta2Condition
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] MachineSet Migration Tests", Ordered, func() {
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this only support on aws", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	var _ = Describe("Create MAPI MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName = "ms-authoritativeapi-mapi"
		var mapiMSAuthCAPIName = "ms-authoritativeapi-capi"
		var existingCAPIMSAuthorityMAPIName = "capi-machineset-authoritativeapi-mapi"
		var existingCAPIMSAuthorityCAPIName = "capi-machineset-authoritativeapi-capi"

		var awsMachineTemplate *capav1.AWSMachineTemplate
		var capiMachineSet *capiv1beta1.MachineSet
		var mapiMachineSet *mapiv1beta1.MachineSet
		var err error

		Context("with spec.authoritativeAPI: MAPI and existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				capiMachineSet, err = createCAPIMachineSet(ctx, cl, 0, existingCAPIMSAuthorityMAPIName, "")
				Expect(err).ToNot(HaveOccurred(), "CAPI MachineSet %s creation should succeed", capiMachineSet.GetName())

				Eventually(func() error {
					awsMachineTemplate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, existingCAPIMSAuthorityMAPIName, capiframework.CAPINamespace)
					return err
				}, capiframework.WaitMedium, capiframework.RetryShort).Should(Succeed(), "awsMachineTemplate should exist")

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MAPI and existing CAPI MachineSet with same name' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*capiv1beta1.MachineSet{capiMachineSet},
						[]*capav1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{},
					)
				})
			})

			// https://issues.redhat.com/browse/OCPCLOUD-2641
			PIt("should reject creation of MAPI MachineSet with same name as existing CAPI MachineSet", func() {
				By("Creating a same name MAPI MachineSet")
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, existingCAPIMSAuthorityMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				Expect(err).To(HaveOccurred(), "denied request to create MAPI MachineSet %s", mapiMachineSet.GetName())
			})
		})

		Context("with spec.authoritativeAPI: MAPI and when no existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet %s creation should succeed", mapiMachineSet.GetName())

				Eventually(func() error {
					awsMachineTemplate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, mapiMSAuthMAPIName, capiframework.CAPINamespace)
					return err
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "awsMachineTemplate should exist")

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MAPI and when no existing CAPI MachineSet with same name' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*capiv1beta1.MachineSet{},
						[]*capav1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should find MAPI MachineSet .status.authoritativeAPI to equal MAPI", func() {
				Eventually(komega.Object(mapiMachineSet)).Should(HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)))
			})

			It("should verify that MAPI MachineSet Paused condition is False", func() {
				verifyMAPIPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should verify that MAPI MachineSet Synchronized condition is True", func() {
				verifySynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should find that MAPI MachineSet has a CAPI MachineSet mirror", func() {
				Eventually(func() error {
					capiMachineSet, err = capiframework.GetMachineSet(cl, mapiMSAuthMAPIName, capiframework.CAPINamespace)
					return err
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "CAPI MachineSet mirror should exist")
			})

			It("should verify that the mirror CAPI MachineSet has Paused condition True", func() {
				verifyCAPIPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
			})
		})

		Context("with spec.authoritativeAPI: CAPI and existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				capiMachineSet, err = createCAPIMachineSet(ctx, cl, 0, existingCAPIMSAuthorityCAPIName, "m5.large")
				Expect(err).ToNot(HaveOccurred(), "CAPI MachineSet %s creation should succeed", capiMachineSet.GetName())

				By("Creating a same name MAPI MachineSet")
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, existingCAPIMSAuthorityCAPIName, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to create MAPI MachineSet %s", existingCAPIMSAuthorityCAPIName)

				Eventually(func() error {
					awsMachineTemplate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, existingCAPIMSAuthorityCAPIName, capiframework.CAPINamespace)
					return err
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "awsMachineTemplate should exist")

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: CAPI and existing CAPI MachineSet with same name' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*capiv1beta1.MachineSet{capiMachineSet},
						[]*capav1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should verify that MAPI MachineSet has Paused condition True", func() {
				verifyMAPIPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			// bug https://issues.redhat.com/browse/OCPBUGS-55337
			PIt("should verify that the non-authoritative MAPI MachineSet providerSpec has been updated to reflect the authoritative CAPI MachineSet mirror values", func() {
				Eventually(func() string {
					providerSpec := mapiMachineSet.Spec.Template.Spec.ProviderSpec
					var awsConfig mapiv1beta1.AWSMachineProviderConfig
					_ = json.Unmarshal(providerSpec.Value.Raw, &awsConfig)
					return awsConfig.InstanceType
				}, capiframework.WaitMedium, capiframework.RetryShort).Should(Equal("m5.large"), "MAPI MSet Sepc should be updated to reflect existing CAPI mirror")
			})
		})

		Context("with spec.authoritativeAPI: CAPI and no existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthCAPIName, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet %s creation should succeed", mapiMachineSet.GetName())

				Eventually(func() error {
					capiMachineSet, err = capiframework.GetMachineSet(cl, mapiMSAuthCAPIName, capiframework.CAPINamespace)
					return err
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "CAPI MachineSet should exist")

				Eventually(func() error {
					awsMachineTemplate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, mapiMSAuthCAPIName, capiframework.CAPINamespace)
					return err
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "awsMachineTemplate should exist")

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: CAPI and no existing CAPI MachineSet with same name' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*capiv1beta1.MachineSet{capiMachineSet},
						[]*capav1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should find MAPI MachineSet .status.authoritativeAPI to equal CAPI", func() {
				Eventually(komega.Object(mapiMachineSet)).Should(HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)))
			})

			It("should verify that MAPI MachineSet Paused condition is True", func() {
				verifyMAPIPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that MAPI MachineSet Synchronized condition is True", func() {
				verifySynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that the non-authoritative MAPI MachineSet has an authoritative CAPI MachineSet mirror", func() {
				Eventually(func() error {
					capiMachineSet, err = capiframework.GetMachineSet(cl, mapiMSAuthCAPIName, capiframework.CAPINamespace)
					return err
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "CAPI MachineSet should exist")
			})

			It("should verify that CAPI MachineSet has Paused condition False", func() {
				verifyCAPIPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})
		})
	})

	var _ = Describe("Scale MAPI MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName = "ms-authoritativeapi-mapi"
		var mapiMSAuthCAPIName = "ms-authoritativeapi-capi"
		var mapiMSAuthMAPICAPI = "ms-mapi-machine-capi"

		var awsMachineTemplate *capav1.AWSMachineTemplate
		var capiMachineSet *capiv1beta1.MachineSet
		var mapiMachineSet *mapiv1beta1.MachineSet
		var firstMAPIMachine *mapiv1beta1.Machine
		var secondMAPIMachine *mapiv1beta1.Machine
		var err error

		Context("with spec.authoritativeAPI: MAPI", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, mapiMSAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet creation should succeed")

				capiMachineSet, awsMachineTemplate = verifyMAPIMachineSetHasCAPIMirror(cl, mapiMSAuthMAPIName)

				mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				capiMachines, err := capiframework.GetMachinesFromMachineSet(cl, capiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get CAPI Machines from MachineSet")
				Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name))
				firstMAPIMachine = mapiMachines[0]

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MAPI' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*capiv1beta1.MachineSet{capiMachineSet},
						[]*capav1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should scale MAPI MachineSet to 2 replicas succeed", func() {
				By("Scaling up MAPI MachineSet to 2 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMachineSet.GetName(), 2)).To(Succeed(), "should be able to scale up MAPI MachineSet")
				mapiframework.WaitForMachineSet(ctx, cl, mapiMSAuthMAPIName)
				Eventually(komega.Object(mapiMachineSet)).Should(HaveField("Spec.Replicas", HaveValue(Equal(int32(2)))))

				By("Verifying a new MAPI Machine is created and Paused condition is False")
				Eventually(func() (*mapiv1beta1.Machine, error) {
					secondMAPIMachine, err = mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
					if err != nil {
						return nil, err
					}
					return secondMAPIMachine, nil
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(SatisfyAll(
					HaveField("Status.Phase", HaveValue(Equal(string(mapiv1beta1.PhaseRunning)))),
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", Equal(MAPIPausedCondition)),
						HaveField("Status", Equal(corev1.ConditionFalse)),
					))),
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)),
				))

				By("Verifying there is a mirrored CAPI Machine Paused condition is True")
				Eventually(func() (*capiv1beta1.Machine, error) {
					capiMachine, err := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
					if err != nil {
						return nil, corev1.ErrIntOverflowGenerated
					}
					return capiMachine, nil
				}, capiframework.WaitLong, capiframework.RetryLong).Should(SatisfyAll(
					HaveField("Status.V1Beta2.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", Equal(CAPIPausedCondition)),
						HaveField("Status", Equal(metav1.ConditionTrue)),
						HaveField("Reason", Equal("Paused")),
					))),
				))
			})

			It("should switch AuthoritativeAPI to ClusterAPI succeed", func() {
				updateMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				verifySynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyCAPIPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should scale up CAPI MachineSet to 3 succeed after switching AuthoritativeAPI to ClusterAPI", func() {
				By("Scaling up CAPI MachineSet to 3")
				Expect(capiframework.ScaleMachineSet(mapiMSAuthMAPIName, 3, capiframework.CAPINamespace)).To(Succeed(), "should be able to scale up CAPI MachineSet")

				By("Verifying a new CAPI Machine is running and Paused condition is False")
				Eventually(func() (*capiv1beta1.Machine, error) {
					capiMachine, err := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
					if err != nil {
						return nil, err
					}
					return capiMachine, nil
				}, capiframework.WaitLong, capiframework.RetryLong).Should(SatisfyAll(
					HaveField("Status.Phase", HaveValue(Equal(string(capiv1beta1.MachinePhaseRunning)))),
					HaveField("Status.V1Beta2.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", Equal(CAPIPausedCondition)),
						HaveField("Status", Equal(metav1.ConditionFalse)),
						HaveField("Reason", Equal("NotPaused")),
					))),
				))

				By("Verifying there is a non-authoritative, paused MAPI Machine mirror for the new CAPI Machine")
				Eventually(func() (*mapiv1beta1.Machine, error) {
					mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
					if err != nil {
						return nil, err
					}
					return mapiMachine, nil
				}, capiframework.WaitShort, capiframework.RetryShort).Should(SatisfyAll(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", Equal(MAPIPausedCondition)),
						HaveField("Status", Equal(corev1.ConditionTrue)),
						HaveField("Reason", Equal("AuthoritativeAPINotMachineAPI")),
					))),
				))

				By("Verifying old Machines still exist and authority on them is still MachineAPI")
				Eventually(komega.Object(firstMAPIMachine)).Should(HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)))
				Eventually(komega.Object(secondMAPIMachine)).Should(HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)))
			})

			It("should scale down CAPI MachineSet to 1 succeed after switching AuthoritativeAPI to ClusterAPI", func() {
				By("Scaling up CAPI MachineSet to 1")
				Expect(capiframework.ScaleMachineSet(mapiMSAuthMAPIName, 1, capiframework.CAPINamespace)).To(Succeed(), "should be able to scale down CAPI MachineSet")

				By("Verifying both MAPI and CAPI machineset are scaled down to 1")
				Eventually(komega.Object(capiMachineSet)).Should(HaveField("Spec.Replicas", HaveValue(Equal(int32(1)))))
				Eventually(komega.Object(mapiMachineSet)).Should(HaveField("Spec.Replicas", HaveValue(Equal(int32(1)))))
			})

			It("should switch AuthoritativeAPI to MachineAPI succeed after switching AuthoritativeAPI to ClusterAPI", func() {
				updateMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				verifySynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyCAPIPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should delete both MAPI and CAPI MachineSets/Machines and InfraMachineTemplate when deleting MAPI MachineSet", func() {
				Expect(mapiframework.DeleteMachineSets(cl, mapiMachineSet)).To(Succeed(), "Should be able to delete test MachineSet")
				capiframework.WaitForMachineSetsDeleted(cl, capiMachineSet)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
				// bug https://issues.redhat.com/browse/OCPBUGS-57195
				/*
					Eventually(func() bool {
						awsMachineTemplate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, mapiMSAuthMAPIName)
						return apierrors.IsNotFound(err)
					}, capiframework.WaitMedium, capiframework.RetryMedium).Should(BeTrue(), "AWSMachineTemplate %s should be deleted", mapiMSAuthMAPIName)
				*/

			})
		})

		Context("with spec.authoritativeAPI: CAPI", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, mapiMSAuthCAPIName, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet creation should succeed")

				capiMachineSet, awsMachineTemplate = verifyMAPIMachineSetHasCAPIMirror(cl, mapiMSAuthCAPIName)

				mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")

				capiMachines, err := capiframework.GetMachinesFromMachineSet(cl, capiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get CAPI Machines from MachineSet")
				Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name))
				firstMAPIMachine = mapiMachines[0]

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: CAPI' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*capiv1beta1.MachineSet{capiMachineSet},
						[]*capav1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should scale CAPI MachineSet to 2 replicas succeed", func() {
				By("Scaling up CAPI MachineSet to 2 replicas")
				capiframework.ScaleMachineSet(mapiMSAuthCAPIName, 2, capiframework.CAPINamespace)
				capiMachineSet, err := capiframework.GetMachineSet(cl, mapiMSAuthCAPIName, capiframework.CAPINamespace)
				Expect(err).ToNot(HaveOccurred(), "Failed to get capiMachineSet %s", mapiMSAuthCAPIName)
				Eventually(komega.Object(mapiMachineSet)).Should(HaveField("Spec.Replicas", HaveValue(Equal(int32(2)))))

				By("Verifying a new CAPI Machine is created and Paused condition is False")
				Eventually(func() (*capiv1beta1.Machine, error) {
					capiMachine, err := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
					if err != nil {
						return nil, err
					}
					return capiMachine, nil
				}, capiframework.WaitLong, capiframework.RetryLong).Should(SatisfyAll(
					HaveField("Status.Phase", HaveValue(Equal(string(capiv1beta1.MachinePhaseRunning)))),
					HaveField("Status.V1Beta2.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", Equal(CAPIPausedCondition)),
						HaveField("Status", Equal(metav1.ConditionFalse)),
						HaveField("Reason", Equal("NotPaused")),
					))),
				))

				By("Verifying there is a non-authoritative, paused MAPI Machine mirror for the new CAPI Machine")
				Eventually(func() (*mapiv1beta1.Machine, error) {
					secondMAPIMachine, err = mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
					if err != nil {
						return nil, err
					}
					return secondMAPIMachine, nil
				}, capiframework.WaitShort, capiframework.RetryShort).Should(SatisfyAll(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", Equal(MAPIPausedCondition)),
						HaveField("Status", Equal(corev1.ConditionTrue)),
						HaveField("Reason", Equal("AuthoritativeAPINotMachineAPI")),
					))),
				))
			})

			It("should switch AuthoritativeAPI to MachineAPI succeed", func() {
				updateMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				verifySynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyCAPIPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should scale up MAPI MachineSet to 3 succeed after switching AuthoritativeAPI to MachineAPI", func() {
				By("Scaling up MAPI MachineSet to 3 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMSAuthCAPIName, 3)).To(Succeed(), "should be able to scale up MAPI MachineSet")
				Eventually(komega.Object(mapiMachineSet)).Should(HaveField("Spec.Replicas", HaveValue(Equal(int32(3)))))

				By("Verifying a new MAPI Machine is created and Paused condition is False")
				Eventually(func() (*mapiv1beta1.Machine, error) {
					mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
					if err != nil {
						return nil, err
					}
					return mapiMachine, nil
				}, capiframework.WaitLong, capiframework.RetryLong).Should(SatisfyAll(
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", Equal(MAPIPausedCondition)),
						HaveField("Status", Equal(corev1.ConditionFalse)),
					))),
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)),
					HaveField("Status.Phase", HaveValue(Equal(string(mapiv1beta1.PhaseRunning)))),
				))

				By("Verifying there is a mirrored paused CAPI Machine")
				Eventually(func(g Gomega) {
					capiMachine, err := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
					g.Expect(err).ToNot(HaveOccurred(), "error getting CAPI machine")
					g.Expect(capiMachine.Status.Conditions).ToNot(BeEmpty(), "CAPI Machine should have conditions")

					var pausedConditionFound bool
					for _, cond := range capiMachine.Status.Conditions {
						if string(cond.Type) == string(MAPIPausedCondition) && cond.Status == corev1.ConditionTrue {
							pausedConditionFound = true
							break
						}
					}
					g.Expect(pausedConditionFound).To(BeTrue(), "Expected Paused=True condition in CAPI Machine")
				}, capiframework.WaitShort, capiframework.RetryShort)

				By("Verifying old Machines still exist and authority on them is still ClusterAPI")
				Eventually(komega.Object(firstMAPIMachine)).Should(HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)))
				Eventually(komega.Object(secondMAPIMachine)).Should(HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)))
			})

			It("should scale down MAPI MachineSet to 1 succeed after switching AuthoritativeAPI to MachineAPI", func() {
				By("Scaling down MAPI MachineSet to 1 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMSAuthCAPIName, 1)).To(Succeed(), "should be able to scale down MAPI MachineSet")
				Eventually(komega.Object(mapiMachineSet)).Should(HaveField("Spec.Replicas", HaveValue(Equal(int32(1)))))
			})

			It("should switch AuthoritativeAPI to ClusterAPI succeed after switching AuthoritativeAPI to MachineAPI", func() {
				updateMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				verifySynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyCAPIPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should delete both MAPI and CAPI MachineSets/Machines and InfraMachineTemplate when deleting CAPI MachineSet", func() {
				capiframework.DeleteMachineSets(cl, capiMachineSet)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
				capiframework.WaitForMachineSetsDeleted(cl, capiMachineSet)
				// bug https://issues.redhat.com/browse/OCPBUGS-57195
				/*
					Eventually(func() bool {
						awsMachineTemplate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, mapiMSAuthMAPIName)
						return apierrors.IsNotFound(err)
					}, capiframework.WaitMedium, capiframework.RetryMedium).Should(BeTrue(), "InfraMachineTemplate should be deleted")
				*/
			})
		})

		Context("with spec.authoritativeAPI: MAPI, spec.template.spec.authoritativeAPI CAPI", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthMAPICAPI, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet creation should succeed")
				capiMachineSet, awsMachineTemplate = verifyMAPIMachineSetHasCAPIMirror(cl, mapiMSAuthMAPICAPI)

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MAPI, spec.template.spec.authoritativeAPI CAPI' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*capiv1beta1.MachineSet{capiMachineSet},
						[]*capav1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should create CAPI Machine when scaling MAPI MachineSet to 1 replicas", func() {
				By("Scaling up MAPI MachineSet to 1 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMachineSet.GetName(), 1)).To(Succeed(), "should be able to scale up MAPI MachineSet")
				capiframework.WaitForMachineSet(cl, mapiMSAuthMAPICAPI, capiframework.CAPINamespace)
				Eventually(komega.Object(mapiMachineSet)).Should(HaveField("Spec.Replicas", HaveValue(Equal(int32(1)))))

				By("Verifying MAPI Machine is created and .status.authoritativeAPI to equal CAPI")
				Eventually(func() (*mapiv1beta1.Machine, error) {
					mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
					if err != nil {
						return nil, err
					}
					return mapiMachine, nil
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(SatisfyAll(
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", Equal(MAPIPausedCondition)),
						HaveField("Status", Equal(corev1.ConditionTrue)),
					))),
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
				))

				By("Verifying CAPI Machine is created and Paused condition is False and provisions a running Machine")
				Eventually(func() (*capiv1beta1.Machine, error) {
					capiMachine, err := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
					if err != nil {
						return nil, err
					}
					return capiMachine, nil
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(SatisfyAll(
					HaveField("Status.Phase", HaveValue(Equal(string(capiv1beta1.MachinePhaseRunning)))),
					HaveField("Status.V1Beta2.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", Equal(CAPIPausedCondition)),
						HaveField("Status", Equal(metav1.ConditionFalse)),
						HaveField("Reason", Equal("NotPaused")),
					))),
				))
			})

			It("should delete both MAPI and CAPI MachineSets/Machines and InfraMachineTemplate when deleting MAPI MachineSet", func() {
				Expect(mapiframework.DeleteMachineSets(cl, mapiMachineSet)).To(Succeed(), "Should be able to delete test MachineSet")
				capiframework.WaitForMachineSetsDeleted(cl, capiMachineSet)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
				// bug https://issues.redhat.com/browse/OCPBUGS-57195
				/*
					Eventually(func() bool {
						awsMachineTemplate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, mapiMSAuthMAPIName)
						return apierrors.IsNotFound(err)
					}, capiframework.WaitMedium, capiframework.RetryMedium).Should(BeTrue(), "InfraMachineTemplate should be deleted")
				*/
			})
		})
	})

	var _ = Describe("Delete MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName = "ms-authoritativeapi-mapi"
		var mapiMachineSet *mapiv1beta1.MachineSet
		var capiMachineSet *capiv1beta1.MachineSet
		var awsMachineTemplate *capav1.AWSMachineTemplate
		var err error

		Context("when removing non-authoritative MAPI MachineSet", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, mapiMSAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet creation should succeed")

				capiMachineSet, awsMachineTemplate = verifyMAPIMachineSetHasCAPIMirror(cl, mapiMSAuthMAPIName)

				mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				capiMachines, err := capiframework.GetMachinesFromMachineSet(cl, capiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get CAPI Machines from MachineSet")
				Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name))

				DeferCleanup(func() {
					By("Cleaning up Context 'when removing non-authoritative MAPI MachineSet' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*capiv1beta1.MachineSet{capiMachineSet},
						[]*capav1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("shouldn't delete CAPI MachineSet when spec.authoritativeAPI is ClusterAPI", func() {
				By("Switching AuthoritativeAPI to ClusterAPI")
				updateMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Scaling up CAPI MachineSet to 2 replicas")
				Expect(capiframework.ScaleMachineSet(mapiMachineSet.GetName(), 2, capiframework.CAPINamespace)).To(Succeed(), "should be able to scale up CAPI MachineSet")
				Eventually(komega.Object(mapiMachineSet)).Should(HaveField("Spec.Replicas", HaveValue(Equal(int32(2)))))

				By("Verifying new CAPI Machine is running")
				Eventually(func() (*capiv1beta1.Machine, error) {
					capiMachine, err := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
					if err != nil {
						return nil, err
					}
					return capiMachine, nil
				}, capiframework.WaitLong, capiframework.RetryMedium).Should(SatisfyAll(
					HaveField("Status.Phase", HaveValue(Equal(string(capiv1beta1.MachinePhaseRunning)))),
					HaveField("Status.V1Beta2.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", Equal(CAPIPausedCondition)),
						HaveField("Status", Equal(metav1.ConditionFalse)),
						HaveField("Reason", Equal("NotPaused")),
					))),
				))

				By("Verifying there is a non-authoritative, paused MAPI Machine mirror for the new CAPI Machine")
				Eventually(func() (*mapiv1beta1.Machine, error) {
					mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
					if err != nil {
						return nil, err
					}
					return mapiMachine, nil
				}, capiframework.WaitShort, capiframework.RetryShort).Should(SatisfyAll(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", Equal(MAPIPausedCondition)),
						HaveField("Status", Equal(corev1.ConditionTrue)),
						HaveField("Reason", Equal("AuthoritativeAPINotMachineAPI")),
					))),
				))

				By("Deleting non-authoritative MAPI MachineSet")
				mapiMachineSet, err = mapiframework.GetMachineSet(ctx, cl, mapiMSAuthMAPIName)
				Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet")
				mapiframework.DeleteMachineSets(cl, mapiMachineSet)

				By("Verifying CAPI MachineSet not removed, both MAPI Machines and Mirrors remain")
				// bug https://issues.redhat.com/browse/OCPBUGS-56897
				/*
					Consistently(func() error {
						capiMachineSet, err := capiframework.GetMachineSet(cl, mapiMSAuthMAPIName, capiframework.CAPINamespace)
						if err != nil {
							return fmt.Errorf("failed to get CAPI MachineSet: %w", err)
						}
						if capiMachineSet == nil {
							return fmt.Errorf("CAPI MachineSet is nil")
						}

						capiMachines, err := capiframework.GetMachinesFromMachineSet(cl, capiMachineSet)
						if err != nil {
							return fmt.Errorf("failed to get CAPI Machines: %w", err)
						}
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
						Expect(machine.GetOwnerReferences()).To(BeEmpty(),
							"MAPI Machine %s should have no owner references", machine.Name)
					}
				*/
			})
		})
	})

	var _ = Describe("Update MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName = "ms-authoritativeapi-mapi"
		var mapiMachineSet *mapiv1beta1.MachineSet
		var capiMachineSet *capiv1beta1.MachineSet
		var awsMachineTemplate *capav1.AWSMachineTemplate
		var changedAWSMachineTemplate *capav1.AWSMachineTemplate
		var err error

		BeforeAll(func() {
			mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
			Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet creation should succeed")

			capiMachineSet, awsMachineTemplate = verifyMAPIMachineSetHasCAPIMirror(cl, mapiMSAuthMAPIName)

			DeferCleanup(func() {
				By("Cleaning up 'Update MachineSet' resources")
				cleanupTestResources(
					ctx,
					cl,
					[]*capiv1beta1.MachineSet{capiMachineSet},
					[]*capav1.AWSMachineTemplate{awsMachineTemplate, changedAWSMachineTemplate},
					[]*mapiv1beta1.MachineSet{mapiMachineSet},
				)
			})
		})

		Context("when MAPI MachineSet with spec.authoritativeAPI: MAPI and replicas 0", Ordered, func() {
			It("should be rejected when scaling CAPI mirror", func() {
				By("Scaling up CAPI MachineSet to 1 should be rejected")
				capiframework.ScaleMachineSet(mapiMSAuthMAPIName, 1, capiframework.CAPINamespace)
				mapiMachineSet, err := capiframework.GetMachineSet(cl, mapiMSAuthMAPIName, capiframework.CAPINamespace)
				Expect(err).ToNot(HaveOccurred(), "Failed to get mapiMachineSet %s", mapiMachineSet)
				Eventually(komega.Object(mapiMachineSet)).Should(HaveField("Spec.Replicas", HaveValue(Equal(int32(0)))))
			})

			It("should be rejected when updating CAPI mirror spec", func() {
				By("Updating CAPI mirror spec (such as DeletePolicy)")
				Eventually(func() error {
					capiMachineSet, err = capiframework.GetMachineSet(cl, mapiMSAuthMAPIName, capiframework.CAPINamespace)
					if err != nil {
						return err
					}
					return komega.Update(capiMachineSet, func() {
						capiMachineSet.Spec.DeletePolicy = "Oldest"
					})()
				}, capiframework.WaitMedium, capiframework.RetryShort).Should(Succeed(), "Failed to update CAPI MachineSet DeletePolicy")

				By("Verifying both MAPI and CAPI MachineSet spec value are restored to original value")
				Eventually(komega.Object(mapiMachineSet)).Should(HaveField("Spec.DeletePolicy", SatisfyAny(BeEmpty(), Equal("Random"))), "DeletePolicy should be either empty or 'Random'")
				Eventually(komega.Object(capiMachineSet)).Should(HaveField("Spec.DeletePolicy", HaveValue(Equal("Random"))))
			})

			It("should create a new InfraTemplate when update MAPI MachineSet providerSpec", func() {
				By("Updating MAPI MachineSet providerSpec InstanceType to m5.large")
				newInstanceType := "m5.large"
				originalAWSMachineTemplateName := capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name
				originalAWSMachineTemplate, err := capiframework.GetAWSMachineTemplateByPrefix(cl, mapiMSAuthMAPIName, capiframework.CAPINamespace)
				Expect(err).ToNot(HaveOccurred(), "Failed to get original awsMachineTemplate  %s", originalAWSMachineTemplate)

				patch := client.MergeFrom(mapiMachineSet.DeepCopy())
				var awsProviderSpec mapiv1beta1.AWSMachineProviderConfig
				Expect(json.Unmarshal(mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, &awsProviderSpec)).To(Succeed())
				awsProviderSpec.InstanceType = newInstanceType
				updatedSpec, err := json.Marshal(awsProviderSpec)
				Expect(err).NotTo(HaveOccurred())
				mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw = updatedSpec
				Expect(cl.Patch(ctx, mapiMachineSet, patch)).To(Succeed())

				By("Waiting for new InfraTemplate to be created")
				Eventually(func() bool {
					capiMachineSet, err := capiframework.GetMachineSet(cl, mapiMSAuthMAPIName, capiframework.CAPINamespace)
					if err != nil {
						return false
					}
					newInfraTemplateName := capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name
					return newInfraTemplateName != originalAWSMachineTemplateName
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(BeTrue(), "New InfraTemplate should be created")

				By("Verifying new InfraTemplate has the updated InstanceType")
				newAWSMachineTemplate, err := capiframework.GetAWSMachineTemplateByPrefix(cl, mapiMSAuthMAPIName, capiframework.CAPINamespace)
				Expect(err).ToNot(HaveOccurred(), "Failed to get new awsMachineTemplate  %s", newAWSMachineTemplate)
				Expect(newAWSMachineTemplate.Spec.Template.Spec.InstanceType).To(Equal(newInstanceType))

				By("Verifying old InfraTemplate is deleted")
				Eventually(func() bool {
					err := komega.Get(&capav1.AWSMachineTemplate{
						ObjectMeta: metav1.ObjectMeta{
							Name:      originalAWSMachineTemplateName,
							Namespace: capiframework.CAPINamespace,
						},
					})()
					return apierrors.IsNotFound(err)
				}).Should(BeTrue())
			})
		})

		Context("when switch MAPI MachineSet with spec.authoritativeAPI: CAPI", Ordered, func() {
			BeforeAll(func() {
				updateMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				verifySynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should be rejected when scaling MAPI MachineSet", func() {
				By("Scaling up MAPI MachineSet to 1")
				mapiframework.ScaleMachineSet(mapiMSAuthMAPIName, 1)

				By("Verifying MAPI MachineSet replicas is restored to original value 0")
				Eventually(komega.Object(mapiMachineSet)).Should(HaveField("Spec.Replicas", HaveValue(Equal(int32(0)))))
			})

			It("should be rejected when when updating providerSpec of MAPI MachineSet", func() {
				By("Getting the current MAPI MachineSet providerSpec InstanceType")
				providerSpec := mapiMachineSet.Spec.Template.Spec.ProviderSpec
				var awsConfig mapiv1beta1.AWSMachineProviderConfig
				_ = json.Unmarshal(providerSpec.Value.Raw, &awsConfig)
				originalInstanceType := awsConfig.InstanceType

				By("Updating the MAPI MachineSet providerSpec InstanceType")
				patch := client.MergeFrom(mapiMachineSet.DeepCopy())
				var awsProviderSpec mapiv1beta1.AWSMachineProviderConfig
				Expect(json.Unmarshal(mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, &awsProviderSpec)).To(Succeed())
				awsProviderSpec.InstanceType = "m5.xlarge"
				updatedSpec, err := json.Marshal(awsProviderSpec)
				Expect(err).NotTo(HaveOccurred())
				mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw = updatedSpec
				Expect(cl.Patch(ctx, mapiMachineSet, patch)).To(Succeed())

				By("Verifying MAPI MachineSet InstanceType is restored to original value")
				Eventually(func() string {
					mapiMachineSet, _ = mapiframework.GetMachineSet(ctx, cl, mapiMSAuthMAPIName)
					providerSpec := mapiMachineSet.Spec.Template.Spec.ProviderSpec
					var awsConfig mapiv1beta1.AWSMachineProviderConfig
					_ = json.Unmarshal(providerSpec.Value.Raw, &awsConfig)
					return awsConfig.InstanceType
				}, capiframework.WaitMedium, capiframework.RetryShort).Should(Equal(originalInstanceType), "Updating providerSpec.instanceType %s of MAPI MachineSet should be rejected", originalInstanceType)
			})

			It("should update MAPI MachineSet and remove old InfraTemplate when CAPI MachineSet points to new InfraTemplate", func() {
				By("Creating a new awsMachineTemplate with different spec")
				newInstanceType := "m6.xlarge"
				originalAWSMachineTemplateName := capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name

				_, mapiDefaultProviderSpec := getDefaultAWSMAPIProviderSpec(cl)
				createAWSClient(mapiDefaultProviderSpec.Placement.Region)
				changedAWSMachineTemplate = newAWSMachineTemplate(mapiDefaultProviderSpec)
				changedAWSMachineTemplate.Name = mapiMSAuthMAPIName + "-new"
				changedAWSMachineTemplate.Spec.Template.Spec.InstanceType = newInstanceType
				Expect(cl.Create(ctx, changedAWSMachineTemplate)).To(Succeed())

				By("Updating CAPI MachineSet to point to the new InfraTemplate")
				Eventually(komega.Update(capiMachineSet, func() {
					capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name = changedAWSMachineTemplate.Name
				})).Should(Succeed())

				By("Verifying the old InfraTemplate is deleted")
				Eventually(func() bool {
					err := komega.Get(&capav1.AWSMachineTemplate{
						ObjectMeta: metav1.ObjectMeta{
							Name:      originalAWSMachineTemplateName,
							Namespace: capiframework.CAPINamespace,
						},
					})()
					return apierrors.IsNotFound(err)
				}).Should(BeTrue())

				By("Verifying the MAPI MachineSet is updated to reflect the new template")
				Eventually(func() string {
					mapiMachineSet, _ = mapiframework.GetMachineSet(ctx, cl, mapiMSAuthMAPIName)
					return string(mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw)
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(ContainSubstring(newInstanceType))
			})
		})
	})
})

func createMAPIMachineSetWithAuthoritativeAPI(ctx context.Context, cl client.Client, replicas int, machineSetName string, machineSetAuthority mapiv1beta1.MachineAuthority, machineAuthority mapiv1beta1.MachineAuthority) (*mapiv1beta1.MachineSet, error) {
	By(fmt.Sprintf("Creating MAPI MachineSet with spec.authoritativeAPI: %s, spec.template.spec.authoritativeAPI: %s, replicas=%d", machineSetAuthority, machineAuthority, replicas))
	machineSetParams := mapiframework.BuildMachineSetParams(ctx, cl, replicas)
	machineSetParams.Name = machineSetName
	machineSetParams.MachinesetAuthoritativeAPI = machineSetAuthority
	machineSetParams.MachineAuthoritativeAPI = machineAuthority
	// Now CAPI machineSet doesn't support taint, remove it. card https://issues.redhat.com/browse/OCPCLOUD-2861
	machineSetParams.Taints = []corev1.Taint{}
	mapiMachineSet, err := mapiframework.CreateMachineSet(cl, machineSetParams)
	Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet %s creation should succeed", machineSetName)

	capiMachineSet := &capiv1beta1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineSetName,
			Namespace: capiframework.CAPINamespace,
		},
	}
	Eventually(komega.Get(capiMachineSet), capiframework.WaitShort, capiframework.RetryShort).Should(
		Succeed(), "Mirror CAPI MachineSet should be created within 1 minute")

	switch machineAuthority {
	case mapiv1beta1.MachineAuthorityMachineAPI:
		mapiframework.WaitForMachineSet(ctx, cl, machineSetName)
	case mapiv1beta1.MachineAuthorityClusterAPI:
		capiframework.WaitForMachineSet(cl, machineSetName, capiframework.CAPINamespace)
	}
	return mapiMachineSet, nil
}

func createCAPIMachineSet(ctx context.Context, cl client.Client, replicas int32, machineSetName string, instanceType string) (*capiv1beta1.MachineSet, error) {
	By(fmt.Sprintf("Creating CAPI MachineSet %s with %d replicas", machineSetName, replicas))

	_, mapiDefaultProviderSpec := getDefaultAWSMAPIProviderSpec(cl)
	createAWSClient(mapiDefaultProviderSpec.Placement.Region)
	awsMachineTemplate := newAWSMachineTemplate(mapiDefaultProviderSpec)
	awsMachineTemplate.Name = machineSetName
	if instanceType != "" {
		awsMachineTemplate.Spec.Template.Spec.InstanceType = instanceType
	}

	if err := cl.Create(ctx, awsMachineTemplate); err != nil {
		Expect(err).ToNot(HaveOccurred())
	}

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
	return machineSet, nil
}

func verifySynchronizedCondition(mapiMachineSet *mapiv1beta1.MachineSet, authority mapiv1beta1.MachineAuthority) {
	By("Verify the MAPI MachineSet Synchronized condition is True")
	var expectedMessage string

	switch authority {
	case mapiv1beta1.MachineAuthorityMachineAPI:
		expectedMessage = "Successfully synchronized MAPI MachineSet to CAPI"
	case mapiv1beta1.MachineAuthorityClusterAPI:
		expectedMessage = "Successfully synchronized CAPI MachineSet to MAPI"
	default:
		Fail(fmt.Sprintf("unknown authoritativeAPI type: %v", authority))
	}

	Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		WithTransform(
			func(ms *mapiv1beta1.MachineSet) []mapiv1beta1.Condition {
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

func verifyMAPIPausedCondition(mapiMachineSet *mapiv1beta1.MachineSet, authority mapiv1beta1.MachineAuthority) {
	var conditionMatcher types.GomegaMatcher

	switch authority {
	case mapiv1beta1.MachineAuthorityMachineAPI:
		By("Verifying MAPI MachineSet is unpaused")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(MAPIPausedCondition)),
			HaveField("Status", Equal(corev1.ConditionFalse)),
			HaveField("Reason", Equal("AuthoritativeAPIMachineAPI")),
			HaveField("Message", ContainSubstring("MachineAPI")),
		)
	case mapiv1beta1.MachineAuthorityClusterAPI:
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

func verifyCAPIPausedCondition(capiMachineSet *capiv1beta1.MachineSet, authority mapiv1beta1.MachineAuthority) {
	var conditionMatcher types.GomegaMatcher

	switch authority {
	case mapiv1beta1.MachineAuthorityClusterAPI:
		By("Verifying CAPI MachineSet is unpaused (ClusterAPI is authoritative)")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(CAPIPausedCondition)),
			HaveField("Status", Equal(metav1.ConditionFalse)),
			HaveField("Reason", Equal("NotPaused")),
		)
	case mapiv1beta1.MachineAuthorityMachineAPI:
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

func cleanupTestResources(ctx context.Context, cl client.Client, capiMachineSets []*capiv1beta1.MachineSet, awsMachineTemplates []*capav1.AWSMachineTemplate, mapiMachineSets []*mapiv1beta1.MachineSet) {
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

func verifyMAPIMachineSetHasCAPIMirror(cl client.Client, machineSetNameMAPI string) (*capiv1beta1.MachineSet, *capav1.AWSMachineTemplate) {
	By("Check MAPI MachineSet has a CAPI MachineSet mirror")
	var err error
	var capiMachineSet *capiv1beta1.MachineSet
	var awsMachineTemplate *capav1.AWSMachineTemplate

	Eventually(func() error {
		capiMachineSet, err = capiframework.GetMachineSet(cl, machineSetNameMAPI, capiframework.CAPINamespace)
		return err
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "CAPI MachineSet should exist")

	Eventually(func() error {
		awsMachineTemplate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, machineSetNameMAPI, capiframework.CAPINamespace)
		return err
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "awsMachineTemplate should exist")

	return capiMachineSet, awsMachineTemplate
}

func updateMachineSetAuthoritativeAPI(mapiMachineSet *mapiv1beta1.MachineSet, machineSetAuthority mapiv1beta1.MachineAuthority, machineAuthority mapiv1beta1.MachineAuthority) {
	Eventually(komega.Update(mapiMachineSet, func() {
		mapiMachineSet.Spec.AuthoritativeAPI = machineSetAuthority
		mapiMachineSet.Spec.Template.Spec.AuthoritativeAPI = machineAuthority
	})).Should(Succeed())
}
