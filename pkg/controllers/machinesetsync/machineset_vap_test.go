/*
Copyright 2025 Red Hat, Inc.

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

package machinesetsync

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	clusterv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta2"
	awsv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	admissiontestutils "github.com/openshift/cluster-capi-operator/pkg/admissionpolicy/testutils"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/kube-storage-version-migrator/pkg/clients/clientset/scheme"
)

var _ = Describe("MachineSet VAP Tests", func() {
	var k komega.Komega
	var vapCleanup func()

	var capiNamespace *corev1.Namespace
	var mapiNamespace *corev1.Namespace

	var capiMachineSet *clusterv1.MachineSet
	var capiMachineSetBuilder clusterv1resourcebuilder.MachineSetBuilder
	var policyBinding *admissionregistrationv1.ValidatingAdmissionPolicyBinding
	var machineSetVap *admissionregistrationv1.ValidatingAdmissionPolicy

	BeforeEach(func() {
		k = komega.New(k8sClient)

		By("Starting the ValidatingAdmissionPolicy status controller")
		var err error
		vapCleanup, err = admissiontestutils.StartVAPStatusController(ctx, cfg, scheme.Scheme)
		Expect(err).NotTo(HaveOccurred())

		By("Setting up namespaces for the test")
		mapiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-machine-api-").Build()
		Eventually(k8sClient.Create(ctx, mapiNamespace)).Should(Succeed(), "mapi namespace should be able to be created")

		capiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-cluster-api-").Build()
		Eventually(k8sClient.Create(ctx, capiNamespace)).Should(Succeed(), "capi namespace should be able to be created")

		infrastructureName := "cluster-foo"

		By("Creating infrastructure resources")
		capaClusterBuilder := awsv1resourcebuilder.AWSCluster().
			WithNamespace(capiNamespace.GetName()).
			WithName(infrastructureName)
		Eventually(k8sClient.Create(ctx, capaClusterBuilder.Build())).Should(Succeed(), "capa cluster should be able to be created")

		capiClusterBuilder := clusterv1resourcebuilder.Cluster().
			WithNamespace(capiNamespace.GetName()).
			WithName(infrastructureName)
		Eventually(k8sClient.Create(ctx, capiClusterBuilder.Build())).Should(Succeed(), "capi cluster should be able to be created")

		capaMachineTemplateBuilder := awsv1resourcebuilder.AWSMachineTemplate().
			WithNamespace(capiNamespace.GetName()).
			WithName("foo")

		capaMachineTemplate := capaMachineTemplateBuilder.Build()

		capiMachineTemplate := clusterv1.MachineTemplateSpec{
			Spec: clusterv1.MachineSpec{
				InfrastructureRef: clusterv1.ContractVersionedObjectReference{
					Kind:     capaMachineTemplate.Kind,
					Name:     capaMachineTemplate.GetName(),
					APIGroup: awsv1.GroupVersion.Group,
				},
			},
		}

		Eventually(k8sClient.Create(ctx, capaMachineTemplate)).Should(Succeed(), "capa machine template should be able to be created")

		capiMachineSetBuilder = clusterv1resourcebuilder.MachineSet().
			WithNamespace(capiNamespace.GetName()).
			WithName("test-machineset").
			WithTemplate(capiMachineTemplate).
			WithClusterName(infrastructureName)

		capiMachineSet = capiMachineSetBuilder.Build()

		By("Loading admission policy profiles")
		admissionPolicies := admissiontestutils.LoadAdmissionPolicyProfiles()

		By("Applying the default admission policies")
		for _, obj := range admissionPolicies[admissiontestutils.DefaultProfile] {
			newObj, ok := obj.DeepCopyObject().(client.Object)
			Expect(ok).To(BeTrue())

			Eventually(func() error {
				err := k8sClient.Create(ctx, newObj)
				if err != nil && !apierrors.IsAlreadyExists(err) {
					return err
				}

				return nil
			}, timeout).Should(Succeed())
		}
	})

	AfterEach(func() {
		By("Stopping VAP status controller")
		if vapCleanup != nil {
			vapCleanup()
		}

		By("Cleaning up VAPs and bindings")
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, "",
			&admissionregistrationv1.ValidatingAdmissionPolicy{},
			&admissionregistrationv1.ValidatingAdmissionPolicyBinding{},
		)

		By("Cleaning up MAPI test resources")
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, mapiNamespace.GetName(),
			&mapiv1beta1.Machine{},
			&mapiv1beta1.MachineSet{},
		)

		By("Cleaning up CAPI test resources")
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, capiNamespace.GetName(),
			&clusterv1.Machine{},
			&clusterv1.MachineSet{},
			&awsv1.AWSCluster{},
			&awsv1.AWSMachineTemplate{},
		)
	})

	Context("Prevent setting of CAPI fields that are not supported by MAPI", func() {
		BeforeEach(func() {
			By("Waiting for VAP to be ready")
			machineSetVap = &admissionregistrationv1.ValidatingAdmissionPolicy{}
			Eventually(k8sClient.Get(ctx, client.ObjectKey{Name: "openshift-cluster-api-prevent-setting-of-capi-fields-unsupported-by-mapi"}, machineSetVap), timeout).Should(Succeed())
			Eventually(k.Update(machineSetVap, func() {
				admissiontestutils.AddSentinelValidation(machineSetVap)
			})).Should(Succeed())

			Eventually(k.Object(machineSetVap), timeout).Should(
				HaveField("Status.ObservedGeneration", BeNumerically(">=", 2)),
			)

			By("Updating the VAP binding")
			policyBinding = &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
			Eventually(k8sClient.Get(ctx, client.ObjectKey{
				Name: "openshift-cluster-api-prevent-setting-of-capi-fields-unsupported-by-mapi"}, policyBinding), timeout).Should(Succeed())

			Eventually(k.Update(policyBinding, func() {
				admissiontestutils.UpdateVAPBindingNamespaces(policyBinding, "", capiNamespace.GetName())
			}), timeout).Should(Succeed())

			Eventually(k.Object(policyBinding), timeout).Should(
				SatisfyAll(
					HaveField("Spec.MatchResources.NamespaceSelector.MatchLabels",
						HaveKeyWithValue("kubernetes.io/metadata.name",
							capiNamespace.GetName())),
				),
			)

			By("Creating a sentinel MachineSet to verify VAP is enforcing")
			sentinelMachineSet := clusterv1resourcebuilder.MachineSet().
				WithName("sentinel-machineset").
				WithNamespace(capiNamespace.Name).
				WithTemplate(clusterv1.MachineTemplateSpec{
					Spec: clusterv1.MachineSpec{
						ProviderID: "force-having-a-spec",
					},
				}).
				Build()
			Eventually(k8sClient.Create(ctx, sentinelMachineSet)).Should(Succeed(), "sentinel machineset should be able to be created")

			admissiontestutils.VerifySentinelValidation(k, sentinelMachineSet, timeout)
		})

		It("should allow creating a MachineSet without forbidden fields", func() {
			Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())
		})

		It("should allow updating a MachineSet without changing forbidden fields", func() {
			Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

			Eventually(k.Update(capiMachineSet, func() {
				replicas := int32(3)
				capiMachineSet.Spec.Replicas = &replicas
			}), timeout).Should(Succeed())
		})

		It("should deny creating a MachineSet with spec.template.spec.version", func() {
			testVersion := "1"
			capiMachineSet.Spec.Template.Spec.Version = testVersion

			Eventually(k8sClient.Create(ctx, capiMachineSet), timeout).Should(MatchError(ContainSubstring(".version is a forbidden field")))
		})

		It("should deny updating spec.template.spec.version on an existing MachineSet", func() {
			Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

			Eventually(k.Update(capiMachineSet, func() {
				testVersion := "1"
				capiMachineSet.Spec.Template.Spec.Version = testVersion
			}), timeout).Should(MatchError(ContainSubstring(".version is a forbidden field")))
		})

		It("should deny creating a MachineSet with spec.template.spec.readinessGates", func() {
			capiMachineSet.Spec.Template.Spec.ReadinessGates = []clusterv1.MachineReadinessGate{{ConditionType: "foo"}}

			Eventually(k8sClient.Create(ctx, capiMachineSet), timeout).Should(MatchError(ContainSubstring(".readinessGates is a forbidden field")))
		})

		It("should deny updating spec.template.spec.readinessGates on an existing MachineSet", func() {
			Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

			Eventually(k.Update(capiMachineSet, func() {
				capiMachineSet.Spec.Template.Spec.ReadinessGates = []clusterv1.MachineReadinessGate{{ConditionType: "foo"}}
			}), timeout).Should(MatchError(ContainSubstring(".readinessGates is a forbidden field")))
		})
	})

	Context("Prevent authoritative MAPI MachineSet creation when same-named CAPI MachineSet exists", func() {
		var mapiMachineSetBuilder machinev1resourcebuilder.MachineSetBuilder
		const vapName string = "openshift-prevent-authoritative-mapi-machineset-create-when-capi-exists"

		BeforeEach(func() {
			By("Waiting for VAP to be ready")
			machineSetVap = &admissionregistrationv1.ValidatingAdmissionPolicy{}
			Eventually(k8sClient.Get(ctx, client.ObjectKey{Name: vapName}, machineSetVap), timeout).Should(Succeed())

			// Add UPDATE operation for easier testing (same as Machine tests)
			resourceRules := machineSetVap.Spec.MatchConstraints.ResourceRules
			Expect(resourceRules).To(HaveLen(1))
			resourceRules[0].Operations = append(resourceRules[0].Operations, admissionregistrationv1.Update)

			Eventually(k.Update(machineSetVap, func() {
				admissiontestutils.AddSentinelValidation(machineSetVap)
				machineSetVap.Spec.MatchConstraints.ResourceRules = resourceRules
			})).Should(Succeed())

			Eventually(k.Object(machineSetVap), timeout).Should(
				HaveField("Status.ObservedGeneration", BeNumerically(">=", 2)),
			)

			By("Updating the VAP binding")
			policyBinding = &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
			Eventually(k8sClient.Get(ctx, client.ObjectKey{
				Name: vapName}, policyBinding), timeout).Should(Succeed())

			Eventually(k.Update(policyBinding, func() {
				// paramNamespace=capiNamespace (CAPI resources are params)
				// targetNamespace=mapiNamespace (MAPI resources are validated)
				admissiontestutils.UpdateVAPBindingNamespaces(policyBinding, capiNamespace.GetName(), mapiNamespace.GetName())
			}), timeout).Should(Succeed())

			// Wait until the binding shows the patched values
			Eventually(k.Object(policyBinding), timeout).Should(
				SatisfyAll(
					HaveField("Spec.MatchResources.NamespaceSelector.MatchLabels",
						HaveKeyWithValue("kubernetes.io/metadata.name",
							mapiNamespace.GetName())),
				),
			)

			By("Creating throwaway MachineSet pair for sentinel validation")
			mapiMachineSetBuilder = machinev1resourcebuilder.MachineSet().
				WithNamespace(mapiNamespace.Name)

			sentinelMachineSet := machinev1resourcebuilder.MachineSet().
				WithNamespace(mapiNamespace.Name).
				WithName("sentinel-machineset").
				WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
				Build()
			Eventually(k8sClient.Create(ctx, sentinelMachineSet), timeout).Should(Succeed())

			capiSentinelMachineSet := clusterv1resourcebuilder.MachineSet().
				WithName("sentinel-machineset").
				WithNamespace(capiNamespace.Name).
				WithTemplate(clusterv1.MachineTemplateSpec{
					Spec: clusterv1.MachineSpec{
						ProviderID: "force-having-a-spec",
					},
				}).
				Build()
			Eventually(k8sClient.Create(ctx, capiSentinelMachineSet)).Should(Succeed())

			Eventually(k.Get(capiSentinelMachineSet)).Should(Succeed())

			admissiontestutils.VerifySentinelValidation(k, sentinelMachineSet, timeout)
		})

		It("Does not allow creation of a MAPI MachineSet with spec.authoritativeAPI: MachineAPI and the same name", func() {
			By("Create the CAPI MachineSet")
			Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

			By("Create the MAPI MachineSet")
			newMapiMachineSet := mapiMachineSetBuilder.
				WithName("test-machineset").
				WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
				Build()
			Eventually(k8sClient.Create(ctx, newMapiMachineSet), timeout).Should(
				MatchError(ContainSubstring("with spec.authoritativeAPI: MachineAPI because a Cluster API MachineSet with the same name already exists.")))
		})

		It("Does allow creation of a MAPI machineset with authoritative API ClusterAPI and the same name", func() {
			By("Create the CAPI MachineSet")
			Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

			By("Create the MAPI MachineSet")
			newMapiMachineSet := mapiMachineSetBuilder.
				WithName("test-machineset").
				WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
				Build()
			Eventually(k8sClient.Create(ctx, newMapiMachineSet), timeout).Should(Succeed())
		})

		It("Does allow creation of a MAPI MachineSet when no matching CAPI MachineSet exists (parameterNotFoundAction)", func() {
			By("Create the MAPI MachineSet without creating a CAPI MachineSet first")
			newMapiMachineSet := mapiMachineSetBuilder.
				WithName("no-capi-equivalent").
				WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
				Build()
			Eventually(k8sClient.Create(ctx, newMapiMachineSet), timeout).Should(Succeed())
		})
	})

	Context("Prevent changes to non-authoritative MAPI MachineSets except from sync controller", func() {
		var mapiMachineSetBuilder machinev1resourcebuilder.MachineSetBuilder
		var mapiMachineSet *mapiv1beta1.MachineSet

		const (
			vapName                    string = "machine-api-machine-set-vap"
			testLabelValue             string = "test-value"
			errMsgProtectedLabels      string = "Cannot add, modify or delete any machine.openshift.io/*, kubernetes.io/* or cluster.x-k8s.io/* label"
			errMsgProtectedAnnotations string = "Cannot add, modify or delete any machine.openshift.io/* or cluster.x-k8s.io/* or clusters.x-k8s.io/* annotation"
		)

		BeforeEach(func() {
			By("Waiting for VAP to be ready")
			machineSetVap = &admissionregistrationv1.ValidatingAdmissionPolicy{}
			Eventually(k8sClient.Get(ctx, client.ObjectKey{Name: vapName}, machineSetVap), timeout).Should(Succeed())

			Eventually(k.Update(machineSetVap, func() {
				admissiontestutils.AddSentinelValidation(machineSetVap)
			})).Should(Succeed())

			Eventually(k.Object(machineSetVap), timeout).Should(
				HaveField("Status.ObservedGeneration", BeNumerically(">=", 2)),
			)

			By("Updating the VAP binding")
			policyBinding = &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
			Eventually(k8sClient.Get(ctx, client.ObjectKey{
				Name: vapName}, policyBinding), timeout).Should(Succeed())

			Eventually(k.Update(policyBinding, func() {
				// paramNamespace=capiNamespace (CAPI resources are params)
				// targetNamespace=mapiNamespace (MAPI resources are validated)
				admissiontestutils.UpdateVAPBindingNamespaces(policyBinding, capiNamespace.GetName(), mapiNamespace.GetName())
			}), timeout).Should(Succeed())

			// Wait until the binding shows the patched values
			Eventually(k.Object(policyBinding), timeout).Should(
				SatisfyAll(
					HaveField("Spec.MatchResources.NamespaceSelector.MatchLabels",
						HaveKeyWithValue("kubernetes.io/metadata.name",
							mapiNamespace.GetName())),
				),
			)

			By("Creating throwaway MachineSet pair for sentinel validation")
			sentinelMachineSet := machinev1resourcebuilder.MachineSet().
				WithNamespace(mapiNamespace.Name).
				WithName("sentinel-machineset").
				WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
				Build()
			Eventually(k8sClient.Create(ctx, sentinelMachineSet), timeout).Should(Succeed())

			Eventually(k.UpdateStatus(sentinelMachineSet, func() {
				sentinelMachineSet.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityClusterAPI
			})).Should(Succeed())

			Eventually(k.Object(sentinelMachineSet), timeout).Should(
				HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)))

			capiSentinelMachineSet := clusterv1resourcebuilder.MachineSet().
				WithName("sentinel-machineset").
				WithNamespace(capiNamespace.Name).
				WithTemplate(clusterv1.MachineTemplateSpec{
					Spec: clusterv1.MachineSpec{
						ProviderID: "force-having-a-spec",
					},
				}).
				Build()
			Eventually(k8sClient.Create(ctx, capiSentinelMachineSet)).Should(Succeed())

			Eventually(k.Get(capiSentinelMachineSet)).Should(Succeed())

			admissiontestutils.VerifySentinelValidation(k, sentinelMachineSet, timeout)

			By("Creating a shared machineset pair to be used across the tests")
			mapiMachineSetBuilder = machinev1resourcebuilder.MachineSet().
				WithNamespace(mapiNamespace.Name).
				WithName(capiMachineSet.Name).
				WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(nil)).
				WithLabels(map[string]string{
					"machine.openshift.io/cluster-api-cluster": "ci-op-gs2k97d6-c9e33-2smph",
					"mapi-param-controlled-label":              "param-controlled-key",
				}).WithAnnotations(map[string]string{
				"capacity.cluster-autoscaler.kubernetes.io/labels": "kubernetes.io/arch=amd64",
				"machine.openshift.io/GPU":                         "0",
				"machine.openshift.io/memoryMb":                    "16384",
				"machine.openshift.io/vCPU":                        "4",
			})
			mapiMachineSet = mapiMachineSetBuilder.Build()
			Eventually(k8sClient.Create(ctx, mapiMachineSet), timeout).Should(Succeed())

			capiMachineSet = capiMachineSetBuilder.WithLabels(map[string]string{
				"machine.openshift.io/cluster-api-cluster": "ci-op-gs2k97d6-c9e33-2smph",

				"capi-param-controlled-label": "param-controlled-key",
			}).WithAnnotations(map[string]string{
				"capacity.cluster-autoscaler.kubernetes.io/labels": "kubernetes.io/arch=amd64",
			}).Build()

			Eventually(k8sClient.Create(ctx, capiMachineSet), timeout).Should(Succeed())

		})

		Context("with status.AuthoritativeAPI: Machine API", func() {
			BeforeEach(func() {
				By("Setting the MAPI machine set AuthoritativeAPI to Machine API")
				Eventually(k.UpdateStatus(mapiMachineSet, func() {
					mapiMachineSet.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI
				})).Should(Succeed())

				Eventually(k.Object(mapiMachineSet), timeout).Should(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)))
			})

			It("updating the spec should be allowed", func() {
				Eventually(k.Update(mapiMachineSet, func() {
					mapiMachineSet.Spec.MinReadySeconds = int32(2)
				}), timeout).Should(Succeed(), "expected success when updating the spec")
			})

		})

		Context("with status.AuthoritativeAPI: ClusterAPI", func() {
			BeforeEach(func() {
				By("Setting the MAPI machine AuthoritativeAPI to Cluster API")
				Eventually(k.UpdateStatus(mapiMachineSet, func() {
					mapiMachineSet.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityClusterAPI
				})).Should(Succeed())

				Eventually(k.Object(mapiMachineSet), timeout).Should(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)))
			})

			It("updating the spec (outside of authoritative api) should be prevented", func() {
				Eventually(k.Update(mapiMachineSet, func() {
					mapiMachineSet.Spec.MinReadySeconds = int32(2)
				}), timeout).Should(MatchError(ContainSubstring("You may only modify spec.authoritativeAPI")))
			})

			It("updating spec.template should be prevented", func() {
				Eventually(k.Update(mapiMachineSet, func() {
					mapiMachineSet.Spec.Template.Labels = map[string]string{"new-label": testLabelValue}
				}), timeout).Should(MatchError(ContainSubstring("You may only modify spec.authoritativeAPI")))
			})

			It("updating the spec.authoritativeAPI should be allowed", func() {
				Eventually(k.Update(mapiMachineSet, func() {
					mapiMachineSet.Spec.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI
				}), timeout).Should(Succeed(), "expected success when updating spec.authoritativeAPI")
			})

			Context("when trying to update metadata.labels", func() {
				It("rejects modification of the protected machine.openshift.io label", func() {
					Eventually(k.Update(mapiMachineSet, func() {
						mapiMachineSet.Labels["machine.openshift.io/cluster-api-cluster"] = testLabelValue
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedLabels)))
				})

				It("rejects deletion of the protected machine.openshift.io label", func() {
					Eventually(k.Update(mapiMachineSet, func() {
						delete(mapiMachineSet.Labels, "machine.openshift.io/cluster-api-cluster")
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedLabels)))
				})

				It("rejects setting of the protected machine.openshift.io label to the empty string ''", func() {
					Eventually(k.Update(mapiMachineSet, func() {
						mapiMachineSet.Labels["machine.openshift.io/cluster-api-cluster"] = ""
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedLabels)))
				})

				It("rejects adding a new machine.openshift.io label", func() {
					Eventually(k.Update(mapiMachineSet, func() {
						mapiMachineSet.Labels["machine.openshift.io/foo"] = testLabelValue
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedLabels)))
				})

				It("rejects adding a new machine.openshift.io label with an empty string value", func() {
					Eventually(k.Update(mapiMachineSet, func() {
						mapiMachineSet.Labels["machine.openshift.io/foo"] = ""
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedLabels)))
				})

				It("allows modification of a non-protected label", func() {
					Eventually(k.Update(mapiMachineSet, func() {
						mapiMachineSet.Labels["test"] = "val"
					}), timeout).Should(Succeed(), "expected success when modifying unrelated labels")
				})
			})

			Context("when trying to update metadata.Annotations", func() {
				It("rejects modification of a protected machine.openshift.io annotation", func() {
					Eventually(k.Update(mapiMachineSet, func() {
						mapiMachineSet.Annotations["machine.openshift.io/vCPU"] = testLabelValue
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedAnnotations)))
				})

				It("rejects deletion of a protected machine.openshift.io annotation", func() {
					Eventually(k.Update(mapiMachineSet, func() {
						delete(mapiMachineSet.Annotations, "machine.openshift.io/vCPU")
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedAnnotations)))
				})

				It("rejects modification of a protected machine.openshift.io annotation to the empty string ''", func() {
					Eventually(k.Update(mapiMachineSet, func() {
						mapiMachineSet.Annotations["machine.openshift.io/vCPU"] = ""
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedAnnotations)))
				})

				It("rejects adding a new protected machine.openshift.io annotation", func() {
					Eventually(k.Update(mapiMachineSet, func() {
						mapiMachineSet.Annotations["machine.openshift.io/foo"] = testLabelValue
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedAnnotations)))
				})

				It("rejects adding a new protected machine.openshift.io annotation with an empty string value", func() {
					Eventually(k.Update(mapiMachineSet, func() {
						mapiMachineSet.Annotations["machine.openshift.io/foo"] = ""
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedAnnotations)))
				})

				It("allows modification of a non-protected annotation", func() {
					Eventually(k.Update(mapiMachineSet, func() {
						mapiMachineSet.Annotations["bar"] = "baz"
					}), timeout).Should(Succeed(), "expected success when modifying unrelated annotations")
				})
			})

			Context("when trying to update Cluster API owned metadata.labels", func() {
				It("allows changing a metadata label to match the param machine", func() {
					Eventually(k.Object(capiMachineSet), timeout).Should(
						HaveField("Labels", HaveKeyWithValue("capi-param-controlled-label", "param-controlled-key")))

					Eventually(k.Update(mapiMachineSet, func() {
						mapiMachineSet.Labels["capi-param-controlled-label"] = "param-controlled-key"
					}), timeout).Should(Succeed(), "expected success when updating label to match CAPI machine")
				})

				It("rejects changing a label to differ from the param machine", func() {
					Eventually(k.Update(mapiMachineSet, func() {
						mapiMachineSet.Labels["capi-param-controlled-label"] = "foo"
					}), timeout).Should(MatchError(ContainSubstring("Cannot modify a Cluster API controlled label except to match the Cluster API mirrored MachineSet")))
				})
			})

			It("rejects updating spec.authoritativeAPI alongside other spec fields", func() {
				Eventually(k.Update(mapiMachineSet, func() {
					mapiMachineSet.Spec.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI
					mapiMachineSet.Spec.Template.Labels = map[string]string{"foo": testLabelValue}
				}), timeout).Should(MatchError(ContainSubstring("You may only modify spec.authoritativeAPI")))

			})

		})
	})

	Context("Validate creation of CAPI machine sets", func() {
		var vapName = "openshift-validate-capi-machine-set-creation"
		var mapiMachineSetBuilder machinev1resourcebuilder.MachineSetBuilder

		BeforeEach(func() {
			By("Waiting for VAP to be ready")
			machineSetVap = &admissionregistrationv1.ValidatingAdmissionPolicy{}
			Eventually(k8sClient.Get(ctx, client.ObjectKey{Name: vapName}, machineSetVap), timeout).Should(Succeed())
			resourceRules := machineSetVap.Spec.MatchConstraints.ResourceRules
			Expect(resourceRules).To(HaveLen(1))
			resourceRules[0].Operations = append(resourceRules[0].Operations, admissionregistrationv1.Update)
			Eventually(k.Update(machineSetVap, func() {
				admissiontestutils.AddSentinelValidation(machineSetVap)
				// Updating the VAP so that it functions on "UPDATE" as well as "CREATE" only in this test suite to make it easier to test the functionality
				machineSetVap.Spec.MatchConstraints.ResourceRules = resourceRules

			})).Should(Succeed())

			Eventually(k.Object(machineSetVap), timeout).Should(
				HaveField("Status.ObservedGeneration", BeNumerically(">=", 2)),
			)

			By("Updating the VAP binding")
			policyBinding = &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
			Eventually(k8sClient.Get(ctx, client.ObjectKey{
				Name: vapName}, policyBinding), timeout).Should(Succeed())

			Eventually(k.Update(policyBinding, func() {
				admissiontestutils.UpdateVAPBindingNamespaces(policyBinding, mapiNamespace.GetName(), capiNamespace.GetName())
			}), timeout).Should(Succeed())

			// Wait until the binding shows the patched values
			Eventually(k.Object(policyBinding), timeout).Should(
				SatisfyAll(
					HaveField("Spec.MatchResources.NamespaceSelector.MatchLabels",
						HaveKeyWithValue("kubernetes.io/metadata.name",
							capiNamespace.GetName())),
				),
			)

			By("Configuring the MAPI MachineSet Builder")
			mapiMachineSetBuilder = machinev1resourcebuilder.MachineSet().WithNamespace(mapiNamespace.Name)

			By("Creating a throwaway MAPI machine set")
			sentinelMachineSet := mapiMachineSetBuilder.WithName("sentinel-machineset").WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).Build()
			Eventually(k8sClient.Create(ctx, sentinelMachineSet), timeout).Should(Succeed())

			capiSentinelMachine := clusterv1resourcebuilder.MachineSet().WithName("sentinel-machineset").WithNamespace(capiNamespace.Name).Build()
			Eventually(k8sClient.Create(ctx, capiSentinelMachine)).Should(Succeed())

			Eventually(k.Get(capiSentinelMachine)).Should(Succeed())

			admissiontestutils.VerifySentinelValidation(k, capiSentinelMachine, timeout)
		})

		Context("when no MAPI machineset exists with the same name", func() {
			It("allows CAPI machineset creation (parameterNotFoundAction=Allow)", func() {
				By("Creating a CAPI machineset without a corresponding MAPI machineset")
				newCapiMachineSet := clusterv1resourcebuilder.MachineSet().
					WithName("no-mapi-counterpart").
					WithNamespace(capiNamespace.Name).
					Build()
				Eventually(k8sClient.Create(ctx, newCapiMachineSet)).Should(Succeed())
			})
		})

		Context("when MAPI machineset has status.authoritativeAPI=MachineAPI", func() {
			BeforeEach(func() {
				By("Creating MAPI machineset with authoritativeAPI=MachineAPI")
				mapiMachineSet := mapiMachineSetBuilder.WithName("validation-machineset").Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				Eventually(k.UpdateStatus(mapiMachineSet, func() {
					mapiMachineSet.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI
				})).Should(Succeed())
			})

			It("denies creating unpaused CAPI machineset", func() {
				By("Attempting to create CAPI machineset without pause annotation or condition")
				newCapiMachineSet := clusterv1resourcebuilder.MachineSet().
					WithName("validation-machineset").
					WithNamespace(capiNamespace.Name).
					Build()

				Eventually(k8sClient.Create(ctx, newCapiMachineSet), timeout).Should(
					MatchError(ContainSubstring("in an un-paused state")))
			})

			It("allows creating CAPI machineset with paused annotation", func() {
				By("Creating CAPI machineset with paused annotation")
				newCapiMachineSet := clusterv1resourcebuilder.MachineSet().
					WithName("validation-machineset").
					WithNamespace(capiNamespace.Name).
					WithAnnotations(map[string]string{
						clusterv1.PausedAnnotation: "",
					}).
					Build()

				Eventually(k8sClient.Create(ctx, newCapiMachineSet)).Should(Succeed())
			})
		})

		Context("when MAPI machineset has status.authoritativeAPI=ClusterAPI", func() {
			var mapiMachineSet *mapiv1beta1.MachineSet
			BeforeEach(func() {
				By("Creating MAPI machineset with authoritativeAPI=ClusterAPI")
				mapiMachineSet = mapiMachineSetBuilder.WithName("validation-machineset").Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				Eventually(k.UpdateStatus(mapiMachineSet, func() {
					mapiMachineSet.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityClusterAPI
				})).Should(Succeed())
			})

			It("denies creation when MAPI machineset is not paused", func() {
				By("Attempting to create CAPI machineset when MAPI machineset has no Paused condition")
				newCapiMachineSet := clusterv1resourcebuilder.MachineSet().
					WithName("validation-machineset").
					WithNamespace(capiNamespace.Name).
					Build()

				Eventually(k8sClient.Create(ctx, newCapiMachineSet), timeout).Should(
					MatchError(ContainSubstring("already exists and is not paused")))
			})

			It("allows creation when MAPI machineset has Paused condition", func() {
				By("Setting Paused condition on the MAPI machineset")
				Eventually(k.UpdateStatus(mapiMachineSet, func() {
					mapiMachineSet.Status.Conditions = []mapiv1beta1.Condition{{
						Type:               "Paused",
						Status:             corev1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
					}}
				})).Should(Succeed())

				By("Creating CAPI machineset")
				newCapiMachineSet := clusterv1resourcebuilder.MachineSet().
					WithName("validation-machineset").
					WithNamespace(capiNamespace.Name).
					Build()

				Eventually(k8sClient.Create(ctx, newCapiMachineSet)).Should(Succeed())
			})
		})
	})

	Context("Prevent changes to non-authoritative CAPI MachineSets except from sync controller", func() {
		var mapiMachineSetBuilder machinev1resourcebuilder.MachineSetBuilder
		var mapiMachineSet *mapiv1beta1.MachineSet

		const (
			vapName                    string = "cluster-api-machine-set-vap"
			testLabelValue             string = "test-value"
			errMsgProtectedLabels      string = "Cannot add, modify or delete any machine.openshift.io/*, kubernetes.io/* or cluster.x-k8s.io/* label"
			errMsgProtectedAnnotations string = "Cannot add, modify or delete any machine.openshift.io/* or cluster.x-k8s.io/* or clusters.x-k8s.io/* annotation"
		)

		BeforeEach(func() {
			By("Waiting for VAP to be ready")
			machineSetVap = &admissionregistrationv1.ValidatingAdmissionPolicy{}
			Eventually(k8sClient.Get(ctx, client.ObjectKey{Name: vapName}, machineSetVap), timeout).Should(Succeed())

			Eventually(k.Update(machineSetVap, func() {
				admissiontestutils.AddSentinelValidation(machineSetVap)
			})).Should(Succeed())

			Eventually(k.Object(machineSetVap), timeout).Should(
				HaveField("Status.ObservedGeneration", BeNumerically(">=", 2)),
			)

			By("Updating the VAP binding")
			policyBinding = &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
			Eventually(k8sClient.Get(ctx, client.ObjectKey{
				Name: vapName}, policyBinding), timeout).Should(Succeed())

			Eventually(k.Update(policyBinding, func() {
				// paramNamespace=mapiNamespace (MAPI resources are params)
				// targetNamespace=capiNamespace (CAPI resources are validated)
				admissiontestutils.UpdateVAPBindingNamespaces(policyBinding, mapiNamespace.GetName(), capiNamespace.GetName())
			}), timeout).Should(Succeed())

			// Wait until the binding shows the patched values
			Eventually(k.Object(policyBinding), timeout).Should(
				SatisfyAll(
					HaveField("Spec.MatchResources.NamespaceSelector.MatchLabels",
						HaveKeyWithValue("kubernetes.io/metadata.name",
							capiNamespace.GetName())),
				),
			)

			By("Creating throwaway MachineSet pair for sentinel validation")
			sentinelMachineSet := machinev1resourcebuilder.MachineSet().
				WithNamespace(mapiNamespace.Name).
				WithName("sentinel-machineset").
				WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
				Build()
			Eventually(k8sClient.Create(ctx, sentinelMachineSet), timeout).Should(Succeed())

			Eventually(k.UpdateStatus(sentinelMachineSet, func() {
				sentinelMachineSet.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI
			})).Should(Succeed())

			Eventually(k.Object(sentinelMachineSet), timeout).Should(
				HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)))

			capiSentinelMachineSet := clusterv1resourcebuilder.MachineSet().
				WithName("sentinel-machineset").
				WithNamespace(capiNamespace.Name).
				WithTemplate(clusterv1.MachineTemplateSpec{
					Spec: clusterv1.MachineSpec{
						ProviderID: "force-having-a-spec",
					},
				}).
				Build()
			Eventually(k8sClient.Create(ctx, capiSentinelMachineSet)).Should(Succeed())

			Eventually(k.Get(capiSentinelMachineSet)).Should(Succeed())

			admissiontestutils.VerifySentinelValidation(k, capiSentinelMachineSet, timeout)

			By("Creating a shared machineset pair to be used across the tests")
			mapiMachineSetBuilder = machinev1resourcebuilder.MachineSet().
				WithNamespace(mapiNamespace.Name).
				WithName(capiMachineSet.Name).
				WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(nil)).
				WithLabels(map[string]string{
					"machine.openshift.io/cluster-api-cluster": "ci-op-gs2k97d6-c9e33-2smph",
					"mapi-param-controlled-label":              "param-controlled-key",
				}).WithAnnotations(map[string]string{
				"capacity.cluster-autoscaler.kubernetes.io/labels": "kubernetes.io/arch=amd64",
				"machine.openshift.io/GPU":                         "0",
				"machine.openshift.io/memoryMb":                    "16384",
				"machine.openshift.io/vCPU":                        "4",
			})
			mapiMachineSet = mapiMachineSetBuilder.Build()
			Eventually(k8sClient.Create(ctx, mapiMachineSet), timeout).Should(Succeed())

			capiMachineSet = capiMachineSetBuilder.WithLabels(map[string]string{
				"machine.openshift.io/cluster-api-cluster": "ci-op-gs2k97d6-c9e33-2smph",
				"cluster.x-k8s.io/cluster-name":            "ci-op-gs2k97d6-c9e33-2smph",

				"capi-param-controlled-label": "param-controlled-key",
			}).WithAnnotations(map[string]string{
				"capacity.cluster-autoscaler.kubernetes.io/labels": "kubernetes.io/arch=amd64",
				"machine.openshift.io/GPU":                         "0",
				"machine.openshift.io/memoryMb":                    "16384",
				"machine.openshift.io/vCPU":                        "4",
			}).Build()

			Eventually(k8sClient.Create(ctx, capiMachineSet), timeout).Should(Succeed())

		})
		Context("with status.authoritativeAPI: Machine API (on MAPI MachineSet)", func() {
			BeforeEach(func() {
				By("Setting the MAPI MachineSet AuthoritativeAPI to Machine API")
				Eventually(k.UpdateStatus(mapiMachineSet, func() {
					mapiMachineSet.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI
				})).Should(Succeed())

				Eventually(k.Object(mapiMachineSet), timeout).Should(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)))
			})

			It("updating the spec should be prevented", func() {
				Eventually(k.Update(capiMachineSet, func() {
					replicas := int32(5)
					capiMachineSet.Spec.Replicas = &replicas
				}), timeout).Should(MatchError(ContainSubstring("Changing .spec is not allowed")))
			})

			It("updating the spec.AuthoritativeAPI (on the MAPI MachineSet) should be allowed", func() {
				Eventually(k.Update(mapiMachineSet, func() {
					mapiMachineSet.Spec.AuthoritativeAPI = mapiv1beta1.MachineAuthorityClusterAPI
				}), timeout).Should(Succeed())
			})

			Context("when trying to update metadata.labels", func() {
				It("rejects modification of the protected machine.openshift.io label", func() {
					Eventually(k.Update(capiMachineSet, func() {
						capiMachineSet.Labels["machine.openshift.io/cluster-api-cluster"] = "different-cluster"
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedLabels)))
				})

				It("rejects deletion of the protected machine.openshift.io label", func() {
					Eventually(k.Update(capiMachineSet, func() {
						delete(capiMachineSet.Labels, "machine.openshift.io/cluster-api-cluster")
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedLabels)))
				})

				It("rejects setting of the protected machine.openshift.io label to the empty string ''", func() {
					Eventually(k.Update(capiMachineSet, func() {
						capiMachineSet.Labels["machine.openshift.io/cluster-api-cluster"] = ""
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedLabels)))
				})

				It("rejects adding a new machine.openshift.io label", func() {
					Eventually(k.Update(capiMachineSet, func() {
						capiMachineSet.Labels["machine.openshift.io/foo"] = testLabelValue
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedLabels)))
				})

				It("rejects adding a new machine.openshift.io label with an empty string value", func() {
					Eventually(k.Update(capiMachineSet, func() {
						capiMachineSet.Labels["machine.openshift.io/foo"] = ""
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedLabels)))
				})

				It("rejects modification of the protected cluster.x-k8s.io label", func() {
					Eventually(k.Update(capiMachineSet, func() {
						capiMachineSet.Labels["cluster.x-k8s.io/cluster-name"] = "different-cluster"
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedLabels)))
				})

				It("rejects deletion of the protected cluster.x-k8s.io label", func() {
					Eventually(k.Update(capiMachineSet, func() {
						delete(capiMachineSet.Labels, "cluster.x-k8s.io/cluster-name")
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedLabels)))
				})

				It("allows modification of a non-protected label", func() {
					Eventually(k.Update(capiMachineSet, func() {
						capiMachineSet.Labels["test"] = "val"
					}), timeout).Should(Succeed(), "expected success when modifying unrelated labels")
				})
			})

			Context("when trying to update metadata.Annotations", func() {
				It("rejects modification of a protected machine.openshift.io annotation", func() {
					Eventually(k.Update(capiMachineSet, func() {
						capiMachineSet.Annotations["machine.openshift.io/vCPU"] = "8"
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedAnnotations)))
				})

				It("rejects deletion of a protected machine.openshift.io annotation", func() {
					Eventually(k.Update(capiMachineSet, func() {
						delete(capiMachineSet.Annotations, "machine.openshift.io/vCPU")
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedAnnotations)))
				})

				It("rejects modification of a protected machine.openshift.io annotation to the empty string ''", func() {
					Eventually(k.Update(capiMachineSet, func() {
						capiMachineSet.Annotations["machine.openshift.io/vCPU"] = ""
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedAnnotations)))
				})

				It("rejects adding a new protected machine.openshift.io annotation", func() {
					Eventually(k.Update(capiMachineSet, func() {
						capiMachineSet.Annotations["machine.openshift.io/foo"] = testLabelValue
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedAnnotations)))
				})

				It("rejects adding a new protected machine.openshift.io annotation with an empty string value", func() {
					Eventually(k.Update(capiMachineSet, func() {
						capiMachineSet.Annotations["machine.openshift.io/foo"] = ""
					}), timeout).Should(MatchError(ContainSubstring(errMsgProtectedAnnotations)))
				})

				It("allows modification of a non-protected annotation", func() {
					Eventually(k.Update(capiMachineSet, func() {
						capiMachineSet.Annotations["bar"] = "baz"
					}), timeout).Should(Succeed(), "expected success when modifying unrelated annotations")
				})
			})

			Context("when trying to update Machine API owned metadata.labels", func() {
				It("allows changing a metadata label to match the param MachineSet", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Labels", HaveKeyWithValue("mapi-param-controlled-label", "param-controlled-key")))

					Eventually(k.Update(capiMachineSet, func() {
						capiMachineSet.Labels["mapi-param-controlled-label"] = "param-controlled-key"
					}), timeout).Should(Succeed(), "expected success when updating label to match MAPI MachineSet")
				})

				It("rejects changing a label to differ from the param MachineSet", func() {
					Eventually(k.Update(capiMachineSet, func() {
						capiMachineSet.Labels["mapi-param-controlled-label"] = testLabelValue
					}), timeout).Should(MatchError(ContainSubstring("Cannot modify a Machine API controlled label except to match the Machine API mirrored MachineSet")))
				})
			})
		})
	})
})
