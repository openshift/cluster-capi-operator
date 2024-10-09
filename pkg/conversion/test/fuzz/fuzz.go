/*
Copyright 2024 Red Hat, Inc.

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
package fuzz

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	fuzz "github.com/google/gofuzz"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/apitesting/fuzzer"
	metafuzzer "k8s.io/apimachinery/pkg/apis/meta/fuzzer"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const powerVSMachineKind = "IBMPowerVSMachine"

// CAPI2MAPIMachineConverterConstructor is a function that constructs a CAPI to MAPI Machine converter.
// Since the CAPI to MAPI conversion relies on different types, it is expected that the constructor is wrapped in a closure
// that handles type assertions to fit the interface.
type CAPI2MAPIMachineConverterConstructor func(*capiv1.Machine, client.Object, client.Object) capi2mapi.MachineAndInfrastructureMachine

// CAPI2MAPIMachineSetConverterConstructor is a function that constructs a CAPI to MAPI MachineSet converter.
// Since the CAPI to MAPI conversion relies on different types, it is expected that the constructor is wrapped in a closure
// that handles type assertions to fit the interface.
type CAPI2MAPIMachineSetConverterConstructor func(*capiv1.MachineSet, client.Object, client.Object) capi2mapi.MachineSetAndMachineTemplate

// MAPI2CAPIMachineConverterConstructor is a function that constructs a MAPI to CAPI Machine converter.
type MAPI2CAPIMachineConverterConstructor func(*mapiv1.Machine, *configv1.Infrastructure) mapi2capi.Machine

// MAPI2CAPIMachineSetConverterConstructor is a function that constructs a MAPI to CAPI MachineSet converter.
type MAPI2CAPIMachineSetConverterConstructor func(*mapiv1.MachineSet, *configv1.Infrastructure) mapi2capi.MachineSet

// StringFuzzer is a function that returns a random string.
type StringFuzzer func(fuzz.Continue) string

// capiToMapiMachineFuzzInput is a struct that holds the input for the CAPI to MAPI fuzz test.
type capiToMapiMachineFuzzInput struct {
	machine                  *capiv1.Machine
	infra                    *configv1.Infrastructure
	infraMachine             client.Object
	infraCluster             client.Object
	mapiConverterConstructor MAPI2CAPIMachineConverterConstructor
	capiConverterConstructor CAPI2MAPIMachineConverterConstructor
}

// CAPI2MAPIMachineRoundTripFuzzTest is a generic test that can be used to test roundtrip conversion between CAPI and MAPI Machine objects.
// It leverages fuzz testing to generate random CAPI objects and then converts them to MAPI objects and back to CAPI objects.
// The test then compares the original CAPI object with the final CAPI object to ensure that the conversion is lossless.
// Any lossy conversions must be accounted for within the fuzz functions passed in.
func CAPI2MAPIMachineRoundTripFuzzTest(scheme *runtime.Scheme, infra *configv1.Infrastructure, infraCluster, infraMachine client.Object, mapiConverter MAPI2CAPIMachineConverterConstructor, capiConverter CAPI2MAPIMachineConverterConstructor, fuzzerFuncs ...fuzzer.FuzzerFuncs) {
	machineFuzzInputs := []TableEntry{}
	fz := getFuzzer(scheme, fuzzerFuncs...)

	for i := 0; i < 1000; i++ {
		m := &capiv1.Machine{}
		fz.Fuzz(m)
		fz.Fuzz(infraMachine)

		// The infraMachine should always have the same name, namespace labels and annotations as its parent machine.
		// https://github.com/kubernetes-sigs/cluster-api/blob/f88d7ae5155700c2cc367b31ddcc151c9ad579e4/internal/controllers/machineset/machineset_controller.go#L575-L579
		infraMachine.SetName(m.Name)
		infraMachine.SetNamespace(m.Namespace)
		infraMachine.SetLabels(m.GetLabels())
		infraMachine.SetAnnotations(m.GetAnnotations())

		in := capiToMapiMachineFuzzInput{
			machine:                  m,
			infra:                    infra,
			infraMachine:             infraMachine,
			infraCluster:             infraCluster,
			mapiConverterConstructor: mapiConverter,
			capiConverterConstructor: capiConverter,
		}

		machineFuzzInputs = append(machineFuzzInputs, Entry(fmt.Sprintf("%d", i), in))
	}

	DescribeTable("should be able to roundtrip fuzzed Machines", func(in capiToMapiMachineFuzzInput) { //nolint:dupl
		capiConverter := in.capiConverterConstructor(in.machine, in.infraMachine, in.infraCluster)

		mapiMachine, warnings, err := capiConverter.ToMachine()
		Expect(err).ToNot(HaveOccurred())
		Expect(warnings).To(BeEmpty())

		mapiConverter := in.mapiConverterConstructor(mapiMachine, in.infra)

		capiMachine, infraMachine, warnings, err := mapiConverter.ToMachineAndInfrastructureMachine()
		Expect(err).ToNot(HaveOccurred())
		Expect(warnings).To(BeEmpty())

		// Break down the comparison to make it easier to debug sections that are failing conversion.

		// Do not match on status yet, we do not support status conversion.
		// Expect(capiMachine.Status).To(Equal(in.machine.Status))
		// Expect(infraMachine.Status).To(Equal(in.infraMachine.Status))

		Expect(capiMachine.TypeMeta).To(Equal(in.machine.TypeMeta))
		Expect(capiMachine.ObjectMeta).To(Equal(in.machine.ObjectMeta))
		Expect(capiMachine.Spec).To(Equal(in.machine.Spec))

		infraMachineJSON, err := json.Marshal(infraMachine)
		Expect(err).ToNot(HaveOccurred())

		infraMachineUnstructured := &unstructured.Unstructured{}
		Expect(json.Unmarshal(infraMachineJSON, infraMachineUnstructured)).To(Succeed())

		Expect(infraMachine.GetObjectKind().GroupVersionKind()).To(Equal(in.infraMachine.GetObjectKind().GroupVersionKind()))
		Expect(infraMachine).To(HaveField("ObjectMeta", testutils.MatchViaJSON(infraMachineUnstructured.Object["metadata"])))
		Expect(infraMachine).To(HaveField("Spec", testutils.MatchViaJSON(infraMachineUnstructured.Object["spec"])))
	}, machineFuzzInputs)
}

// capiToMapiMachineSetFuzzInput is a struct that holds the input for the CAPI to MAPI fuzz test.
type capiToMapiMachineSetFuzzInput struct {
	machineSet               *capiv1.MachineSet
	infra                    *configv1.Infrastructure
	infraMachineTemplate     client.Object
	infraCluster             client.Object
	mapiConverterConstructor MAPI2CAPIMachineSetConverterConstructor
	capiConverterConstructor CAPI2MAPIMachineSetConverterConstructor
}

// CAPI2MAPIMachineSetRoundTripFuzzTest is a generic test that can be used to test roundtrip conversion between CAPI and MAPI MachineSet objects.
// It leverages fuzz testing to generate random CAPI objects and then converts them to MAPI objects and back to CAPI objects.
// The test then compares the original CAPI object with the final CAPI object to ensure that the conversion is lossless.
// Any lossy conversions must be accounted for within the fuzz functions passed in.
func CAPI2MAPIMachineSetRoundTripFuzzTest(scheme *runtime.Scheme, infra *configv1.Infrastructure, infraCluster, infraMachineTemplate client.Object, mapiConverter MAPI2CAPIMachineSetConverterConstructor, capiConverter CAPI2MAPIMachineSetConverterConstructor, fuzzerFuncs ...fuzzer.FuzzerFuncs) {
	machineFuzzInputs := []TableEntry{}
	fz := getFuzzer(scheme, fuzzerFuncs...)

	for i := 0; i < 1000; i++ {
		m := &capiv1.MachineSet{}
		fz.Fuzz(m)
		fz.Fuzz(infraMachineTemplate)

		in := capiToMapiMachineSetFuzzInput{
			machineSet:               m,
			infra:                    infra,
			infraMachineTemplate:     infraMachineTemplate,
			infraCluster:             infraCluster,
			mapiConverterConstructor: mapiConverter,
			capiConverterConstructor: capiConverter,
		}

		machineFuzzInputs = append(machineFuzzInputs, Entry(fmt.Sprintf("%d", i), in))
	}

	DescribeTable("should be able to roundtrip fuzzed MachineSets", func(in capiToMapiMachineSetFuzzInput) { //nolint:dupl
		capiConverter := in.capiConverterConstructor(in.machineSet, in.infraMachineTemplate, in.infraCluster)

		mapiMachineSet, warnings, err := capiConverter.ToMachineSet()
		Expect(err).ToNot(HaveOccurred())
		Expect(warnings).To(BeEmpty())

		mapiConverter := in.mapiConverterConstructor(mapiMachineSet, in.infra)

		capiMachineSet, infraMachineTemplate, warnings, err := mapiConverter.ToMachineSetAndMachineTemplate()
		Expect(err).ToNot(HaveOccurred())
		Expect(warnings).To(BeEmpty())

		// Break down the comparison to make it easier to debug sections that are failing conversion.

		// Do not match on status yet, we do not support status conversion.
		// Expect(capiMachineSet.Status).To(Equal(in.machineSet.Status))
		// Expect(infraMachineTemplate.Status).To(Equal(in.infraMachineTemplate.Status))

		Expect(capiMachineSet.TypeMeta).To(Equal(in.machineSet.TypeMeta))
		Expect(capiMachineSet.ObjectMeta).To(Equal(in.machineSet.ObjectMeta))
		Expect(capiMachineSet.Spec).To(Equal(in.machineSet.Spec))

		infraMachineTemplateJSON, err := json.Marshal(infraMachineTemplate)
		Expect(err).ToNot(HaveOccurred())

		infraMachineTemplateUnstructured := &unstructured.Unstructured{}
		Expect(json.Unmarshal(infraMachineTemplateJSON, infraMachineTemplateUnstructured)).To(Succeed())

		Expect(infraMachineTemplate.GetObjectKind().GroupVersionKind()).To(Equal(in.infraMachineTemplate.GetObjectKind().GroupVersionKind()))
		Expect(infraMachineTemplate).To(HaveField("ObjectMeta", testutils.MatchViaJSON(infraMachineTemplateUnstructured.Object["metadata"])))
		Expect(infraMachineTemplate).To(HaveField("Spec", testutils.MatchViaJSON(infraMachineTemplateUnstructured.Object["spec"])))
	}, machineFuzzInputs)
}

// mapiToCapiMachineFuzzInput is a struct that holds the input for the MAPI to CAPI fuzz test.
type mapiToCapiMachineFuzzInput struct {
	machine                  *mapiv1.Machine
	infra                    *configv1.Infrastructure
	infraCluster             client.Object
	mapiConverterConstructor MAPI2CAPIMachineConverterConstructor
	capiConverterConstructor CAPI2MAPIMachineConverterConstructor
}

// MAPI2CAPIMachineRoundTripFuzzTest is a generic test that can be used to test roundtrip conversion between MAPI and CAPI Machine objects.
// It leverages fuzz testing to generate random MAPI objects and then converts them to CAPI objects and back to MAPI objects.
// The test then compares the original MAPI object with the final MAPI object to ensure that the conversion is lossless.
// Any lossy conversions must be accounted for within the fuzz functions passed in.
func MAPI2CAPIMachineRoundTripFuzzTest(scheme *runtime.Scheme, infra *configv1.Infrastructure, infraCluster client.Object, mapiConverter MAPI2CAPIMachineConverterConstructor, capiConverter CAPI2MAPIMachineConverterConstructor, fuzzerFuncs ...fuzzer.FuzzerFuncs) {
	machineFuzzInputs := []TableEntry{}
	fz := getFuzzer(scheme, fuzzerFuncs...)

	for i := 0; i < 1000; i++ {
		m := &mapiv1.Machine{}
		fz.Fuzz(m)

		in := mapiToCapiMachineFuzzInput{
			machine:                  m,
			infra:                    infra,
			infraCluster:             infraCluster,
			mapiConverterConstructor: mapiConverter,
			capiConverterConstructor: capiConverter,
		}

		machineFuzzInputs = append(machineFuzzInputs, Entry(fmt.Sprintf("%d", i), in))
	}

	DescribeTable("should be able to roundtrip fuzzed Machines", func(in mapiToCapiMachineFuzzInput) {
		mapiConverter := in.mapiConverterConstructor(in.machine, in.infra)

		capiMachine, infraMachine, warnings, err := mapiConverter.ToMachineAndInfrastructureMachine()
		Expect(err).ToNot(HaveOccurred())
		Expect(warnings).To(BeEmpty())

		capiConverter := in.capiConverterConstructor(capiMachine, infraMachine, in.infraCluster)
		mapiMachine, warnings, err := capiConverter.ToMachine()

		Expect(err).ToNot(HaveOccurred())
		Expect(warnings).To(BeEmpty())

		// Break down the comparison to make it easier to debug sections that are failing conversion.

		// Do not match on status yet, we do not support status conversion.
		// Expect(mapiMachine.Status).To(Equal(in.machine.Status))

		Expect(mapiMachine.TypeMeta).To(Equal(in.machine.TypeMeta))
		Expect(mapiMachine.ObjectMeta).To(Equal(in.machine.ObjectMeta))
		Expect(mapiMachine.Spec).To(WithTransform(ignoreMachineProviderSpec, testutils.MatchViaJSON(ignoreMachineProviderSpec(in.machine.Spec))))
		Expect(mapiMachine.Spec.ProviderSpec.Value.Raw).To(MatchJSON(in.machine.Spec.ProviderSpec.Value.Raw))
	}, machineFuzzInputs)
}

// mapiToCapiMachineSetFuzzInput is a struct that holds the input for the MAPI to CAPI fuzz test.
type mapiToCapiMachineSetFuzzInput struct {
	machineSet               *mapiv1.MachineSet
	infra                    *configv1.Infrastructure
	infraCluster             client.Object
	mapiConverterConstructor MAPI2CAPIMachineSetConverterConstructor
	capiConverterConstructor CAPI2MAPIMachineSetConverterConstructor
}

// MAPI2CAPIMachineSetRoundTripFuzzTest is a generic test that can be used to test roundtrip conversion between MAPI and CAPI MachineSet objects.
// It leverages fuzz testing to generate random MAPI objects and then converts them to CAPI objects and back to MAPI objects.
// The test then compares the original MAPI object with the final MAPI object to ensure that the conversion is lossless.
// Any lossy conversions must be accounted for within the fuzz functions passed in.
func MAPI2CAPIMachineSetRoundTripFuzzTest(scheme *runtime.Scheme, infra *configv1.Infrastructure, infraCluster client.Object, mapiConverter MAPI2CAPIMachineSetConverterConstructor, capiConverter CAPI2MAPIMachineSetConverterConstructor, fuzzerFuncs ...fuzzer.FuzzerFuncs) {
	machineFuzzInputs := []TableEntry{}
	fz := getFuzzer(scheme, fuzzerFuncs...)

	for i := 0; i < 1000; i++ {
		m := &mapiv1.MachineSet{}
		fz.Fuzz(m)

		in := mapiToCapiMachineSetFuzzInput{
			machineSet:               m,
			infra:                    infra,
			infraCluster:             infraCluster,
			mapiConverterConstructor: mapiConverter,
			capiConverterConstructor: capiConverter,
		}

		machineFuzzInputs = append(machineFuzzInputs, Entry(fmt.Sprintf("%d", i), in))
	}

	DescribeTable("should be able to roundtrip fuzzed MachineSets", func(in mapiToCapiMachineSetFuzzInput) {
		mapiConverter := in.mapiConverterConstructor(in.machineSet, in.infra)

		capiMachineSet, machineTemplate, warnings, err := mapiConverter.ToMachineSetAndMachineTemplate()
		Expect(err).ToNot(HaveOccurred())
		Expect(warnings).To(BeEmpty())

		capiConverter := in.capiConverterConstructor(capiMachineSet, machineTemplate, in.infraCluster)

		mapiMachineSet, warnings, err := capiConverter.ToMachineSet()
		Expect(err).ToNot(HaveOccurred())
		Expect(warnings).To(BeEmpty())

		// Break down the comparison to make it easier to debug sections that are failing conversion.

		// Do not match on status yet, we do not support status conversion.
		// Expect(mapiMachineSet.Status).To(Equal(in.machineSet.Status))

		Expect(mapiMachineSet.TypeMeta).To(Equal(in.machineSet.TypeMeta))
		Expect(mapiMachineSet.ObjectMeta).To(Equal(in.machineSet.ObjectMeta))
		Expect(mapiMachineSet.Spec).To(WithTransform(ignoreMachineSetProviderSpec, testutils.MatchViaJSON(ignoreMachineSetProviderSpec(in.machineSet.Spec))))
		Expect(mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw).To(MatchJSON(in.machineSet.Spec.Template.Spec.ProviderSpec.Value.Raw))
	}, machineFuzzInputs)
}

// getFuzzer returns a new fuzzer to be used for testing.
func getFuzzer(scheme *runtime.Scheme, funcs ...fuzzer.FuzzerFuncs) *fuzz.Fuzzer {
	funcs = append([]fuzzer.FuzzerFuncs{
		metafuzzer.Funcs,
	}, funcs...)

	return fuzzer.FuzzerFor(
		fuzzer.MergeFuzzerFuncs(funcs...),
		rand.NewSource(rand.Int63()), //nolint:gosec
		runtimeserializer.NewCodecFactory(scheme),
	)
}

// ignoreMachineProviderSpec returns a copy of the MachineSpec with the ProviderSpec field set to nil.
// This is used so that we can separate the comparison of the ProviderSpec field.
func ignoreMachineProviderSpec(in mapiv1.MachineSpec) mapiv1.MachineSpec {
	out := in.DeepCopy()
	out.ProviderSpec.Value = nil

	return *out
}

// ignoreMachineSetProviderSpec returns a copy of the MachineSpec with the ProviderSpec field set to nil.
// This is used so that we can separate the comparison of the ProviderSpec field.
func ignoreMachineSetProviderSpec(in mapiv1.MachineSetSpec) mapiv1.MachineSetSpec {
	out := in.DeepCopy()
	out.Template.Spec.ProviderSpec.Value = nil

	return *out
}

// ObjectMetaFuzzerFuncs returns a set of fuzzer functions that can be used to fuzz ObjectMeta objects.
// The namespace is forced to the provided namespace as the conversion always sets specific namespaces.
// Fields that are not required for conversion are cleared.
func ObjectMetaFuzzerFuncs(namespace string) fuzzer.FuzzerFuncs {
	return func(codecs runtimeserializer.CodecFactory) []interface{} {
		return []interface{}{
			func(o *metav1.ObjectMeta, c fuzz.Continue) {
				c.FuzzNoCustom(o)

				// Force the namespace else the conversion will fail as it always sets the namespaces deliberately.
				o.Namespace = namespace

				// Clear fields that are not required for conversion.
				o.GenerateName = ""
				o.SelfLink = "" //nolint:staticcheck
				o.UID = ""
				o.ResourceVersion = ""
				o.Generation = 0
				o.CreationTimestamp = metav1.Time{}
				o.DeletionTimestamp = nil
				o.DeletionGracePeriodSeconds = nil
				o.Finalizers = nil // Finalizers are handled outside of the conversion library.
				o.ManagedFields = nil

				// Clear fields that are not currently supported in the conversion.
				o.OwnerReferences = nil // TODO(OCPCLOUD-2716)

				// Annotations and labels maps should be non-nil (Since the conversion initialises them).
				if o.Annotations == nil {
					o.Annotations = map[string]string{}
				}
				if o.Labels == nil {
					o.Labels = map[string]string{}
				}
			},
		}
	}
}

// CAPIMachineFuzzerFuncs returns a set of fuzzer functions that can be used to fuzz MachineSpec objects.
func CAPIMachineFuzzerFuncs(providerIDFuzz StringFuzzer, infraKind, infraAPIVersion, clusterName string) fuzzer.FuzzerFuncs {
	return func(codecs runtimeserializer.CodecFactory) []interface{} {
		return []interface{}{
			func(b *capiv1.Bootstrap, c fuzz.Continue) {
				c.FuzzNoCustom(b)

				// Clear fields that are not supported in the bootstrap spec.
				b.ConfigRef = nil

				// If we fuzzed an empty string, nil it out to match the behaviour of the converter.
				if b.DataSecretName != nil && *b.DataSecretName == "" {
					b.DataSecretName = nil
				}
			},
			func(m *capiv1.MachineSpec, c fuzz.Continue) {
				c.FuzzNoCustom(m)

				m.ClusterName = clusterName
				m.ProviderID = ptr.To(providerIDFuzz(c))

				// Clear fields that are not supported in the machine spec.
				m.Version = nil

				// Clear fields that are not yet supported in the conversion.
				// TODO(OCPCLOUD-2715): Implement support for node draining options in MAPI.
				m.NodeDrainTimeout = nil
				m.NodeVolumeDetachTimeout = nil
				m.NodeDeletionTimeout = nil

				// Clear fields that are zero valued.
				if m.FailureDomain != nil && *m.FailureDomain == "" {
					m.FailureDomain = nil
				}
				// Power VS does not support failure domain
				if infraKind == powerVSMachineKind {
					m.FailureDomain = nil
				}
			},
			func(m *capiv1.Machine, c fuzz.Continue) {
				c.FuzzNoCustom(m)

				// The reference from a Machine to the InfraMachine should
				// always use the same name and namespace as the Machine itself.
				// The kind and APIVersion should be set to the InfraMachine's kind and APIVersion.
				// This is fixed in the conversion so we fix it here.
				// Other fields are not required for conversion.
				m.Spec.InfrastructureRef = corev1.ObjectReference{
					APIVersion: infraAPIVersion,
					Kind:       infraKind,
					Name:       m.Name,
					Namespace:  m.Namespace,
				}
			},
		}
	}
}

// CAPIMachineSetFuzzerFuncs returns a set of fuzzer functions that can be used to fuzz MachineSetSpec objects.
func CAPIMachineSetFuzzerFuncs(infraTemplateKind, infraAPIVersion, clusterName string) fuzzer.FuzzerFuncs {
	return func(codecs runtimeserializer.CodecFactory) []interface{} {
		return []interface{}{
			func(t *capiv1.MachineTemplateSpec, c fuzz.Continue) {
				c.FuzzNoCustom(t)

				// Annotations and labels maps should be non-nil (Since the conversion initialises them).
				if t.Annotations == nil {
					t.Annotations = map[string]string{}
				}
				if t.Labels == nil {
					t.Labels = map[string]string{}
				}
			},
			func(m *capiv1.MachineSetSpec, c fuzz.Continue) {
				c.FuzzNoCustom(m)

				m.ClusterName = clusterName
			},
			func(m *capiv1.MachineSet, c fuzz.Continue) {
				c.FuzzNoCustom(m)

				// The reference from a MachineSet to the InfraMachine should
				// always use the same name and namespace as the Machine itself.
				// The kind and APIVersion should be set to the InfraMachineTemplate's kind and APIVersion.
				// This is fixed in the conversion so we fix it here.
				// Other fields are not required for conversion.
				m.Spec.Template.Spec.InfrastructureRef = corev1.ObjectReference{
					APIVersion: infraAPIVersion,
					Kind:       infraTemplateKind,
					Name:       m.Name,
					Namespace:  m.Namespace,
				}
			},
		}
	}
}

// MAPIMachineFuzzerFuncs returns a set of fuzzer functions that can be used to fuzz MachineSpec objects.
// The providerSpec should be a pointer to a providerSpec type for the platform being tested.
// This will be fuzzed and then injected into the MachineSpec as a RawExtension.
// The providerIDFuzz function should be a function that returns a valid providerID for the platform being tested.
func MAPIMachineFuzzerFuncs(providerSpec runtime.Object, providerIDFuzz StringFuzzer) fuzzer.FuzzerFuncs {
	return func(codecs runtimeserializer.CodecFactory) []interface{} {
		return []interface{}{
			// MAPI to CAPI conversion functions.
			func(m *mapiv1.MachineSpec, c fuzz.Continue) {
				c.FuzzNoCustom(m)
				c.Fuzz(providerSpec)

				bytes, err := json.Marshal(providerSpec)
				if err != nil {
					panic(err)
				}

				// Set the bytes field on the RawExtension
				m.ProviderSpec.Value = &runtime.RawExtension{
					Raw: bytes,
				}

				// Clear fields that are not supported in the machine spec.
				m.ObjectMeta.Name = ""
				m.ObjectMeta.GenerateName = ""
				m.ObjectMeta.Namespace = ""
				m.ObjectMeta.OwnerReferences = nil
				m.AuthoritativeAPI = ""

				// Clear fields that are not yet supported in the conversion.
				// TODO(OCPCLOUD-2680): For taints and annotations.
				m.ObjectMeta.Annotations = nil
				m.Taints = nil

				// Set the providerID to a valid providerID that will at least pass through the conversion.
				m.ProviderID = ptr.To(providerIDFuzz(c))

				// Labels to go onto the node have to have specific prefixes.
				m.ObjectMeta.Labels = map[string]string{
					"node-role.kubernetes.io/worker":                                                "",
					"node-restriction.kubernetes.io/" + strings.ReplaceAll(c.RandString(), "/", ""): c.RandString(),
					"node.cluster.x-k8s.io/" + strings.ReplaceAll(c.RandString(), "/", ""):          c.RandString(),
					strings.ReplaceAll(c.RandString(), "/", "") + ".node-restriction.kubernetes.io": c.RandString(),
					strings.ReplaceAll(c.RandString(), "/", "") + ".node.cluster.x-k8s.io":          c.RandString(),
				}
			},
			func(hooks *mapiv1.LifecycleHooks, c fuzz.Continue) {
				c.FuzzNoCustom(hooks)

				// Clear the slices if they are empty.
				// This aids in comparison with the conversion which doesn't initialise the slices.
				if len(hooks.PreTerminate) == 0 {
					hooks.PreTerminate = nil
				}

				if len(hooks.PreDrain) == 0 {
					hooks.PreDrain = nil
				}
			},
		}
	}
}

// MAPIMachineSetFuzzerFuncs returns a set of fuzzer functions that can be used to fuzz MachineSetSpec objects.
// This function relies on the MachineSpec fuzzer functions to fuzz the MachineTemplateSpec.
func MAPIMachineSetFuzzerFuncs() fuzzer.FuzzerFuncs {
	return func(codecs runtimeserializer.CodecFactory) []interface{} {
		return []interface{}{
			// MAPI to CAPI conversion functions.
			func(m *mapiv1.MachineSetSpec, c fuzz.Continue) {
				c.FuzzNoCustom(m)

				// Clear fields that are not supported in the machine template objectmeta.
				m.Template.ObjectMeta.Name = ""
				m.Template.ObjectMeta.GenerateName = ""
				m.Template.ObjectMeta.Namespace = ""
				m.Template.ObjectMeta.OwnerReferences = nil

				// Clear the authoritative API since that's not relevant for conversion.
				m.AuthoritativeAPI = ""
			},
		}
	}
}
