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
	"regexp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/consts"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/randfill"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/apitesting/fuzzer"
	metafuzzer "k8s.io/apimachinery/pkg/apis/meta/fuzzer"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/utils/ptr"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

const fuzzIterations = 1000

const powerVSMachineKind = "IBMPowerVSMachine"

// CAPI2MAPIMachineConverterConstructor is a function that constructs a CAPI to MAPI Machine converter.
// Since the CAPI to MAPI conversion relies on different types, it is expected that the constructor is wrapped in a closure
// that handles type assertions to fit the interface.
type CAPI2MAPIMachineConverterConstructor func(*clusterv1.Machine, client.Object, client.Object) capi2mapi.MachineAndInfrastructureMachine

// CAPI2MAPIMachineSetConverterConstructor is a function that constructs a CAPI to MAPI MachineSet converter.
// Since the CAPI to MAPI conversion relies on different types, it is expected that the constructor is wrapped in a closure
// that handles type assertions to fit the interface.
type CAPI2MAPIMachineSetConverterConstructor func(*clusterv1.MachineSet, client.Object, client.Object) capi2mapi.MachineSetAndMachineTemplate

// MAPI2CAPIMachineConverterConstructor is a function that constructs a MAPI to CAPI Machine converter.
type MAPI2CAPIMachineConverterConstructor func(*mapiv1beta1.Machine, *configv1.Infrastructure) mapi2capi.Machine

// MAPI2CAPIMachineSetConverterConstructor is a function that constructs a MAPI to CAPI MachineSet converter.
type MAPI2CAPIMachineSetConverterConstructor func(*mapiv1beta1.MachineSet, *configv1.Infrastructure) mapi2capi.MachineSet

// StringFuzzer is a function that returns a random string.
type StringFuzzer func(randfill.Continue) string

// capiToMapiMachineFuzzInput is a struct that holds the input for the CAPI to MAPI fuzz test.
type capiToMapiMachineFuzzInput struct {
	machine                  *clusterv1.Machine
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
//
//nolint:funlen
func CAPI2MAPIMachineRoundTripFuzzTest(scheme *runtime.Scheme, infra *configv1.Infrastructure, infraCluster, infraMachine client.Object, mapiConverter MAPI2CAPIMachineConverterConstructor, capiConverter CAPI2MAPIMachineConverterConstructor, fuzzerFuncs ...fuzzer.FuzzerFuncs) {
	machineFuzzInputs := []TableEntry{}
	fz := getFuzzer(scheme, fuzzerFuncs...)

	for i := 0; i < fuzzIterations; i++ {
		m := &clusterv1.Machine{}
		fz.Fill(m)
		fz.Fill(infraMachine)

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

	DescribeTable("should be able to roundtrip fuzzed Machines", func(in capiToMapiMachineFuzzInput) {
		capiConverter := in.capiConverterConstructor(in.machine, in.infraMachine, in.infraCluster)

		mapiMachine, warnings, err := capiConverter.ToMachine()
		Expect(err).ToNot(HaveOccurred())
		Expect(warnings).To(BeEmpty())

		mapiConverter := in.mapiConverterConstructor(mapiMachine, in.infra)

		capiMachine, infraMachine, warnings, err := mapiConverter.ToMachineAndInfrastructureMachine()
		Expect(err).ToNot(HaveOccurred())
		Expect(warnings).To(BeEmpty())

		// Break down the comparison to make it easier to debug sections that are failing conversion.

		// Status comparison
		capiMachine.Status.Deprecated.V1Beta1.Conditions = nil // This is not a 1:1 mapping conversion between CAPI and MAPI.
		capiMachine.Status.Conditions = nil                    // This is not a 1:1 mapping conversion between CAPI and MAPI.

		capiMachine.Status.CertificatesExpiryDate = metav1.Time{} // This is not present on the MAPI Machine status.
		capiMachine.Status.Deletion = nil                         // This is not present on the MAPI Machine status.
		capiMachine.Status.NodeInfo = nil                         // This is not present on the MAPI Machine status.

		// Set Deprecated to nil if the values are zero
		if capiMachine.Status.Deprecated.V1Beta1.FailureReason == nil &&
			capiMachine.Status.Deprecated.V1Beta1.FailureMessage == nil &&
			len(capiMachine.Status.Deprecated.V1Beta1.Conditions) == 0 {
			capiMachine.Status.Deprecated = nil
		}

		Expect(capiMachine.Status).To(Equal(in.machine.Status))

		// Status comparison for infrastructure machines is not implemented yet.
		// Expect(infraMachine.Status).To(Equal(in.infraMachine.Status))

		capiMachine.Finalizers = nil
		Expect(capiMachine.TypeMeta).To(Equal(in.machine.TypeMeta))
		Expect(capiMachine.ObjectMeta).To(Equal(in.machine.ObjectMeta))
		Expect(capiMachine.Spec).To(Equal(in.machine.Spec))

		infraMachine.SetFinalizers(nil)
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
	machineSet               *clusterv1.MachineSet
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
//
//nolint:funlen
func CAPI2MAPIMachineSetRoundTripFuzzTest(scheme *runtime.Scheme, infra *configv1.Infrastructure, infraCluster, infraMachineTemplate client.Object, mapiConverter MAPI2CAPIMachineSetConverterConstructor, capiConverter CAPI2MAPIMachineSetConverterConstructor, fuzzerFuncs ...fuzzer.FuzzerFuncs) {
	machineFuzzInputs := []TableEntry{}
	fz := getFuzzer(scheme, fuzzerFuncs...)

	for i := 0; i < fuzzIterations; i++ {
		m := &clusterv1.MachineSet{}
		fz.Fill(m)
		fz.Fill(infraMachineTemplate)

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

	DescribeTable("should be able to roundtrip fuzzed MachineSets", func(in capiToMapiMachineSetFuzzInput) {
		capiConverter := in.capiConverterConstructor(in.machineSet, in.infraMachineTemplate, in.infraCluster)

		mapiMachineSet, warnings, err := capiConverter.ToMachineSet()
		Expect(err).ToNot(HaveOccurred())
		Expect(warnings).To(BeEmpty())

		mapiConverter := in.mapiConverterConstructor(mapiMachineSet, in.infra)

		capiMachineSet, infraMachineTemplate, warnings, err := mapiConverter.ToMachineSetAndMachineTemplate()
		Expect(err).ToNot(HaveOccurred())
		Expect(warnings).To(BeEmpty())

		// Break down the comparison to make it easier to debug sections that are failing conversion.

		// Infrastructure machine template status comparison is not implemented yet.
		// Expect(infraMachineTemplate.Status).To(Equal(in.infraMachineTemplate.Status))

		capiMachineSet.Finalizers = nil

		Expect(capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name).To(
			// During roundtrip conversion, the InfraMachineTemplate gains a hash suffix. This is intended.
			MatchRegexp("^" + regexp.QuoteMeta(in.machineSet.Spec.Template.Spec.InfrastructureRef.Name) + "-[a-f0-9]{8}$"),
		)

		// Reset the name to match the original name. This is intentional lossy conversion that is checked above.
		capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name = in.machineSet.Spec.Template.Spec.InfrastructureRef.Name

		Expect(capiMachineSet.TypeMeta).To(Equal(in.machineSet.TypeMeta))
		Expect(capiMachineSet.ObjectMeta).To(Equal(in.machineSet.ObjectMeta))
		Expect(capiMachineSet.Spec).To(Equal(in.machineSet.Spec))

		// The conditions are not a 1:1 mapping conversion between CAPI and MAPI.
		// So null them out to match the original nil fuzzing.
		capiMachineSet.Status.Deprecated.V1Beta1.Conditions = nil
		// Set Deprecated to nil if the values are zero
		if capiMachineSet.Status.Deprecated.V1Beta1.FullyLabeledReplicas == 0 &&
			capiMachineSet.Status.Deprecated.V1Beta1.ReadyReplicas == 0 &&
			capiMachineSet.Status.Deprecated.V1Beta1.AvailableReplicas == 0 &&
			capiMachineSet.Status.Deprecated.V1Beta1.FailureReason == nil &&
			capiMachineSet.Status.Deprecated.V1Beta1.FailureMessage == nil &&
			len(capiMachineSet.Status.Deprecated.V1Beta1.Conditions) == 0 {
			capiMachineSet.Status.Deprecated = nil
		}

		// Conversion always sets the replicas to be 0 and not a pointer.
		if in.machineSet.Status.Replicas == nil {
			in.machineSet.Status.Replicas = ptr.To(int32(0))
		}

		capiMachineSet.Status.Conditions = nil

		// The status selector is computed based on the spec selector of the same object,
		// so we don't want to compare it with the original object's status selector.
		capiMachineSet.Status.Selector = ""

		Expect(capiMachineSet.Status).To(Equal(in.machineSet.Status))

		infraMachineTemplate.SetFinalizers(nil)
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
	machine                  *mapiv1beta1.Machine
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

	for i := 0; i < fuzzIterations; i++ {
		m := &mapiv1beta1.Machine{}
		fz.Fill(m)

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

		mapiMachine.Status.LastOperation = nil // Ignore, this field as it is not present in CAPI.
		// The conditions are not a 1:1 mapping conversion between CAPI and MAPI.
		// So null them out to match the original nil fuzzing.
		mapiMachine.Status.Conditions = nil

		// The ProviderStatus only contains non-roundtripable fields.
		Expect(mapiMachine.Status).To(WithTransform(ignoreMachineProviderStatus, Equal(ignoreMachineProviderStatus(in.machine.Status))), "converted MAPI machine should have matching .status")

		mapiMachine.Finalizers = nil
		Expect(mapiMachine.TypeMeta).To(Equal(in.machine.TypeMeta), "converted MAPI machine should have matching .typeMeta")
		Expect(mapiMachine.ObjectMeta).To(Equal(in.machine.ObjectMeta), "converted MAPI machine should have matching .metadata")
		Expect(mapiMachine.Spec).To(WithTransform(ignoreMachineProviderSpec, testutils.MatchViaJSON(ignoreMachineProviderSpec(in.machine.Spec))), "converted MAPI machine should have matching .spec")
		Expect(mapiMachine.Spec.ProviderSpec.Value.Raw).To(MatchJSON(in.machine.Spec.ProviderSpec.Value.Raw), "converted MAPI machine should have matching .spec.providerSpec")
	}, machineFuzzInputs)
}

// mapiToCapiMachineSetFuzzInput is a struct that holds the input for the MAPI to CAPI fuzz test.
type mapiToCapiMachineSetFuzzInput struct {
	machineSet               *mapiv1beta1.MachineSet
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

	for i := 0; i < fuzzIterations; i++ {
		m := &mapiv1beta1.MachineSet{}
		fz.Fill(m)

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
		mapiMachineSet.Finalizers = nil
		Expect(mapiMachineSet.TypeMeta).To(Equal(in.machineSet.TypeMeta), "converted MAPI machine set should have matching .typeMeta")
		Expect(mapiMachineSet.ObjectMeta).To(Equal(in.machineSet.ObjectMeta), "converted MAPI machine set should have matching .metadata")
		Expect(mapiMachineSet.Status).To(Equal(in.machineSet.Status), "converted MAPI machine set should have matching .status")
		Expect(mapiMachineSet.Spec).To(WithTransform(ignoreMachineSetProviderSpec, testutils.MatchViaJSON(ignoreMachineSetProviderSpec(in.machineSet.Spec))), "converted MAPI machine set should have matching .spec")
		Expect(mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw).To(MatchJSON(in.machineSet.Spec.Template.Spec.ProviderSpec.Value.Raw), "converted MAPI machine set should have matching .spec.template.spec.providerSpec")
	}, machineFuzzInputs)
}

// getFuzzer returns a new fuzzer to be used for testing.
func getFuzzer(scheme *runtime.Scheme, funcs ...fuzzer.FuzzerFuncs) *randfill.Filler {
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
func ignoreMachineProviderSpec(in mapiv1beta1.MachineSpec) mapiv1beta1.MachineSpec {
	out := in.DeepCopy()
	out.ProviderSpec.Value = nil

	return *out
}

// ignoreMachineSetProviderSpec returns a copy of the MachineSpec with the ProviderSpec field set to nil.
// This is used so that we can separate the comparison of the ProviderSpec field.
func ignoreMachineSetProviderSpec(in mapiv1beta1.MachineSetSpec) mapiv1beta1.MachineSetSpec {
	out := in.DeepCopy()
	out.Template.Spec.ProviderSpec.Value = nil

	return *out
}

// ignoreMachineProviderStatus returns a copy of the MachineSpec with the ProviderSpec field set to nil.
// This is used so that we can separate the comparison of the ProviderSpec field.
func ignoreMachineProviderStatus(in mapiv1beta1.MachineStatus) mapiv1beta1.MachineStatus {
	out := in.DeepCopy()
	out.ProviderStatus = nil

	return *out
}

// ObjectMetaFuzzerFuncs returns a set of fuzzer functions that can be used to fuzz ObjectMeta objects.
// The namespace is forced to the provided namespace as the conversion always sets specific namespaces.
// Fields that are not required for conversion are cleared.
func ObjectMetaFuzzerFuncs(namespace string) fuzzer.FuzzerFuncs {
	return func(codecs runtimeserializer.CodecFactory) []interface{} {
		return []interface{}{
			func(o *metav1.ObjectMeta, c randfill.Continue) {
				c.FillNoCustom(o)

				// Force the namespace else the conversion will fail as it always sets the namespaces deliberately.
				o.Namespace = namespace

				// Clear fields that are not required for conversion.
				o.GenerateName = ""
				o.SelfLink = ""
				o.UID = ""
				o.ResourceVersion = ""
				o.Generation = 0
				o.CreationTimestamp = metav1.Time{}
				o.DeletionTimestamp = nil
				o.DeletionGracePeriodSeconds = nil
				o.Finalizers = nil // Finalizers are handled outside of the conversion library.
				o.ManagedFields = nil

				// Clear fields that are not currently supported in the conversion.
				o.OwnerReferences = nil // Handled outside of the conversion library.

				// Empty annotations and labels maps should be nil (Since the conversion nils them).
				if len(o.Annotations) == 0 {
					o.Annotations = nil
				}
				if len(o.Labels) == 0 {
					o.Labels = nil
				}
			},
		}
	}
}

// CAPIMachineFuzzerFuncs returns a set of fuzzer functions that can be used to fuzz MachineSpec objects.
func CAPIMachineFuzzerFuncs(providerIDFuzz StringFuzzer, infraKind, infraAPIGroup, clusterName string) fuzzer.FuzzerFuncs {
	return func(codecs runtimeserializer.CodecFactory) []interface{} {
		return []interface{}{
			func(b *clusterv1.Bootstrap, c randfill.Continue) {
				c.FillNoCustom(b)

				// Clear fields that are not supported in the bootstrap spec.
				b.ConfigRef = clusterv1.ContractVersionedObjectReference{}

				// If we fuzzed an empty string, nil it out to match the behaviour of the converter.
				if b.DataSecretName != nil && *b.DataSecretName == "" {
					b.DataSecretName = nil
				}
			},
			func(m *clusterv1.MachineSpec, c randfill.Continue) {
				c.FillNoCustom(m)

				m.ClusterName = clusterName
				m.ProviderID = providerIDFuzz(c)

				// Clear fields that are not supported in the machine spec.
				m.Version = ""
				m.ReadinessGates = nil
				m.MinReadySeconds = nil
				// Clear fields that are not yet supported in the conversion.
				// TODO(OCPCLOUD-2715): Implement support for node draining options in MAPI.
				m.Deletion.NodeDrainTimeoutSeconds = nil
				m.Deletion.NodeVolumeDetachTimeoutSeconds = nil
				m.Deletion.NodeDeletionTimeoutSeconds = ptr.To(int32(10)) // This is defaulted to 10s by default in CAPI.

				// Power VS does not support failure domain
				if infraKind == powerVSMachineKind {
					m.FailureDomain = ""
				}
			},
			func(m *clusterv1.Machine, c randfill.Continue) {
				c.FillNoCustom(m)

				if m.Labels == nil {
					m.Labels = make(map[string]string)
				}
				m.Labels[clusterv1.ClusterNameLabel] = clusterName

				// The reference from a Machine to the InfraMachine should
				// always use the same name and namespace as the Machine itself.
				// The kind and APIVersion should be set to the InfraMachine's kind and APIVersion.
				// This is fixed in the conversion so we fix it here.
				// Other fields are not required for conversion.
				m.Spec.InfrastructureRef = clusterv1.ContractVersionedObjectReference{
					APIGroup: infraAPIGroup,
					Kind:     infraKind,
					Name:     m.Name,
				}
			},
			func(m *clusterv1.MachineStatus, c randfill.Continue) {
				c.FillNoCustom(m)

				fuzzCAPIMachineStatusAddresses(&m.Addresses, c)
				fuzzCAPIMachineStatusPhase(&m.Phase, c)

				m.ObservedGeneration = 0                 // Ignore, this field as it shouldn't match between CAPI and MAPI.
				m.Conditions = nil                       // Ignore, this field as it is not a 1:1 mapping between CAPI and MAPI but rather a recomputation of the conditions based on other fields.
				m.CertificatesExpiryDate = metav1.Time{} // Ignore, this field as it is not present in MAPI.
				m.Deletion = nil                         // Ignore, this field as it is not present in MAPI.
			},
			func(m *clusterv1.MachineStatus, c randfill.Continue) {
				// Deal with the V1Beta2 conditions.
				m.Conditions = nil
			},
		}
	}
}

// CAPIMachineSetFuzzerFuncs returns a set of fuzzer functions that can be used to fuzz MachineSetSpec objects.
func CAPIMachineSetFuzzerFuncs(infraTemplateKind, infraAPIGroup, clusterName string) fuzzer.FuzzerFuncs {
	return func(codecs runtimeserializer.CodecFactory) []interface{} {
		return []interface{}{
			func(t *clusterv1.MachineTemplateSpec, c randfill.Continue) {
				c.FillNoCustom(t)

				if len(t.Annotations) == 0 {
					t.Annotations = nil
				}

				if t.Labels == nil {
					t.Labels = make(map[string]string)
				}
				t.Labels[clusterv1.ClusterNameLabel] = clusterName
			},
			func(m *clusterv1.MachineSetSpec, c randfill.Continue) {
				c.FillNoCustom(m)

				m.ClusterName = clusterName

				if m.Selector.MatchLabels == nil {
					m.Selector.MatchLabels = map[string]string{}
				}

				// Clear MachineNaming.Template as it is not supported in MAPI conversion.
				// This field does not have an equivalent in MAPI MachineSet and would be lost
				// during CAPI->MAPI->CAPI roundtrip conversion.
				m.MachineNaming.Template = ""

				fuzzCAPIMachineSetSpecDeletionOrder(&m.Deletion.Order, c)
			},
			func(m *clusterv1.MachineSetStatus, c randfill.Continue) {
				c.FillNoCustom(m)
				m.Selector = ""          // Ignore, this field as it is not present in MAPI.
				m.ObservedGeneration = 0 // Ignore, this field as it shouldn't match between CAPI and MAPI.
				m.Conditions = nil       // Ignore, this field as it is not a 1:1 mapping between CAPI and MAPI but rather a recomputation of the conditions based on other fields.
			},
			func(m *clusterv1.MachineSetStatus, c randfill.Continue) {
				m.Conditions = nil
				if m.Deprecated != nil && m.Deprecated.V1Beta1 != nil {
					m.ReadyReplicas = ptr.To(m.Deprecated.V1Beta1.ReadyReplicas)
					m.AvailableReplicas = ptr.To(m.Deprecated.V1Beta1.AvailableReplicas)
				}
				// If the current MachineSet is a stand-alone MachineSet, the MachineSet controller does not set an up-to-date condition
				// on its child Machines, allowing tools managing higher level abstractions to set this condition.
				// This is also consistent with the fact that the MachineSet controller primarily takes care of the number of Machine
				// replicas, it doesn't reconcile them (even if we have a few exceptions like in-place propagation of a few selected
				// fields and remediation).
				// So considering we don't use the MachineDeployments on the MAPI side
				// and don't support "matching" higher level abstractions
				// for the conversion of a MachineSet from MAPI to CAPI
				// We always want to set this to zero on conversion.
				// ref:
				// https://github.com/kubernetes-sigs/cluster-api/blob/9c2eb0a04d5a03e18f2d557f1297391fb635f88d/internal/controllers/machineset/machineset_controller.go#L610-L618
				m.UpToDateReplicas = ptr.To(int32(0))
			},
			func(m *clusterv1.MachineSet, c randfill.Continue) {
				c.FillNoCustom(m)

				if m.Labels == nil {
					m.Labels = make(map[string]string)
				}
				m.Labels[clusterv1.ClusterNameLabel] = clusterName

				// The reference from a MachineSet to the InfraMachine should
				// always use the same name and namespace as the Machine itself.
				// The kind and APIVersion should be set to the InfraMachineTemplate's kind and APIVersion.
				// This is fixed in the conversion so we fix it here.
				// Other fields are not required for conversion.
				m.Spec.Template.Spec.InfrastructureRef = clusterv1.ContractVersionedObjectReference{
					APIGroup: infraAPIGroup,
					Kind:     infraTemplateKind,
					Name:     m.Name,
				}
			},
		}
	}
}

// MAPIMachineFuzzer is a helper struct for fuzzing MAPI Machine objects.
// It tracks instance type, region, and zone information to ensure consistency
// between the provider spec and machine labels during fuzz testing.
type MAPIMachineFuzzer struct {
	InstanceType string
	Region       string
	Zone         string
}

// FuzzMachine fuzzes a MAPI Machine object and ensures that machine labels
// are consistent with the provider spec fields (instance type, region, zone).
func (f *MAPIMachineFuzzer) FuzzMachine(m *mapiv1beta1.Machine, c randfill.Continue) {
	// Reset the fuzzer
	f.InstanceType = ""
	f.Region = ""
	f.Zone = ""

	c.FillNoCustom(m)

	if m.Labels == nil {
		m.Labels = map[string]string{}
	}

	// Copy over from fuzzed struct to the designated labels to have the same value.
	if f.InstanceType != "" {
		m.Labels[consts.MAPIMachineMetadataLabelInstanceType] = f.InstanceType
	}

	if f.Region != "" {
		m.Labels[consts.MAPIMachineMetadataLabelRegion] = f.Region
	}

	if f.Zone != "" {
		m.Labels[consts.MAPIMachineMetadataLabelZone] = f.Zone
	}

	if len(m.Labels) == 0 {
		m.Labels = nil
	}

	// The conversion library while converting
	// machine labels and annotations from MAPI->CAPI merges the
	// MAPI machine.spec.metadata.labels/annotations and MAPI machine.metadata.labels/annotations
	// into CAPI machine.metadata.labels/annotations as that's the only place for metadata that CAPI has.
	// When they are back-converted from CAPI->MAPI
	// the conversion library converts CAPI machine.metadata.labels copying them both to
	// MAPI machine.spec.metadata.labels and MAPI machine.metadata.labels.
	// So these should match when we generate the initial MAPI machine
	// so we get the same MAPI machine after the roundtrip.
	m.Spec.ObjectMeta.Annotations = util.DeepCopyMapStringString(m.Annotations)
	m.Spec.ObjectMeta.Labels = util.DeepCopyMapStringString(m.Labels)
}

// MAPIMachineFuzzerFuncs returns a set of fuzzer functions that can be used to fuzz MachineSpec objects.
// The providerSpec should be a pointer to a providerSpec type for the platform being tested.
// This will be fuzzed and then injected into the MachineSpec as a RawExtension.
// The providerIDFuzz function should be a function that returns a valid providerID for the platform being tested.
//
//nolint:funlen
func MAPIMachineFuzzerFuncs(providerSpec runtime.Object, providerStatus interface{}, providerIDFuzz StringFuzzer) fuzzer.FuzzerFuncs {
	return func(codecs runtimeserializer.CodecFactory) []interface{} {
		return []interface{}{
			// MAPI to CAPI conversion functions.
			(&MAPIMachineFuzzer{}).FuzzMachine,
			func(m *mapiv1beta1.MachineSpec, c randfill.Continue) {
				c.FillNoCustom(m)
				c.Fill(providerSpec)

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
				// TODO(OCPCLOUD-2861/2899): For taints.
				m.Taints = nil

				// Set the providerID to a valid providerID that will at least pass through the conversion.
				m.ProviderID = ptr.To(providerIDFuzz(c))
			},
			func(hooks *mapiv1beta1.LifecycleHooks, c randfill.Continue) {
				c.FillNoCustom(hooks)

				// Clear the slices if they are empty.
				// This aids in comparison with the conversion which doesn't initialise the slices.
				if len(hooks.PreTerminate) == 0 {
					hooks.PreTerminate = nil
				}

				if len(hooks.PreDrain) == 0 {
					hooks.PreDrain = nil
				}
			},
			func(m *mapiv1beta1.MachineStatus, c randfill.Continue) {
				c.FillNoCustom(m)

				fuzzMAPIMachineStatusAddresses(&m.Addresses, c)
				fuzzMAPIMachineStatusPhase(m.Phase, c)

				bytes, err := json.Marshal(providerStatus)
				if err != nil {
					panic(err)
				}

				// Set the bytes field on the RawExtension
				m.ProviderStatus = &runtime.RawExtension{
					Raw: bytes,
				}

				// The only valid node reference is of kind Node and APIVersion v1.
				if m.NodeRef != nil {
					m.NodeRef = &corev1.ObjectReference{
						Kind:       "Node",
						Name:       m.NodeRef.Name,
						APIVersion: "v1",
					}
				}
				// Otherwise set it to nil.
				if m.NodeRef != nil && m.NodeRef.Name == "" {
					m.NodeRef = nil
				}

				m.LastOperation = nil        // Ignore, this field as it is not present in CAPI.
				m.AuthoritativeAPI = ""      // Ignore, this field as it is not present in CAPI.
				m.SynchronizedGeneration = 0 // Ignore, this field as it is not present in CAPI.
				m.Conditions = nil           // Ignore, this field as it is not a 1:1 mapping between CAPI and MAPI but rather a recomputation of the conditions based on other fields.
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
			func(m *mapiv1beta1.MachineSetSpec, c randfill.Continue) {
				c.FillNoCustom(m)

				// Clear fields that are not supported in the machine template objectmeta.
				m.Template.ObjectMeta.Name = ""
				m.Template.ObjectMeta.GenerateName = ""
				m.Template.ObjectMeta.Namespace = ""
				m.Template.ObjectMeta.OwnerReferences = nil
				// The conversion library while converting
				// machineSet.template labels/annotations from MAPI->CAPI
				// merges MAPI machineSet.template.spec.metadata.labels/annotations
				// and MAPI machineSet.template.metadata.labels/annotations
				// into CAPI machineSet.template.metadata.labels/annotations
				// as that's the only place for metadata that CAPI has.
				// When they are back-converted from CAPI->MAPI
				// the conversion library converts CAPI machineSet.template.metadata.labels/annotations
				// it does so to both to MAPI machineSet.template.spec.metadata.labels/annotations and
				// MAPI machineSet.template.metadata.labels/annotations
				// So these should match when we generate the initial MAPI MachineSet
				// so we get the same MAPI MachineSet back after the roundtrip.
				m.Template.Spec.ObjectMeta.Labels = util.DeepCopyMapStringString(m.Template.ObjectMeta.Labels)
				m.Template.Spec.ObjectMeta.Annotations = util.DeepCopyMapStringString(m.Template.ObjectMeta.Annotations)

				fuzzMAPIMachineSetSpecDeletePolicy(&m.DeletePolicy, c)

				// Clear the authoritative API since that's not relevant for conversion.
				m.AuthoritativeAPI = ""
			},
			func(m *mapiv1beta1.MachineSetStatus, c randfill.Continue) {
				c.FillNoCustom(m)

				m.ObservedGeneration = 0     // Ignore, this field as it shouldn't match between CAPI and MAPI.
				m.AuthoritativeAPI = ""      // Ignore, this field as it is not present in CAPI.
				m.SynchronizedGeneration = 0 // Ignore, this field as it is not present in CAPI.
				m.Conditions = nil           // Ignore, this field as it is not a 1:1 mapping between CAPI and MAPI but rather a recomputation of the conditions based on other fields.
			},
		}
	}
}

// fuzzMAPIMachineSetSpecDeletePolicy fuzzes a single MAPI MachineSetDeletePolicy with valid values.
func fuzzMAPIMachineSetSpecDeletePolicy(deletePolicy *string, c randfill.Continue) {
	switch c.Int31n(3) {
	case 0:
		*deletePolicy = string(mapiv1beta1.RandomMachineSetDeletePolicy)
	case 1:
		*deletePolicy = string(mapiv1beta1.NewestMachineSetDeletePolicy)
	case 2:
		*deletePolicy = string(mapiv1beta1.OldestMachineSetDeletePolicy)
		// case 3:
		// 	*deletePolicy = "" // Do not fuzz MAPI MachineSetDeletePolicy to the empty value.
		// It will otherwise get converted to CAPI RandomMachineSetDeletePolicy (default in CAPI) which
		// if converted back to MAPI will become RandomMachineSetDeletePolicy,
		// resulting in a known lossy rountrip conversion, which would make the test to fail.
		// This is not an issue in real conditions as the defaults are the same for CAPI and MAPI (Random).
	} //nolint:wsl
}

// fuzzCAPIMachineSetSpecDeletionOrder fuzzes a single CAPI MachineSetDeletePolicy with valid values.
func fuzzCAPIMachineSetSpecDeletionOrder(deletePolicy *clusterv1.MachineSetDeletionOrder, c randfill.Continue) {
	switch c.Int31n(3) {
	case 0:
		*deletePolicy = clusterv1.RandomMachineSetDeletionOrder
	case 1:
		*deletePolicy = clusterv1.NewestMachineSetDeletionOrder
	case 2:
		*deletePolicy = clusterv1.OldestMachineSetDeletionOrder
		// case 3:
		// 	*deletePolicy = "" // Do not fuzz CAPI MachineSetDeletePolicy to the empty value.
		// It will otherwise get converted to CAPI RandomMachineSetDeletePolicy (default in CAPI) which
		// if to MAPI will become RandomMachineSetDeletePolicy,
		// and converted back to CAPI will become RandomMachineSetDeletePolicy,
		// resulting in a known lossy rountrip conversion, which would make the test to fail.
		// This is not an issue in real conditions as the defaults are the same for CAPI and MAPI (Random).
	} //nolint:wsl
}

// fuzzMAPIMachineStatusAddress fuzzes a single MAPI machine status address with valid address types and randomized IP addresses.
//
//nolint:dupl
func fuzzMAPIMachineStatusAddress(address *corev1.NodeAddress, c randfill.Continue) {
	// Fuzz the address type to one of the valid types for MAPI machines
	// Based on the conversion code, MAPI supports: Hostname, ExternalIP, InternalIP
	// (ExternalDNS and InternalDNS are not supported in MAPI conversion)
	switch c.Int31n(5) {
	case 0:
		address.Type = corev1.NodeHostName
		// Generate a random hostname
		address.Address = fmt.Sprintf("node-%d.example.com", c.Int31n(1000))
	case 1:
		address.Type = corev1.NodeExternalIP
		// Generate a random external IP address (public IP range)
		address.Address = fmt.Sprintf("%d.%d.%d.%d",
			c.Int31n(223)+1, // 1-223 (avoid 0.x.x.x and 224+ multicast)
			c.Int31n(256),   // 0-255
			c.Int31n(256),   // 0-255
			c.Int31n(254)+1) // 1-254 (avoid .0 and .255)
	case 2:
		address.Type = corev1.NodeInternalIP
		// Generate a random internal IP address (private IP ranges)
		switch c.Int31n(3) {
		case 0:
			// 10.0.0.0/8
			address.Address = fmt.Sprintf("10.%d.%d.%d",
				c.Int31n(256), c.Int31n(256), c.Int31n(254)+1)
		case 1:
			// 172.16.0.0/12
			address.Address = fmt.Sprintf("172.%d.%d.%d",
				c.Int31n(16)+16, c.Int31n(256), c.Int31n(254)+1)
		case 2:
			// 192.168.0.0/16
			address.Address = fmt.Sprintf("192.168.%d.%d",
				c.Int31n(256), c.Int31n(254)+1)
		}
	case 3:
		address.Type = corev1.NodeExternalDNS
		address.Address = fmt.Sprintf("node-%d.example.com", c.Int31n(1000))
	case 4:
		address.Type = corev1.NodeInternalDNS
		address.Address = fmt.Sprintf("node-%d.example.com", c.Int31n(1000))
	}
}

// fuzzMAPIMachineStatusAddresses fuzzes a slice of MAPI machine status addresses with randomized count and content.
func fuzzMAPIMachineStatusAddresses(addresses *[]corev1.NodeAddress, c randfill.Continue) {
	// Randomize the number of addresses (0-3 addresses)
	count := c.Int31n(4)
	*addresses = make([]corev1.NodeAddress, count)

	// Fuzz each address
	for i := range *addresses {
		fuzzMAPIMachineStatusAddress(&(*addresses)[i], c)
	}
}

// fuzzMAPIMachineStatusPhase fuzzes a single MAPI machine status phase with valid phases.
func fuzzMAPIMachineStatusPhase(phase *string, c randfill.Continue) {
	if phase == nil {
		phase = ptr.To("")
	}

	switch c.Int31n(5) {
	case 0:
		*phase = "Running"
	case 1:
		*phase = "Provisioning"
	case 2:
		*phase = "Provisioned"
	case 3:
		*phase = "Deleting"
	case 4:
		*phase = "Failed"
	}
}

// fuzzCAPIMachineStatusAddresses fuzzes a slice of CAPI machine status addresses with randomized count and content.
func fuzzCAPIMachineStatusAddresses(addresses *clusterv1.MachineAddresses, c randfill.Continue) {
	// Randomize the number of addresses (0-3 addresses)
	count := c.Int31n(4)
	*addresses = make(clusterv1.MachineAddresses, count)

	// Fuzz each address
	for i := range *addresses {
		fuzzCAPIMachineStatusAddress(&(*addresses)[i], c)
	}
}

// fuzzCAPIMachineStatusPhase fuzzes a single CAPI machine status phase with valid phases.
func fuzzCAPIMachineStatusPhase(phase *string, c randfill.Continue) {
	if phase == nil {
		phase = ptr.To("")
	}

	switch c.Int31n(8) {
	case 0:
		*phase = string(clusterv1.MachinePhasePending)
	case 1:
		*phase = string(clusterv1.MachinePhaseRunning)
	case 2:
		*phase = string(clusterv1.MachinePhaseProvisioning)
	case 3:
		*phase = string(clusterv1.MachinePhaseProvisioned)
	case 4:
		*phase = string(clusterv1.MachinePhaseDeleting)
	case 5:
		*phase = string(clusterv1.MachinePhaseFailed)
	case 6:
		*phase = string(clusterv1.MachinePhaseDeleted)
	case 7:
		*phase = string(clusterv1.MachinePhaseUnknown)
	}
}

// fuzzCAPIMachineStatusAddress fuzzes a single CAPI machine status address with valid address types and randomized IP addresses.
//
//nolint:dupl
func fuzzCAPIMachineStatusAddress(address *clusterv1.MachineAddress, c randfill.Continue) {
	// Fuzz the address type to one of the valid types for CAPI machines
	// Based on the conversion code, CAPI supports: Hostname, ExternalIP, InternalIP
	// (ExternalDNS and InternalDNS are not supported in CAPI conversion)
	switch c.Int31n(5) {
	case 0:
		address.Type = clusterv1.MachineHostName
		// Generate a random hostname
		address.Address = fmt.Sprintf("node-%d.example.com", c.Int31n(1000))
	case 1:
		address.Type = clusterv1.MachineExternalIP
		// Generate a random external IP address (public IP range)
		address.Address = fmt.Sprintf("%d.%d.%d.%d",
			c.Int31n(223)+1, // 1-223 (avoid 0.x.x.x and 224+ multicast)
			c.Int31n(256),   // 0-255
			c.Int31n(256),   // 0-255
			c.Int31n(254)+1) // 1-254 (avoid .0 and .255)
	case 2:
		address.Type = clusterv1.MachineInternalIP
		// Generate a random internal IP address (private IP ranges)
		switch c.Int31n(3) {
		case 0:
			// 10.0.0.0/8
			address.Address = fmt.Sprintf("10.%d.%d.%d",
				c.Int31n(256), c.Int31n(256), c.Int31n(254)+1)
		case 1:
			// 172.16.0.0/12
			address.Address = fmt.Sprintf("172.%d.%d.%d",
				c.Int31n(16)+16, c.Int31n(256), c.Int31n(254)+1)
		case 2:
			// 192.168.0.0/16
			address.Address = fmt.Sprintf("192.168.%d.%d",
				c.Int31n(256), c.Int31n(254)+1)
		}
	case 3:
		address.Type = clusterv1.MachineExternalDNS
		// Generate a random external DNS address
		address.Address = fmt.Sprintf("node-%d.example.com", c.Int31n(1000))
	case 4:
		address.Type = clusterv1.MachineInternalDNS
		// Generate a random internal DNS address
		address.Address = fmt.Sprintf("node-%d.example.com", c.Int31n(1000))
	}
}
