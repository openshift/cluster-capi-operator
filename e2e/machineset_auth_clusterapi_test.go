package e2e

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const ()

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] A MachineSet with ClusterAPI Authority", Ordered, func() {
	var mapiMachineSet *machinev1beta1.MachineSet
	var capiMachineSet *capiv1beta1.MachineSet
	var awsMachineTemplate *capav1.AWSMachineTemplate
	var machineSetParams mapiframework.MachineSetParams
	var machinesetName = "machineset-auth-capi"

	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("skipping tests on %s, this only support on aws", platform))
		}

		if !framework.IsTechPreviewNoUpgrade(ctx, cl) {
			Skip(fmt.Sprintf("skipping, this feature is only supported on TechPreviewNoUpgrade clusters"))
		}

		By("Creating a MAPI machine set with AuthoritativeAPI ClusterAPI")
		var err error
		machineSetParams = mapiframework.BuildMachineSetParams(ctx, cl, 1)
		machineSetParams.Name = machinesetName
		machineSetParams.MachinesetAuthoritativeAPI = machinev1beta1.MachineAuthorityClusterAPI
		machineSetParams.MachineAuthoritativeAPI = machinev1beta1.MachineAuthorityClusterAPI
		// now capi machineset doesn't support taint. card https://issues.redhat.com/browse/OCPCLOUD-2861
		machineSetParams.Taints = []corev1.Taint{}
		mapiMachineSet, err = mapiframework.CreateMachineSet(cl, machineSetParams)
		Expect(err).ToNot(HaveOccurred(), "mapi machineSet creation should succeed")
		time.Sleep(framework.WaitShort)
		framework.WaitForMachineSet(cl, machinesetName)
	})

	AfterAll(func() {
		if mapiMachineSet != nil {
			By("Deleting the new MAPI MachineSet")
			mapiframework.DeleteMachineSets(cl, mapiMachineSet)
			mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
		}
		if capiMachineSet != nil {
			By("Deleting the new CAPI MachineSet")
			framework.DeleteMachineSets(cl, capiMachineSet)
			// now capi machineset can't be deleted. bug https://issues.redhat.com/browse/OCPBUGS-55215
			//framework.WaitForMachineSetsDeleted(cl, capiMachineSet)
			framework.DeleteObjects(cl, awsMachineTemplate)
		}
	})

	Context("when the CAPI machine set does not exist", func() {
		It("should create the CAPI machine set and machine and template", func() {
			var err error
			Eventually(func() error {
				capiMachineSet, err = framework.GetMachineSet(cl, machinesetName)
				return err
			}, framework.WaitMedium, framework.RetryMedium).Should(Succeed(), "it should be able to get the capi machineset")
			Eventually(func() error {
				awsMachineTemplate, err = framework.GetAWSMachineTemplate(cl, machinesetName)
				return err
			}, framework.WaitMedium, framework.RetryMedium).Should(Succeed(), "it should be able to get the awsmachinetemplate")

			mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
			Expect(err).ToNot(HaveOccurred(), "failed to get mapi machines from machineset")
			capiMachines, err := framework.GetMachinesFromMachineSet(cl, capiMachineSet)
			Expect(err).ToNot(HaveOccurred(), "failed to get capi machines from machineset")
			Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name))
		})

		It("should set the MAPI machine set status AuthoritativeAPI to 'ClusterAPI'", func() {
			Eventually(func() machinev1beta1.MachineAuthority {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
				if err != nil {
					return ""
				}
				return mapiMachineSet.Status.AuthoritativeAPI
			}, framework.WaitShort, framework.RetryShort).Should(Equal(machinev1beta1.MachineAuthorityClusterAPI), "mapi machineset status.AuthoritativeAPI should be ClusterAPI")
		})

		It("should update the synchronized condition on the MAPI machine set to True", func() {
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
		})

		It("should update the Paused condition on the MAPI machine set to True", func() {
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
		})

		// bug https://issues.redhat.com/browse/OCPBUGS-55367
		/*
			It("should update the Paused condition on the CAPI machine set to False", func() {
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
							HaveField("Reason", Equal("Paused")),
							HaveField("Message", Equal("MachineSet has the cluster.x-k8s.io/paused annotation")),
						),
					),
				)
			})
		*/

		It("should create CAPI MachineSet and InfraMachineTemplate with CAPI Cluster OwnerReference", func() {
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

		Context("when the CAPI machine set does exist", func() {
			// bug https://issues.redhat.com/browse/OCPBUGS-55367
			/*
				It("should take affect when scaling up capi machineset", func() {
					Expect(framework.ScaleMachineSet(machinesetName, 2)).To(Succeed(), "should be able to scale up capi MachineSet")
					framework.WaitForMachineSet(cl, machinesetName)
					mapims, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
					Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet %s", machinesetName)
					Expect(*mapims.Spec.Replicas).To(Equal(int32(2)), "replicas should change to 2")
				})
			*/

			It("should not take affect when scaling up mapi machineset", func() {
				mapiframework.ScaleMachineSet(machinesetName, 2)
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
				Expect(err).ToNot(HaveOccurred(), "Failed to get mapiMachineSet %s", machinesetName)
				Expect(*mapiMachineSet.Spec.Replicas).To(Equal(int32(1)), "mapiMachineSet replicas should be 1")

				capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
				Expect(err).ToNot(HaveOccurred(), "Failed to get capiMachineSet %s", machinesetName)
				Expect(*capiMachineSet.Spec.Replicas).To(Equal(int32(1)), "capiMachineSet replicas should be 1")
			})

			It("should not take affect when updating mapi machineset's DeletePolicy", func() {
				mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
				Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet %s", machinesetName)

				mapiMachineSet = mapiMachineSet.DeepCopy()
				mapiMachineSet.Spec.DeletePolicy = "Oldest"

				err = cl.Patch(ctx, mapiMachineSet, client.MergeFrom(mapiMachineSet))
				Expect(err).NotTo(HaveOccurred(), "failed to patch MAPI MachineSet deletePolicy")

				mapiMachineSet, err = mapiframework.GetMachineSet(ctx, cl, machinesetName)
				Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet %s", machinesetName)
				Expect(mapiMachineSet.Spec.DeletePolicy).To(
					SatisfyAny(
						BeEmpty(),
						Equal("Random"),
					),
					"MAPI MachineSet deletePolicy should be empty or 'Random', but got: %s",
					mapiMachineSet.Spec.DeletePolicy,
				)

				capiMachineSet, err = framework.GetMachineSet(cl, machinesetName)
				Expect(err).ToNot(HaveOccurred(), "failed to get updated CAPI MachineSet")
				Expect(capiMachineSet.Spec.DeletePolicy).To(Equal("Random"), "CAPI MachineSet deletePolicy should remain the original value 'Random'")
			})

			It("should take affect when updating capi machineset's DeletePolicy", func() {
				capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
				Expect(err).ToNot(HaveOccurred(), "failed to get capiMachineSet %s", machinesetName)

				capiMachineSet.Spec.DeletePolicy = "Oldest"
				err = cl.Update(ctx, capiMachineSet)
				Expect(err).NotTo(HaveOccurred(), "failed to update CAPI MachineSet DeletePolicy")

				Eventually(func() string {
					capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
					if err != nil {
						return ""
					}
					return string(capiMachineSet.Spec.DeletePolicy)
				}, framework.WaitShort, framework.RetryShort).Should(Equal("Oldest"), "CAPI MachineSet DeletePolicy changed to the new value 'Oldest'")

				Eventually(func() string {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
					if err != nil {
						return ""
					}
					return string(mapiMachineSet.Spec.DeletePolicy)
				}, framework.WaitShort, framework.RetryShort).Should(Equal("Oldest"), "MAPI MachineSet DeletePolicy changed to the new value 'Oldest'")
			})

			It("should pass labels/annotations from CAPI to MAPI machineset", func() {
				capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
				Expect(err).ToNot(HaveOccurred(), "Failed to get capiMachineSet %s", machinesetName)

				patch := client.MergeFrom(capiMachineSet.DeepCopy())
				if capiMachineSet.Spec.Template.ObjectMeta.Labels == nil {
					capiMachineSet.Spec.Template.ObjectMeta.Labels = make(map[string]string)
				}
				if capiMachineSet.Spec.Template.ObjectMeta.Annotations == nil {
					capiMachineSet.Spec.Template.ObjectMeta.Annotations = make(map[string]string)
				}
				capiMachineSet.Spec.Template.ObjectMeta.Labels["mapi-label1"] = "mapi-label1"
				capiMachineSet.Spec.Template.ObjectMeta.Annotations["mapi-ano1"] = "mapi-ano1"

				err = cl.Patch(ctx, capiMachineSet, patch)
				Expect(err).NotTo(HaveOccurred())

				Eventually(func() bool {
					capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
					if err != nil {
						return false
					}
					_, hasLabel := capiMachineSet.Spec.Template.ObjectMeta.Labels["mapi-label1"]
					_, hasAnno := capiMachineSet.Spec.Template.ObjectMeta.Annotations["mapi-ano1"]
					return hasLabel && hasAnno
				}, framework.WaitShort, framework.RetryShort).Should(BeTrue(), "CAPI machineset should have new labels/annotations")

				Eventually(func() bool {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
					if err != nil {
						return false
					}
					_, hasLabel := mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels["mapi-label1"]
					_, hasAnno := mapiMachineSet.Spec.Template.Spec.ObjectMeta.Annotations["mapi-ano1"]
					return hasLabel && hasAnno
				}, framework.WaitShort, framework.RetryShort).Should(BeTrue(), "MAPI machineset should have new labels/annotations")
			})

			// bug https://issues.redhat.com/browse/OCPBUGS-55992
			/*
				It("should not propagate labels/annotations from MAPI to CAPI machineset", func() {
					mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
					Expect(err).ToNot(HaveOccurred(), "failed to get capiMachineSet %s", machinesetName)

					patch := client.MergeFrom(mapiMachineSet.DeepCopy())
					if mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels == nil {
						mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels = make(map[string]string)
					}
					if mapiMachineSet.Spec.Template.Spec.ObjectMeta.Annotations == nil {
						mapiMachineSet.Spec.Template.Spec.ObjectMeta.Annotations = make(map[string]string)
					}
					mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels["capi-label1"] = "capi-label1"
					mapiMachineSet.Spec.Template.Spec.ObjectMeta.Annotations["capi-ano1"] = "capi-ano1"

					err = cl.Patch(ctx, mapiMachineSet, patch)
					Expect(err).NotTo(HaveOccurred())

					Eventually(func() bool {
						mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
						if err != nil {
							return false
						}
						_, hasLabel := mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels["capi-label1"]
						_, hasAnno := mapiMachineSet.Spec.Template.Spec.ObjectMeta.Annotations["capi-ano1"]
						return !hasLabel && !hasAnno
					}, framework.WaitShort, framework.RetryShort).Should(BeTrue(), "MAPI machineset should not have CAPI labels/annotations")

					Eventually(func() bool {
						capiMachineSet, err := framework.GetMachineSet(cl, machinesetName)
						if err != nil {
							return false
						}
						_, hasLabel := capiMachineSet.Spec.Template.ObjectMeta.Labels["capi-label1"]
						_, hasAnno := capiMachineSet.Spec.Template.ObjectMeta.Annotations["capi-ano1"]
						return !hasLabel && !hasAnno
					}, framework.WaitShort, framework.RetryShort).Should(BeTrue(), "CAPI machineset should not have new labels/annotations")
				})
			*/

			When("change AuthoritativeAPI from ClusterAPI to MachineAPI", func() {
				It("should take affect when changing AuthoritativeAPI to MachineAPI", func() {
					patchData := client.MergeFrom(&machinev1beta1.MachineSet{
						Spec: machinev1beta1.MachineSetSpec{
							AuthoritativeAPI: "MachineAPI",
							Template: machinev1beta1.MachineTemplateSpec{
								Spec: machinev1beta1.MachineSpec{
									AuthoritativeAPI: "MachineAPI",
								},
							},
						},
					})

					err := cl.Patch(ctx, &machinev1beta1.MachineSet{
						ObjectMeta: metav1.ObjectMeta{
							Name:      machinesetName,
							Namespace: framework.MAPINamespace,
						},
					}, patchData)
					Expect(err).ToNot(HaveOccurred(), "failed to patch mapiMachineSet")

					Eventually(func() string {
						mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
						if err != nil {
							return ""
						}
						return string(mapiMachineSet.Status.AuthoritativeAPI)
					}, framework.WaitMedium, framework.RetryMedium).Should(Equal("MachineAPI"), "change AuthoritativeAPI to MachineAPI failed")
				})

				It("should set the MAPI machine set status AuthoritativeAPI to 'MachineAPI'", func() {
					Eventually(func() machinev1beta1.MachineAuthority {
						mapiMachineSet, err := mapiframework.GetMachineSet(ctx, cl, machinesetName)
						if err != nil {
							return ""
						}
						return mapiMachineSet.Status.AuthoritativeAPI
					}, framework.WaitMedium, framework.RetryMedium).Should(Equal(machinev1beta1.MachineAuthorityMachineAPI), "mapi machineset status.AuthoritativeAPI should be MachineAPI")
				})

				It("should update the synchronized condition on the MAPI machine set to True", func() {
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
				})

				It("should update the Paused condition on the MAPI machine set to False", func() {
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
				})

				It("should update the Paused condition on the CAPI machine set to True", func() {
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
			})
		})
	})
})
