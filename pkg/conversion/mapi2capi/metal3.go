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
package mapi2capi

import (
	"fmt"
	"reflect"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	baremetalv1 "github.com/openshift/cluster-api-provider-baremetal/pkg/apis/baremetal/v1alpha1"

	metal3v1 "github.com/metal3-io/cluster-api-provider-metal3/api/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	metal3MachineKind         = "Metal3Machine"
	metal3MachineTemplateKind = "Metal3MachineTemplate"
)

// metal3MachineAndInfra stores the details of a Machine API BareMetalMachine and Infra.
type metal3MachineAndInfra struct {
	machine        *mapiv1beta1.Machine
	infrastructure *configv1.Infrastructure
}

// metal3MachineSetAndInfra stores the details of a Machine API BareMetalMachine set and Infra.
type metal3MachineSetAndInfra struct {
	machineSet     *mapiv1beta1.MachineSet
	infrastructure *configv1.Infrastructure
	*metal3MachineAndInfra
}

// FromMetal3MachineAndInfra wraps a Machine API Machine for Metal3/BareMetal and the OCP Infrastructure object into a mapi2capi Metal3ProviderSpec.
func FromMetal3MachineAndInfra(m *mapiv1beta1.Machine, i *configv1.Infrastructure) Machine {
	return &metal3MachineAndInfra{machine: m, infrastructure: i}
}

// FromMetal3MachineSetAndInfra wraps a Machine API MachineSet for Metal3/BareMetal and the OCP Infrastructure object into a mapi2capi Metal3ProviderSpec.
func FromMetal3MachineSetAndInfra(m *mapiv1beta1.MachineSet, i *configv1.Infrastructure) MachineSet {
	return &metal3MachineSetAndInfra{
		machineSet:     m,
		infrastructure: i,
		metal3MachineAndInfra: &metal3MachineAndInfra{
			machine: &mapiv1beta1.Machine{
				Spec: m.Spec.Template.Spec,
			},
			infrastructure: i,
		},
	}
}

// ToMachineAndInfrastructureMachine is used to generate a CAPI Machine and the corresponding InfrastructureMachine
// from the stored MAPI Machine and Infrastructure objects.
func (m *metal3MachineAndInfra) ToMachineAndInfrastructureMachine() (*clusterv1.Machine, client.Object, []string, error) {
	capiMachine, capbMachine, warnings, errs := m.toMachineAndInfrastructureMachine()

	if len(errs) > 0 {
		return nil, nil, warnings, errs.ToAggregate()
	}

	return capiMachine, capbMachine, warnings, nil
}

func (m *metal3MachineAndInfra) toMachineAndInfrastructureMachine() (*clusterv1.Machine, client.Object, []string, field.ErrorList) {
	var (
		errs     field.ErrorList
		warnings []string
	)

	baremetalProviderConfig, err := BareMetalProviderSpecFromRawExtension(m.machine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, nil, nil, field.ErrorList{field.Invalid(field.NewPath("spec", "providerSpec", "value"), m.machine.Spec.ProviderSpec.Value, err.Error())}
	}

	capbMachine, warn, machineErrs := m.toMetal3Machine(baremetalProviderConfig)
	if machineErrs != nil {
		errs = append(errs, machineErrs...)
	}

	warnings = append(warnings, warn...)

	capiMachine, machineErrs := fromMAPIMachineToCAPIMachine(m.machine, metal3v1.GroupVersion.String(), metal3MachineKind)
	if machineErrs != nil {
		errs = append(errs, machineErrs...)
	}

	// Extract and plug ProviderID on CAPM3, if the providerID is present on CAPI (instance has been provisioned).
	if capiMachine.Spec.ProviderID != nil {
		capbMachine.Spec.ProviderID = capiMachine.Spec.ProviderID
	}

	// Plug into Core CAPI Machine fields that come from the MAPI ProviderConfig which belong here instead of the CAPI Metal3MachineTemplate.
	if baremetalProviderConfig.UserData != nil && baremetalProviderConfig.UserData.Name != "" {
		capiMachine.Spec.Bootstrap = clusterv1.Bootstrap{
			DataSecretName: &baremetalProviderConfig.UserData.Name,
		}
	}

	// Populate the CAPI Machine ClusterName from the OCP Infrastructure object.
	if m.infrastructure == nil || m.infrastructure.Status.InfrastructureName == "" {
		errs = append(errs, field.Invalid(field.NewPath("infrastructure", "status", "infrastructureName"), m.infrastructure.Status.InfrastructureName, "infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachine.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		capiMachine.Labels[clusterv1.ClusterNameLabel] = m.infrastructure.Status.InfrastructureName
	}

	// The InfraMachine should always have the same labels and annotations as the Machine.
	// See https://github.com/kubernetes-sigs/cluster-api/blob/f88d7ae5155700c2cc367b31ddcc151c9ad579e4/internal/controllers/machineset/machineset_controller.go#L578-L579
	capiMachineAnnotations := capiMachine.GetAnnotations()
	if len(capiMachineAnnotations) > 0 {
		capbMachine.SetAnnotations(capiMachineAnnotations)
	}

	capiMachineLabels := capiMachine.GetLabels()
	if len(capiMachineLabels) > 0 {
		capbMachine.SetLabels(capiMachineLabels)
	}

	return capiMachine, capbMachine, warnings, errs
}

// ToMachineSetAndMachineTemplate converts a mapi2capi Metal3MachineSetAndInfra into a CAPI MachineSet and CAPM3 Metal3MachineTemplate.
//
//nolint:dupl
func (m *metal3MachineSetAndInfra) ToMachineSetAndMachineTemplate() (*clusterv1.MachineSet, client.Object, []string, error) {
	var (
		errs     []error
		warnings []string
	)

	capiMachine, capbMachineObj, warn, errList := m.toMachineAndInfrastructureMachine()
	if errList != nil {
		errs = append(errs, errList.ToAggregate().Errors()...)
	}

	warnings = append(warnings, warn...)

	capbMachine, ok := capbMachineObj.(*metal3v1.Metal3Machine)
	if !ok {
		panic(fmt.Errorf("%w: %T", errUnexpectedObjectTypeForMachine, capbMachineObj))
	}

	capbMachineTemplate, err := metal3MachineToMetal3MachineTemplate(capbMachine, m.machineSet.Name, capiNamespace)
	if err != nil {
		errs = append(errs, err)
	}

	capiMachineSet, machineSetErrs := fromMAPIMachineSetToCAPIMachineSet(m.machineSet)
	if machineSetErrs != nil {
		errs = append(errs, machineSetErrs.Errors()...)
	}

	capiMachineSet.Spec.Template.Spec = capiMachine.Spec

	// We have to merge these two maps so that labels and annotations added to the template objectmeta are persisted
	// along with the labels and annotations from the machine objectmeta.
	capiMachineSet.Spec.Template.ObjectMeta.Labels = util.MergeMaps(capiMachineSet.Spec.Template.ObjectMeta.Labels, capiMachine.Labels)
	capiMachineSet.Spec.Template.ObjectMeta.Annotations = util.MergeMaps(capiMachineSet.Spec.Template.ObjectMeta.Annotations, capiMachine.Annotations)

	// Override the reference so that it matches the Metal3MachineTemplate.
	capiMachineSet.Spec.Template.Spec.InfrastructureRef.Kind = metal3MachineTemplateKind
	capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name = capbMachineTemplate.Name

	if m.infrastructure == nil || m.infrastructure.Status.InfrastructureName == "" {
		errs = append(errs, field.Invalid(field.NewPath("infrastructure", "status", "infrastructureName"), m.infrastructure.Status.InfrastructureName, "infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachineSet.Spec.Template.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		capiMachineSet.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		capiMachineSet.Labels[clusterv1.ClusterNameLabel] = m.infrastructure.Status.InfrastructureName
	}

	if len(errs) > 0 {
		return nil, nil, warnings, utilerrors.NewAggregate(errs)
	}

	return capiMachineSet, capbMachineTemplate, warnings, nil
}

// toMetal3Machine implements the ProviderSpec conversion interface for the Metal3 provider,
// it converts BareMetalMachineProviderSpec to Metal3Machine.
//
//nolint:unparam
func (m *metal3MachineAndInfra) toMetal3Machine(providerSpec baremetalv1.BareMetalMachineProviderSpec) (*metal3v1.Metal3Machine, []string, field.ErrorList) {
	fldPath := field.NewPath("spec", "providerSpec", "value")

	var (
		errs     field.ErrorList
		warnings []string
	)

	spec := metal3v1.Metal3MachineSpec{
		Image:        convertBareMetalImageToMetal3(providerSpec.Image),
		CustomDeploy: convertBareMetalCustomDeployToMetal3(providerSpec.CustomDeploy),
		UserData:     providerSpec.UserData,
		HostSelector: convertBareMetalHostSelectorToMetal3(providerSpec.HostSelector),
		// ProviderID: This is populated when this is called in higher level funcs (ToMachine(), ToMachineSet()).
		// DataTemplate: Not present in MAPI, not used in OpenShift.
		// MetaData: Not present in MAPI, not used in OpenShift.
		// NetworkData: Not present in MAPI, not used in OpenShift.
		// AutomatedCleaningMode: Not present in MAPI, not used in OpenShift.
	}

	// Unused fields - Below this line are fields not used from the MAPI BareMetalMachineProviderConfig.

	// TypeMeta - Only for the purpose of the raw extension, not used for any functionality.
	if !reflect.DeepEqual(providerSpec.ObjectMeta, metav1.ObjectMeta{}) {
		// We don't support setting the object metadata in the provider spec.
		// It's only present for the purpose of the raw extension and doesn't have any functionality.
		errs = append(errs, field.Invalid(fldPath.Child("metadata"), providerSpec.ObjectMeta, "metadata is not supported"))
	}

	return &metal3v1.Metal3Machine{
		TypeMeta: metav1.TypeMeta{
			APIVersion: metal3v1.GroupVersion.String(),
			Kind:       metal3MachineKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.machine.Name,
			Namespace: capiNamespace,
		},
		Spec: spec,
	}, warnings, errs
}

// BareMetalProviderSpecFromRawExtension unmarshals a raw extension into a BareMetalMachineProviderSpec type.
func BareMetalProviderSpecFromRawExtension(rawExtension *runtime.RawExtension) (baremetalv1.BareMetalMachineProviderSpec, error) {
	if rawExtension == nil {
		return baremetalv1.BareMetalMachineProviderSpec{}, nil
	}

	spec := baremetalv1.BareMetalMachineProviderSpec{}
	if err := yaml.Unmarshal(rawExtension.Raw, &spec); err != nil {
		return baremetalv1.BareMetalMachineProviderSpec{}, fmt.Errorf("error unmarshalling providerSpec: %w", err)
	}

	return spec, nil
}

func metal3MachineToMetal3MachineTemplate(metal3Machine *metal3v1.Metal3Machine, name string, namespace string) (*metal3v1.Metal3MachineTemplate, error) {
	nameWithHash, err := util.GenerateInfraMachineTemplateNameWithSpecHash(name, metal3Machine.Spec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate infrastructure machine template name with spec hash: %w", err)
	}

	return &metal3v1.Metal3MachineTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: metal3v1.GroupVersion.String(),
			Kind:       metal3MachineTemplateKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      nameWithHash,
			Namespace: namespace,
		},
		Spec: metal3v1.Metal3MachineTemplateSpec{
			Template: metal3v1.Metal3MachineTemplateResource{
				Spec: metal3Machine.Spec,
			},
		},
	}, nil
}

//////// Conversion helpers

func convertBareMetalImageToMetal3(mapiImage baremetalv1.Image) metal3v1.Image {
	return metal3v1.Image{
		URL:      mapiImage.URL,
		Checksum: mapiImage.Checksum,
		// ChecksumType: Not present in MAPI, will use CAPM3 default.
		// DiskFormat: Not present in MAPI, will use CAPM3 default.
	}
}

func convertBareMetalCustomDeployToMetal3(mapiCustomDeploy baremetalv1.CustomDeploy) *metal3v1.CustomDeploy {
	if mapiCustomDeploy.Method == "" {
		return nil
	}

	return &metal3v1.CustomDeploy{
		Method: mapiCustomDeploy.Method,
	}
}

func convertBareMetalHostSelectorToMetal3(mapiHostSelector baremetalv1.HostSelector) metal3v1.HostSelector {
	return metal3v1.HostSelector{
		MatchLabels:      mapiHostSelector.MatchLabels,
		MatchExpressions: convertBareMetalHostSelectorRequirementsToMetal3(mapiHostSelector.MatchExpressions),
	}
}

func convertBareMetalHostSelectorRequirementsToMetal3(mapiRequirements []baremetalv1.HostSelectorRequirement) []metal3v1.HostSelectorRequirement {
	if mapiRequirements == nil {
		return nil
	}

	capiRequirements := make([]metal3v1.HostSelectorRequirement, 0, len(mapiRequirements))
	for _, req := range mapiRequirements {
		capiRequirements = append(capiRequirements, metal3v1.HostSelectorRequirement{
			Key:      req.Key,
			Operator: req.Operator,
			Values:   req.Values,
		})
	}

	return capiRequirements
}
