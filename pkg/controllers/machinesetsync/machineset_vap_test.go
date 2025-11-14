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

	clusterv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta1"
	awsv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	admissiontestutils "github.com/openshift/cluster-capi-operator/pkg/admissionpolicy/testutils"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
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
				InfrastructureRef: corev1.ObjectReference{
					Kind:      capaMachineTemplate.Kind,
					Name:      capaMachineTemplate.GetName(),
					Namespace: capaMachineTemplate.GetNamespace(),
				},
			},
		}

		Eventually(k8sClient.Create(ctx, capaMachineTemplate)).Should(Succeed(), "capa machine template should be able to be created")

		capiMachineSetBuilder := clusterv1resourcebuilder.MachineSet().
			WithNamespace(capiNamespace.GetName()).
			WithName("test-machineset").
			WithTemplate(capiMachineTemplate).
			WithClusterName(infrastructureName)

		capiMachineSet = capiMachineSetBuilder.Build()

		By("Loading the transport config maps")
		transportConfigMaps := admissiontestutils.LoadTransportConfigMaps()

		By("Applying the objects found in clusterAPICustomAdmissionPolicies")
		for _, obj := range transportConfigMaps[admissiontestutils.ClusterAPICustomAdmissionPolicies] {
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
			capiMachineSet.Spec.Template.Spec.Version = &testVersion

			Eventually(k8sClient.Create(ctx, capiMachineSet), timeout).Should(MatchError(ContainSubstring(".version is a forbidden field")))
		})

		It("should deny updating spec.template.spec.version on an existing MachineSet", func() {
			Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

			Eventually(k.Update(capiMachineSet, func() {
				testVersion := "1"
				capiMachineSet.Spec.Template.Spec.Version = &testVersion
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
})
