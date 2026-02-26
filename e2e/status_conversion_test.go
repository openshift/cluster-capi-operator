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
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/yaml"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Status Conversion Tests", Ordered, func() {
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, these tests only support AWS", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	var _ = Describe("MachineSet Status Conversion", Ordered, func() {
		var mapiMSAuthMAPI *mapiv1beta1.MachineSet
		var mapiMSAuthCAPI *mapiv1beta1.MachineSet
		var capiMSMirrorMAPI *clusterv1.MachineSet
		var capiMSMirrorCAPI *clusterv1.MachineSet
		var awsMachineTemplateMAPI *awsv1.AWSMachineTemplate
		var awsMachineTemplateCAPI *awsv1.AWSMachineTemplate

		var mapiMSSameName *mapiv1beta1.MachineSet
		var capiMSSameName *clusterv1.MachineSet
		var awsMachineTemplateSameName *awsv1.AWSMachineTemplate

		BeforeAll(func() {
			By("Creating both MAPI-auth and CAPI-auth MachineSets without waiting (parallel creation)")
			mapiMSAuthMAPI = createMAPIMachineSetWithAuthoritativeAPISkipWait(
				ctx, cl, 1,
				UniqueName("status-mapi-auth-ms"),
				mapiv1beta1.MachineAuthorityMachineAPI,
				mapiv1beta1.MachineAuthorityMachineAPI,
				true, // skipWait=true
			)
			mapiMSAuthCAPI = createMAPIMachineSetWithAuthoritativeAPISkipWait(
				ctx, cl, 1,
				UniqueName("status-capi-auth-ms"),
				mapiv1beta1.MachineAuthorityClusterAPI,
				mapiv1beta1.MachineAuthorityClusterAPI,
				true, // skipWait=true
			)

			By("Creating a same-name MAPI MachineSet with CAPI authority")
			sameNameMSName := UniqueName("status-same-name-ms")
			capiMSSameName = createCAPIMachineSetSkipWait(ctx, cl, 1, sameNameMSName, "", true) // skipWait=true
			mapiMSSameName = createMAPIMachineSetWithAuthoritativeAPISkipWait(
				ctx, cl, 1, sameNameMSName,
				mapiv1beta1.MachineAuthorityClusterAPI,
				mapiv1beta1.MachineAuthorityClusterAPI,
				true, // skipWait=true
			)

			By("Waiting for all MachineSets to become ready (parallel waiting)")
			waitForMAPIMachineSetReady(ctx, cl, mapiMSAuthMAPI.Name, mapiv1beta1.MachineAuthorityMachineAPI)
			waitForMAPIMachineSetReady(ctx, cl, mapiMSAuthCAPI.Name, mapiv1beta1.MachineAuthorityClusterAPI)
			capiframework.WaitForMachineSet(cl, capiMSSameName.Name, capiframework.CAPINamespace)

			By("Getting CAPI MachineSet mirrors and AWSMachineTemplates")
			capiMSMirrorMAPI, awsMachineTemplateMAPI = waitForMAPIMachineSetMirrors(cl, mapiMSAuthMAPI.Name)
			capiMSMirrorCAPI, awsMachineTemplateCAPI = waitForMAPIMachineSetMirrors(cl, mapiMSAuthCAPI.Name)
			capiMSSameName, awsMachineTemplateSameName = waitForMAPIMachineSetMirrors(cl, sameNameMSName)
			capiframework.WaitForMachineSet(cl, mapiMSAuthCAPI.Name, capiframework.CAPINamespace)

			DeferCleanup(func() {
				By("Cleaning up MachineSet Status Conversion test resources")
				cleanupMachineSetTestResources(
					ctx, cl,
					[]*clusterv1.MachineSet{capiMSMirrorMAPI, capiMSMirrorCAPI, capiMSSameName},
					[]*awsv1.AWSMachineTemplate{awsMachineTemplateMAPI, awsMachineTemplateCAPI, awsMachineTemplateSameName},
					[]*mapiv1beta1.MachineSet{mapiMSAuthMAPI, mapiMSAuthCAPI, mapiMSSameName},
				)
			})
		})

		Context("MAPI to CAPI conversion", func() {
			It("should have MAPI MachineSet SynchronizedGeneration set and equal to its Generation", func() {
				Eventually(komega.Object(mapiMSAuthMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.SynchronizedGeneration", Equal(mapiMSAuthMAPI.Generation)),
					"MAPI MachineSet SynchronizedGeneration should equal its Generation",
				)
			})

			It("should convert status.replicas from MAPI to CAPI", func() {
				Eventually(komega.Object(capiMSMirrorMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Replicas", HaveValue(Equal(int32(1)))),
					"Should have CAPI MachineSet status.replicas equal to 1",
				)
			})

			It("should convert status.readyReplicas from MAPI to CAPI v1beta2 and v1beta1 deprecated status", func() {
				By("Waiting for MAPI MachineSet to have readyReplicas")
				Eventually(komega.Object(mapiMSAuthMAPI), capiframework.WaitLong, capiframework.RetryLong).Should(
					HaveField("Status.ReadyReplicas", BeNumerically(">", 0)),
					"Should have MAPI MachineSet with readyReplicas > 0",
				)

				By("Verifying CAPI MachineSet status.readyReplicas is synchronized")
				Eventually(komega.Object(capiMSMirrorMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.ReadyReplicas", HaveValue(Equal(mapiMSAuthMAPI.Status.ReadyReplicas))),
					"Should have CAPI MachineSet status.readyReplicas match MAPI",
				)

				By("Verifying CAPI MachineSet status.Deprecated.V1Beta1.readyReplicas is synchronized")
				Eventually(komega.Object(capiMSMirrorMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Deprecated.V1Beta1.ReadyReplicas", Equal(mapiMSAuthMAPI.Status.ReadyReplicas)),
					"Should have CAPI MachineSet status.Deprecated.V1Beta1.readyReplicas match MAPI",
				)
			})

			It("should convert status.availableReplicas from MAPI to CAPI status and status.Deprecated.V1Beta1", func() {
				By("Waiting for MAPI availableReplicas > 0")
				Eventually(komega.Object(mapiMSAuthMAPI), capiframework.WaitLong, capiframework.RetryMedium).Should(
					HaveField("Status.AvailableReplicas", BeNumerically(">", 0)),
					"Should have MAPI MachineSet status.availableReplicas > 0",
				)
				By("Verifying CAPI MachineSet status.availableReplicas is synchronized")
				Eventually(komega.Object(capiMSMirrorMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.AvailableReplicas", HaveValue(Equal(mapiMSAuthMAPI.Status.AvailableReplicas))),
					"Should have CAPI MachineSet status.availableReplicas match MAPI",
				)
				By("Verifying CAPI MachineSet status.Deprecated.V1Beta1.availableReplicas is synchronized")
				Eventually(komega.Object(capiMSMirrorMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Deprecated.V1Beta1.AvailableReplicas", Equal(mapiMSAuthMAPI.Status.AvailableReplicas)),
					"Should have CAPI MachineSet status.Deprecated.V1Beta1.availableReplicas match MAPI",
				)
			})

			It("should convert status.fullyLabeledReplicas from MAPI to CAPI", func() {
				By("Waiting for MAPI fullyLabeledReplicas > 0")
				Eventually(komega.Object(mapiMSAuthMAPI), capiframework.WaitLong, capiframework.RetryMedium).Should(
					HaveField("Status.FullyLabeledReplicas", BeNumerically(">", 0)),
					"Should have MAPI MachineSet status.FullyLabeledReplicas > 0",
				)
				By("Verifying CAPI MachineSet status.fullyLabeledReplicas is synchronized")
				Eventually(komega.Object(capiMSMirrorMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Deprecated.V1Beta1.FullyLabeledReplicas", Equal(mapiMSAuthMAPI.Status.FullyLabeledReplicas)),
					"Should have CAPI MachineSet Status.Deprecated.V1Beta1.FullyLabeledReplicas match MAPI",
				)
			})

			It("should convert status.ObservedGeneration from MAPI to CAPI", func() {
				Eventually(komega.Object(capiMSMirrorMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.ObservedGeneration", Equal(mapiMSAuthMAPI.Status.ObservedGeneration)),
					"Should have CAPI MachineSet status.ObservedGeneration match MAPI",
				)
			})

			It("should have v1beta1 deprecated conditions", func() {
				Eventually(komega.Object(capiMSMirrorMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Deprecated.V1Beta1.Conditions", SatisfyAll(
						ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.ReadyV1Beta1Condition)),
							HaveField("Status", Equal(corev1.ConditionTrue)),
						)),
						ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachinesReadyV1Beta1Condition)),
							HaveField("Status", Equal(corev1.ConditionTrue)),
						)),
						ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachinesCreatedV1Beta1Condition)),
							HaveField("Status", Equal(corev1.ConditionTrue)),
						)),
						ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.ResizedV1Beta1Condition)),
							HaveField("Status", Equal(corev1.ConditionTrue)),
						)),
					)),
					"Should have correct CAPI MachineSet v1beta1 deprecated conditions",
				)
			})

			// Note: When MachineSet has authoritativeAPI=MachineAPI, it gets paused (cluster.x-k8s.io/paused annotation).
			// CAPI clears all v1beta2 conditions for paused MachineSets, keeping only the Paused condition.
			// Therefore, we only verify the Paused condition.
			It("should have CAPI MachineSet v1beta2 Paused condition when authoritativeAPI is MachineAPI", func() {
				Eventually(komega.Object(capiMSMirrorMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", Equal(clusterv1.PausedCondition)),
						HaveField("Status", Equal(metav1.ConditionTrue)),
						HaveField("Reason", Equal(clusterv1.PausedReason)),
					))),
					"Should have CAPI MachineSet Paused condition",
				)
			})

			It("should NOT have Synchronized condition in CAPI MachineSet", func() {
				By("Verifying MAPI MachineSet has Synchronized condition")
				Eventually(komega.Object(mapiMSAuthMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Conditions", ContainElement(HaveField("Type", Equal(SynchronizedCondition)))),
					"Should have MAPI MachineSet with Synchronized condition",
				)
				By("Verifying CAPI MachineSet does NOT have Synchronized condition in v1beta2 conditions")
				Consistently(komega.Object(capiMSMirrorMAPI), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal("Synchronized"))))),
					"Should NOT have Synchronized condition in CAPI MachineSet v1beta2 conditions",
				)
			})

			It("should convert spec.selector to CAPI MachineSet status.selector", func() {
				By("Converting MAPI selector.matchLabels to label selector string")
				expectedSelector := labels.SelectorFromSet(mapiMSAuthMAPI.Spec.Selector.MatchLabels).String()

				By("Verifying CAPI MachineSet status.selector matches MAPI selector")
				Eventually(komega.Object(capiMSMirrorMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Selector", Equal(expectedSelector)),
					"Should have CAPI MachineSet status.selector match MAPI selector",
				)
			})
		})

		Context("CAPI to MAPI conversion", func() {
			It("should have MAPI MachineSet SynchronizedGeneration set and equal to CAPI MachineSet Generation", func() {
				Eventually(komega.Object(mapiMSAuthCAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.SynchronizedGeneration", Equal(capiMSMirrorCAPI.Generation)),
					"MAPI MachineSet SynchronizedGeneration should equal CAPI MachineSet Generation (CAPI authoritative)",
				)
			})

			It("should convert status.replicas from CAPI to MAPI", func() {
				Eventually(komega.Object(mapiMSAuthCAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Replicas", Equal(ptr.Deref(capiMSMirrorCAPI.Status.Replicas, 0))),
					"Should have MAPI MachineSet status.replicas match CAPI",
				)
			})

			It("should convert status.readyReplicas from CAPI to MAPI", func() {
				By("Waiting for CAPI readyReplicas > 0")
				Eventually(komega.Object(capiMSMirrorCAPI), capiframework.WaitLong, capiframework.RetryLong).Should(
					HaveField("Status.ReadyReplicas", HaveValue(BeNumerically(">", 0))),
					"Should have CAPI MachineSet with readyReplicas > 0",
				)

				By("Verifying MAPI MachineSet status.readyReplicas is synchronized")
				Eventually(komega.Object(mapiMSAuthCAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.ReadyReplicas", Equal(ptr.Deref(capiMSMirrorCAPI.Status.ReadyReplicas, 0))),
					"Should have MAPI MachineSet status.readyReplicas match CAPI",
				)
			})

			It("should convert status.availableReplicas from CAPI to MAPI", func() {
				By("Waiting for CAPI availableReplicas > 0")
				Eventually(komega.Object(capiMSMirrorCAPI), capiframework.WaitLong, capiframework.RetryMedium).Should(
					HaveField("Status.AvailableReplicas", HaveValue(BeNumerically(">", 0))),
					"Should have CAPI MachineSet with availableReplicas > 0",
				)

				By("Verifying MAPI MachineSet status.availableReplicas is synchronized")
				Eventually(komega.Object(mapiMSAuthCAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.AvailableReplicas", Equal(ptr.Deref(capiMSMirrorCAPI.Status.AvailableReplicas, 0))),
					"Should have MAPI MachineSet status.availableReplicas match CAPI",
				)
			})

			It("should convert status.fullyLabeledReplicas from CAPI to MAPI", func() {
				By("Waiting for CAPI fullyLabeledReplicas > 0")
				Eventually(komega.Object(capiMSMirrorCAPI), capiframework.WaitLong, capiframework.RetryMedium).Should(
					HaveField("Status.Deprecated.V1Beta1.FullyLabeledReplicas", BeNumerically(">", 0)),
					"Should have CAPI MachineSet with fullyLabeledReplicas > 0",
				)

				By("Verifying MAPI MachineSet status.fullyLabeledReplicas is synchronized")
				Eventually(komega.Object(mapiMSAuthCAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.FullyLabeledReplicas", Equal(capiMSMirrorCAPI.Status.Deprecated.V1Beta1.FullyLabeledReplicas)),
					"Should have MAPI MachineSet status.fullyLabeledReplicas match CAPI",
				)
			})

			It("should convert status.ObservedGeneration from CAPI to MAPI", func() {
				Eventually(komega.Object(mapiMSAuthCAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.ObservedGeneration", Equal(capiMSMirrorCAPI.Status.ObservedGeneration)),
					"Should have MAPI MachineSet status.ObservedGeneration match CAPI",
				)
			})

			It("should have MAPI MachineSet Synchronized condition", func() {
				Eventually(komega.Object(mapiMSAuthCAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", Equal(SynchronizedCondition)),
						HaveField("Status", Equal(corev1.ConditionTrue)),
					))),
					"Should have MAPI MachineSet with Synchronized condition",
				)
			})

			It("should NOT have CAPI-specific conditions", func() {
				Consistently(komega.Object(mapiMSAuthCAPI), capiframework.WaitShort, capiframework.RetryShort).Should(
					SatisfyAll(
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.ReadyCondition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.MachinesReadyV1Beta1Condition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.ResizedV1Beta1Condition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.MachinesCreatedV1Beta1Condition))))),
					),
					"Should NOT have CAPI-specific conditions in MAPI MachineSet",
				)
			})
		})

		Context("when CAPI MachineSet exists and MAPI MachineSet with CAPI authority is created with same name", func() {
			It("should have MAPI MachineSet SynchronizedGeneration set and equal to CAPI MachineSet Generation", func() {
				Eventually(komega.Object(mapiMSSameName), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.SynchronizedGeneration", Equal(capiMSSameName.Generation)),
					"MAPI MachineSet SynchronizedGeneration should equal CAPI MachineSet Generation",
				)
			})

			It("should convert status.replicas from CAPI to MAPI", func() {
				By("Waiting for CAPI MachineSet to have replicas > 0")
				Eventually(komega.Object(capiMSSameName), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Replicas", HaveValue(BeNumerically(">", 0))),
					"Should have CAPI MachineSet status.replicas > 0",
				)

				By("Verifying MAPI MachineSet status.replicas is synchronized")
				Eventually(komega.Object(mapiMSSameName), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Replicas", Equal(ptr.Deref(capiMSSameName.Status.Replicas, 0))),
					"Should have MAPI MachineSet status.replicas match CAPI",
				)
			})

			It("should convert status.readyReplicas from CAPI to MAPI", func() {
				By("Waiting for CAPI readyReplicas > 0")
				Eventually(komega.Object(capiMSSameName), capiframework.WaitLong, capiframework.RetryLong).Should(
					HaveField("Status.ReadyReplicas", HaveValue(BeNumerically(">", 0))),
					"Should have CAPI MachineSet with readyReplicas > 0",
				)

				By("Verifying MAPI MachineSet status.readyReplicas is synchronized")
				Eventually(komega.Object(mapiMSSameName), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.ReadyReplicas", Equal(ptr.Deref(capiMSSameName.Status.ReadyReplicas, 0))),
					"Should have MAPI MachineSet status.readyReplicas match CAPI",
				)
			})

			It("should convert status.availableReplicas from CAPI to MAPI", func() {
				By("Waiting for CAPI availableReplicas > 0")
				Eventually(komega.Object(capiMSSameName), capiframework.WaitLong, capiframework.RetryMedium).Should(
					HaveField("Status.AvailableReplicas", HaveValue(BeNumerically(">", 0))),
					"Should have CAPI MachineSet with availableReplicas > 0",
				)

				By("Verifying MAPI MachineSet status.AvailableReplicas is synchronized")
				Eventually(komega.Object(mapiMSSameName), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.AvailableReplicas", Equal(ptr.Deref(capiMSSameName.Status.AvailableReplicas, 0))),
					"Should have MAPI MachineSet status.availableReplicas match CAPI",
				)
			})

			It("should convert status.fullyLabeledReplicas from CAPI to MAPI", func() {
				By("Waiting for CAPI fullyLabeledReplicas > 0")
				Eventually(komega.Object(capiMSSameName), capiframework.WaitLong, capiframework.RetryMedium).Should(
					HaveField("Status.Deprecated.V1Beta1.FullyLabeledReplicas", BeNumerically(">", 0)),
					"Should have CAPI MachineSet with fullyLabeledReplicas > 0",
				)

				By("Verifying MAPI MachineSet status.FullyLabeledReplicas is synchronized")
				Eventually(komega.Object(mapiMSSameName), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.FullyLabeledReplicas", Equal(capiMSSameName.Status.Deprecated.V1Beta1.FullyLabeledReplicas)),
					"Should have MAPI MachineSet status.fullyLabeledReplicas match CAPI",
				)
			})

			It("should convert status.ObservedGeneration from CAPI to MAPI", func() {
				Eventually(komega.Object(mapiMSSameName), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.ObservedGeneration", Equal(capiMSSameName.Status.ObservedGeneration)),
					"Should have MAPI MachineSet status.ObservedGeneration match CAPI",
				)
			})

			It("should have MAPI MachineSet Synchronized condition", func() {
				Eventually(komega.Object(mapiMSSameName), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", Equal(SynchronizedCondition)),
						HaveField("Status", Equal(corev1.ConditionTrue)),
					))),
					"Should have MAPI MachineSet with Synchronized condition",
				)
			})

			It("should NOT have CAPI-specific conditions in MAPI MachineSet", func() {
				Consistently(komega.Object(mapiMSSameName), capiframework.WaitShort, capiframework.RetryShort).Should(
					SatisfyAll(
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.ReadyCondition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.MachinesReadyV1Beta1Condition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.ResizedV1Beta1Condition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.MachinesCreatedV1Beta1Condition))))),
					),
					"Should NOT have CAPI-specific conditions in MAPI MachineSet",
				)
			})
		})
	})

	var _ = Describe("Machine Status Conversion", Ordered, func() {
		var mapiMachineAuthMAPI *mapiv1beta1.Machine
		var mapiMachineAuthCAPI *mapiv1beta1.Machine
		var capiMachineMirrorMAPI *clusterv1.Machine
		var capiMachineMirrorCAPI *clusterv1.Machine

		var errorMachineSet *mapiv1beta1.MachineSet
		var errorCAPIMachineSet *clusterv1.MachineSet
		var errorAWSMachineTemplate *awsv1.AWSMachineTemplate
		var errorMachine *mapiv1beta1.Machine
		var errorCAPIMachine *clusterv1.Machine

		var capiMachineSameName *clusterv1.Machine
		var mapiMachineSameName *mapiv1beta1.Machine
		var err error

		BeforeAll(func() {
			By("Creating all Machines without waiting for them to be ready")
			mapiMachineAuthMAPI = createMAPIMachineWithAuthority(
				ctx, cl,
				UniqueName("status-mapi-machine"),
				mapiv1beta1.MachineAuthorityMachineAPI,
			)
			mapiMachineAuthCAPI = createMAPIMachineWithAuthority(
				ctx, cl,
				UniqueName("status-capi-machine"),
				mapiv1beta1.MachineAuthorityClusterAPI,
			)

			By("Creating error MachineSet with invalid instanceType (skipWait)")
			errorMachineSet = createMAPIMachineSetWithAuthoritativeAPISkipWait(
				ctx, cl, 0,
				UniqueName("status-error"),
				mapiv1beta1.MachineAuthorityMachineAPI,
				mapiv1beta1.MachineAuthorityMachineAPI,
				true, // skipWait=true
			)

			By("Setting invalid instanceType to trigger error")
			updateAWSMachineSetProviderSpec(ctx, cl, errorMachineSet, func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
				providerSpec.InstanceType = "invalid"
			})

			By("Waiting for error MachineSet to be ready before scaling")
			waitForMAPIMachineSetReady(ctx, cl, errorMachineSet.Name, mapiv1beta1.MachineAuthorityMachineAPI)

			By("Scaling error MachineSet to 1 and waiting for Machine creation")
			mapiframework.ScaleMachineSet(errorMachineSet.Name, 1)

			Eventually(func() ([]*mapiv1beta1.Machine, error) {
				return mapiframework.GetMachinesFromMachineSet(ctx, cl, errorMachineSet)
			}, capiframework.WaitShort, capiframework.RetryShort).Should(
				And(Not(BeEmpty()), HaveLen(1)),
				"Should have exactly one error Machine created",
			)

			errorMachine, err = mapiframework.GetLatestMachineFromMachineSet(ctx, cl, errorMachineSet)
			Expect(err).ToNot(HaveOccurred(), "Should get error Machine from MachineSet")
			Expect(errorMachine).ToNot(BeNil(), "Error Machine should not be nil")
			errorCAPIMachine = capiframework.GetMachine(cl, errorMachine.Name, capiframework.CAPINamespace)

			By("Creating same-name scenario: CAPI Machine first, then MAPI Machine")
			sameNameMachineName := UniqueName("status-same-name-machine")
			capiMachineSameName = createCAPIMachine(ctx, cl, sameNameMachineName)
			mapiMachineSameName = createMAPIMachineWithAuthority(ctx, cl, sameNameMachineName, mapiv1beta1.MachineAuthorityClusterAPI)

			By("Waiting for all normal Machines to be running (parallel waiting)")
			verifyMachineRunning(cl, mapiMachineAuthMAPI)
			verifyMachineRunning(cl, mapiMachineAuthCAPI)

			By("Getting CAPI Machine mirrors")
			capiMachineMirrorMAPI = capiframework.GetMachine(cl, mapiMachineAuthMAPI.Name, capiframework.CAPINamespace)
			capiMachineMirrorCAPI = capiframework.GetMachine(cl, mapiMachineAuthCAPI.Name, capiframework.CAPINamespace)
			errorCAPIMachineSet, errorAWSMachineTemplate = waitForMAPIMachineSetMirrors(cl, errorMachineSet.Name)

			DeferCleanup(func() {
				By("Cleaning up Machine Status Conversion test resources")
				cleanupMachineSetTestResources(ctx, cl,
					[]*clusterv1.MachineSet{errorCAPIMachineSet},
					[]*awsv1.AWSMachineTemplate{errorAWSMachineTemplate},
					[]*mapiv1beta1.MachineSet{errorMachineSet},
				)
				cleanupMachineResources(ctx, cl,
					[]*clusterv1.Machine{capiMachineMirrorCAPI, capiMachineSameName},
					[]*mapiv1beta1.Machine{mapiMachineAuthMAPI, mapiMachineAuthCAPI, mapiMachineSameName},
				)
			})
		})

		Context("MAPI to CAPI conversion", func() {
			It("should have MAPI Machine SynchronizedGeneration set and equal to its Generation", func() {
				mapiMachineAuthMAPI, err = mapiframework.GetMachine(cl, mapiMachineAuthMAPI.Name)
				Expect(err).ToNot(HaveOccurred())
				Eventually(komega.Object(mapiMachineAuthMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.SynchronizedGeneration", Equal(mapiMachineAuthMAPI.Generation)),
					"Should have SynchronizedGeneration equal to Generation",
				)
			})

			It("should convert MAPI Machine phase to CAPI Machine phase", func() {
				Eventually(komega.Object(capiMachineMirrorMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Phase", Equal(ptr.Deref(mapiMachineAuthMAPI.Status.Phase, ""))),
					"Should have CAPI Machine phase match MAPI Machine phase",
				)
			})

			It("should convert MAPI Machine nodeRef to CAPI Machine nodeRef", func() {
				By("Waiting for MAPI Machine to have nodeRef")
				Eventually(komega.Object(mapiMachineAuthMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.NodeRef", Not(BeNil())),
					"Should have MAPI nodeRef not nil",
				)

				By("Verifying CAPI Machine nodeRef matches MAPI Machine nodeRef")
				// Note: CAPI v1beta2 MachineNodeReference only has Name field, not Kind
				Eventually(komega.Object(capiMachineMirrorMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.NodeRef.Name", Equal(mapiMachineAuthMAPI.Status.NodeRef.Name)),
					"Should have CAPI Machine nodeRef Name match MAPI Machine nodeRef",
				)
			})

			It("should convert MAPI Machine lastUpdated to CAPI Machine lastUpdated", func() {
				By("Waiting for MAPI Machine to have lastUpdated")
				Eventually(komega.Object(mapiMachineAuthMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.LastUpdated", Not(BeNil())),
					"Should have MAPI Machine with lastUpdated",
				)

				By("Verifying CAPI Machine lastUpdated matches MAPI Machine lastUpdated")
				Eventually(komega.Object(capiMachineMirrorMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.LastUpdated", Equal(*mapiMachineAuthMAPI.Status.LastUpdated)),
					"Should have CAPI Machine lastUpdated match MAPI Machine lastUpdated",
				)
			})

			It("should convert MAPI Machine addresses to CAPI Machine addresses", func() {
				Eventually(komega.Object(mapiMachineAuthMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Addresses", Not(BeEmpty())),
					"Should have MAPI addresses not empty",
				)

				expectedMatchers := make([]interface{}, len(mapiMachineAuthMAPI.Status.Addresses))
				for i, addr := range mapiMachineAuthMAPI.Status.Addresses {
					expectedMatchers[i] = SatisfyAll(
						HaveField("Type", Equal(clusterv1.MachineAddressType(addr.Type))),
						HaveField("Address", Equal(addr.Address)),
					)
				}
				Eventually(komega.Object(capiMachineMirrorMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Addresses", ConsistOf(expectedMatchers...)),
					"Should have CAPI Machine addresses match MAPI Machine addresses",
				)
			})

			It("should convert MAPI Machine conditions to CAPI Machine v1beta1 deprecated conditions", func() {
				By("Waiting for MAPI Machine to have conditions")
				Eventually(komega.Object(mapiMachineAuthMAPI), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.Conditions", Not(BeEmpty())),
					"Should have MAPI conditions not empty",
				)

				By("Verifying CAPI Machine has v1beta1 deprecated conditions set")
				Eventually(komega.Object(capiMachineMirrorMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					SatisfyAll(
						HaveField("Status.Deprecated.V1Beta1.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.ReadyV1Beta1Condition)),
							HaveField("Status", Equal(corev1.ConditionTrue)),
						))),
						HaveField("Status.Deprecated.V1Beta1.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.BootstrapReadyV1Beta1Condition)),
							HaveField("Status", Equal(corev1.ConditionTrue)),
						))),
						HaveField("Status.Deprecated.V1Beta1.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.InfrastructureReadyV1Beta1Condition)),
							HaveField("Status", Equal(corev1.ConditionTrue)),
						))),
					),
					"Should have CAPI Machine v1beta1 deprecated conditions set",
				)
			})

			It("should convert MAPI Machine conditions to CAPI Machine v1beta2 conditions", func() {
				Eventually(komega.Object(capiMachineMirrorMAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					SatisfyAll(
						HaveField("Status.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachineAvailableCondition)),
							HaveField("Status", Equal(metav1.ConditionTrue)),
						))),
						HaveField("Status.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachineReadyCondition)),
							HaveField("Status", Equal(metav1.ConditionTrue)),
						))),
						HaveField("Status.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachineBootstrapConfigReadyCondition)),
							HaveField("Status", Equal(metav1.ConditionTrue)),
						))),
						HaveField("Status.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachineInfrastructureReadyCondition)),
							HaveField("Status", Equal(metav1.ConditionTrue)),
						))),
						HaveField("Status.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachineNodeReadyCondition)),
							HaveField("Status", Equal(metav1.ConditionTrue)),
						))),
						HaveField("Status.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", Equal(clusterv1.MachineDeletingCondition)),
							HaveField("Status", Equal(metav1.ConditionFalse)),
						))),
					),
					"Should have CAPI Machine v1beta2 conditions set",
				)
			})

			It("should NOT have MAPI Synchronized condition in CAPI Machine", func() {
				By("Verifying MAPI Machine has Synchronized condition")
				Eventually(komega.Object(mapiMachineAuthMAPI), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.Conditions", ContainElement(HaveField("Type", Equal(SynchronizedCondition)))),
					"Should have MAPI Machine with Synchronized condition",
				)
				By("Verifying CAPI Machine does NOT have Synchronized condition in v1beta2 conditions")
				Consistently(komega.Object(capiMachineMirrorMAPI), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal("Synchronized"))))),
					"Should NOT have Synchronized condition in CAPI Machine v1beta2 conditions",
				)
			})

			It("should convert MAPI Machine providerStatus to CAPI AWSMachine status", func() {
				By("Getting AWSMachine for the CAPI Machine")
				awsMachine := capiframework.GetAWSMachine(cl, capiMachineMirrorMAPI.Name, capiframework.CAPINamespace)
				Expect(awsMachine).NotTo(BeNil(), "AWSMachine should exist")

				By("Verifying AWSMachine status.ready is set based on MAPI Machine providerStatus.instanceState")
				Eventually(komega.Object(awsMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Ready", BeTrue()),
					"Should have AWSMachine status.ready be true when instance is running",
				)

				By("Verifying AWSMachine status.instanceState matches MAPI Machine providerStatus.instanceState")
				Eventually(komega.Object(mapiMachineAuthMAPI), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.ProviderStatus.Raw", Not(BeEmpty())),
					"Should have MAPI Machine ProviderStatus.Raw populated",
				)

				mapiMachineAuthMAPI, err = mapiframework.GetMachine(cl, mapiMachineAuthMAPI.Name)
				Expect(err).ToNot(HaveOccurred())
				var mapiProviderStatus mapiv1beta1.AWSMachineProviderStatus
				err = yaml.Unmarshal(mapiMachineAuthMAPI.Status.ProviderStatus.Raw, &mapiProviderStatus)
				Expect(err).ToNot(HaveOccurred())

				Eventually(komega.Object(awsMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.InstanceState", HaveValue(Equal(awsv1.InstanceState(ptr.Deref(mapiProviderStatus.InstanceState, ""))))),
					"Should have AWSMachine status.instanceState match MAPI providerStatus.instanceState",
				)
			})
		})

		Context("CAPI to MAPI conversion", func() {
			It("should have MAPI Machine SynchronizedGeneration set and equal to CAPI Machine Generation", func() {
				mapiMachineAuthCAPI, err = mapiframework.GetMachine(cl, mapiMachineAuthCAPI.Name)
				Expect(err).ToNot(HaveOccurred())
				capiMachineMirrorCAPI = capiframework.GetMachine(cl, mapiMachineAuthCAPI.Name, capiframework.CAPINamespace)

				Eventually(komega.Object(mapiMachineAuthCAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.SynchronizedGeneration", Equal(capiMachineMirrorCAPI.Generation)),
					"Should have MAPI Machine SynchronizedGeneration equal to CAPI Generation",
				)
			})

			It("should convert CAPI Machine phase to MAPI Machine phase", func() {
				Eventually(komega.Object(mapiMachineAuthCAPI), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.Phase", HaveValue(Equal(capiMachineMirrorCAPI.Status.Phase))),
					"Should have MAPI Machine phase match CAPI Machine phase",
				)
			})

			It("should convert CAPI Machine nodeRef to MAPI Machine nodeRef", func() {
				By("Waiting for CAPI Machine to have nodeRef")
				Eventually(komega.Object(capiMachineMirrorCAPI), capiframework.WaitLong, capiframework.RetryShort).Should(
					HaveField("Status.NodeRef", Not(BeNil())),
					"Should have CAPI nodeRef not nil",
				)

				By("Verifying MAPI Machine nodeRef matches CAPI Machine nodeRef")
				Eventually(komega.Object(mapiMachineAuthCAPI), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.NodeRef.Name", Equal(capiMachineMirrorCAPI.Status.NodeRef.Name)),
					"Should have MAPI Machine nodeRef match CAPI Machine nodeRef",
				)
			})

			It("should convert CAPI Machine lastUpdated to MAPI Machine lastUpdated", func() {
				By("Waiting for CAPI Machine to have lastUpdated")
				Eventually(komega.Object(capiMachineMirrorCAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.LastUpdated", Not(BeZero())),
					"Should have CAPI Machine with lastUpdated set",
				)

				By("Verifying MAPI Machine lastUpdated matches CAPI Machine lastUpdated")
				Eventually(komega.Object(mapiMachineAuthCAPI), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.LastUpdated", HaveValue(Equal(capiMachineMirrorCAPI.Status.LastUpdated))),
					"Should have MAPI Machine lastUpdated match CAPI Machine lastUpdated",
				)
			})

			It("should convert CAPI Machine addresses to MAPI Machine addresses", func() {
				By("Waiting for CAPI Machine to have addresses")
				capiMachineMirrorCAPI = capiframework.GetMachine(cl, capiMachineMirrorCAPI.Name, capiframework.CAPINamespace)
				Eventually(komega.Object(capiMachineMirrorCAPI), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.Addresses", Not(BeEmpty())),
					"Should have CAPI addresses not empty",
				)

				By("Verifying MAPI Machine has matching addresses from CAPI")
				expectedMatchers := make([]interface{}, len(capiMachineMirrorCAPI.Status.Addresses))
				for i, addr := range capiMachineMirrorCAPI.Status.Addresses {
					expectedMatchers[i] = SatisfyAll(
						HaveField("Type", Equal(corev1.NodeAddressType(addr.Type))),
						HaveField("Address", Equal(addr.Address)),
					)
				}
				Eventually(komega.Object(mapiMachineAuthCAPI), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Addresses", ConsistOf(expectedMatchers...)),
					"Should have MAPI Machine addresses match CAPI Machine addresses",
				)
			})

			It("should NOT have CAPI-specific conditions in MAPI Machine", func() {
				Consistently(komega.Object(mapiMachineAuthCAPI), capiframework.WaitShort, capiframework.RetryShort).Should(
					SatisfyAll(
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.ReadyCondition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.MachinesReadyV1Beta1Condition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.ResizedV1Beta1Condition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.MachinesCreatedV1Beta1Condition))))),
					),
					"Should NOT have CAPI-specific conditions in MAPI Machine",
				)
			})

			// bug https://issues.redhat.com/browse/OCPBUGS-70136
			PIt("should convert CAPI AWSMachine status to MAPI providerStatus", func() {
				By("Getting AWSMachine for the CAPI Machine")
				awsMachine := capiframework.GetAWSMachine(cl, capiMachineMirrorCAPI.Name, capiframework.CAPINamespace)
				Expect(awsMachine).NotTo(BeNil(), "AWSMachine should exist")

				By("Waiting for AWSMachine to have instanceState set")
				Eventually(komega.Object(awsMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.InstanceState", Not(BeNil())),
					"Should have AWSMachine InstanceState not nil",
				)

				verifyMAPIMachineProviderStatus(mapiMachineAuthCAPI,
					HaveField("InstanceState", HaveValue(Equal(ptr.Deref(awsMachine.Status.InstanceState, "")))),
				)

				verifyMAPIMachineProviderStatus(mapiMachineAuthCAPI,
					WithTransform(func(status *mapiv1beta1.AWSMachineProviderStatus) bool {
						for _, cond := range status.Conditions {
							if cond.Type == string(mapiv1beta1.MachineCreation) {
								return cond.Status == metav1.ConditionTrue
							}
						}
						return false
					}, BeTrue()),
				)
			})
		})

		Context("Error status conversion", func() {
			It("should convert error status to CAPI failureReason and failureMessage", func() {
				By("Waiting for MAPI Machine error status")
				Eventually(komega.Object(errorMachine), capiframework.WaitLong, capiframework.RetryLong).Should(
					SatisfyAny(
						HaveField("Status.ErrorReason", Not(BeNil())),
						HaveField("Status.ErrorMessage", Not(BeNil())),
					),
					"Should have error status in MAPI Machine",
				)

				if errorMachine.Status.ErrorReason != nil {
					Eventually(komega.Object(errorCAPIMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
						HaveField("Status.Deprecated.V1Beta1.FailureReason", HaveValue(BeEquivalentTo(*errorMachine.Status.ErrorReason))),
						"Should have failureReason converted from MAPI to CAPI",
					)
				}

				if errorMachine.Status.ErrorMessage != nil {
					Eventually(komega.Object(errorCAPIMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
						HaveField("Status.Deprecated.V1Beta1.FailureMessage", HaveValue(Equal(*errorMachine.Status.ErrorMessage))),
						"Should have failureMessage converted from MAPI to CAPI",
					)
				}
			})

			It("should convert Failed phase", func() {
				Eventually(komega.Object(errorMachine), capiframework.WaitLong, capiframework.RetryLong).Should(
					HaveField("Status.Phase", HaveValue(Equal("Failed"))),
					"Should have Failed phase in MAPI Machine",
				)
				Eventually(komega.Object(errorCAPIMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Phase", Equal("Failed")),
					"Should have Failed phase converted from MAPI to CAPI",
				)
			})
		})

		Context("when CAPI Machine exists and MAPI Machine with CAPI authority is created with same name", func() {
			It("should have MAPI Machine SynchronizedGeneration set and equal to CAPI Machine Generation same-name scenario", func() {
				mapiMachineSameName, err = mapiframework.GetMachine(cl, mapiMachineSameName.Name)
				Expect(err).ToNot(HaveOccurred())
				capiMachineSameName = capiframework.GetMachine(cl, mapiMachineSameName.Name, capiframework.CAPINamespace)

				Eventually(komega.Object(mapiMachineSameName), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.SynchronizedGeneration", Equal(capiMachineSameName.Generation)),
					"Should have SynchronizedGeneration equal to CAPI Generation",
				)
			})

			// bug https://issues.redhat.com/browse/OCPBUGS-63183
			PIt("should convert CAPI Machine phase to MAPI Machine phase", func() {
				Eventually(komega.Object(mapiMachineSameName), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.Phase", HaveValue(Equal(capiMachineSameName.Status.Phase))),
					"Should have phase converted from CAPI to MAPI",
				)
			})

			It("should convert CAPI Machine nodeRef to MAPI Machine nodeRef", func() {
				By("Waiting for CAPI nodeRef")
				Eventually(komega.Object(capiMachineSameName), capiframework.WaitLong, capiframework.RetryShort).Should(
					HaveField("Status.NodeRef", Not(BeNil())),
					"Should have CAPI nodeRef not nil",
				)

				By("Verifying MAPI Machine has matching nodeRef from CAPI")
				Eventually(komega.Object(mapiMachineSameName), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.NodeRef.Name", Equal(capiMachineSameName.Status.NodeRef.Name)),
					"Should have nodeRef.Name converted from CAPI to MAPI",
				)
			})

			// bug https://issues.redhat.com/browse/OCPBUGS-63183
			PIt("should convert CAPI Machine lastUpdated to MAPI Machine lastUpdated", func() {
				By("Waiting for CAPI Machine to have lastUpdated")
				Eventually(komega.Object(capiMachineSameName), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.LastUpdated", Not(BeZero())),
					"Should have CAPI Machine with lastUpdated set",
				)

				By("Verifying MAPI Machine lastUpdated matches CAPI Machine lastUpdated")
				Eventually(komega.Object(mapiMachineSameName), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.LastUpdated", HaveValue(Equal(capiMachineSameName.Status.LastUpdated))),
					"Should have lastUpdated converted from CAPI to MAPI",
				)
			})

			// bug https://issues.redhat.com/browse/OCPBUGS-63183
			PIt("should convert CAPI Machine addresses to MAPI Machine addresses", func() {
				By("Waiting for CAPI Machine to have addresses")
				capiMachineSameName = capiframework.GetMachine(cl, capiMachineSameName.Name, capiframework.CAPINamespace)
				Eventually(komega.Object(capiMachineSameName), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Status.Addresses", Not(BeEmpty())),
					"Should have CAPI addresses not empty",
				)

				By("Verifying MAPI Machine has matching addresses from CAPI")
				expectedMatchers := make([]interface{}, len(capiMachineSameName.Status.Addresses))
				for i, addr := range capiMachineSameName.Status.Addresses {
					expectedMatchers[i] = SatisfyAll(
						HaveField("Type", Equal(corev1.NodeAddressType(addr.Type))),
						HaveField("Address", Equal(addr.Address)),
					)
				}
				Eventually(komega.Object(mapiMachineSameName), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Status.Addresses", ConsistOf(expectedMatchers...)),
					"Should have MAPI Machine addresses match CAPI Machine addresses",
				)
			})

			It("should NOT have CAPI-specific conditions in MAPI Machine", func() {
				Consistently(komega.Object(mapiMachineSameName), capiframework.WaitShort, capiframework.RetryShort).Should(
					SatisfyAll(
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.ReadyCondition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.MachinesReadyV1Beta1Condition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.ResizedV1Beta1Condition))))),
						HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal(clusterv1.MachinesCreatedV1Beta1Condition))))),
					),
					"Should NOT have CAPI-specific conditions in MAPI Machine",
				)
			})
		})
	})
})
