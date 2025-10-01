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
package capi2mapi

import (
	"encoding/json"
	"errors"
	"fmt"

	metal3v1 "github.com/metal3-io/cluster-api-provider-metal3/api/v1beta1"
	baremetalv1 "github.com/openshift/cluster-api-provider-baremetal/pkg/apis/baremetal/v1alpha1"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var (
	errCAPIMachineMetal3MachineMetal3ClusterCannotBeNil            = errors.New("provided Machine, Metal3Machine and Metal3Cluster can not be nil")
	errCAPIMachineSetMetal3MachineTemplateMetal3ClusterCannotBeNil = errors.New("provided MachineSet, Metal3MachineTemplate and Metal3Cluster can not be nil")
)

// machineAndMetal3MachineAndMetal3Cluster stores the details of a Cluster API Machine and Metal3Machine and Metal3Cluster.
type machineAndMetal3MachineAndMetal3Cluster struct {
	machine       *clusterv1.Machine
	metal3Machine *metal3v1.Metal3Machine
	metal3Cluster *metal3v1.Metal3Cluster
}

// machineSetAndMetal3MachineTemplateAndMetal3Cluster stores the details of a Cluster API MachineSet and Metal3MachineTemplate and Metal3Cluster.
type machineSetAndMetal3MachineTemplateAndMetal3Cluster struct {
	machineSet *clusterv1.MachineSet
	template   *metal3v1.Metal3MachineTemplate
	*machineAndMetal3MachineAndMetal3Cluster
}

// FromMachineAndMetal3MachineAndMetal3Cluster wraps a CAPI Machine and CAPM3 Metal3Machine and CAPM3 Metal3Cluster into a capi2mapi MachineAndInfrastructureMachine.
func FromMachineAndMetal3MachineAndMetal3Cluster(m *clusterv1.Machine, mm *metal3v1.Metal3Machine, mc *metal3v1.Metal3Cluster) MachineAndInfrastructureMachine {
	return &machineAndMetal3MachineAndMetal3Cluster{machine: m, metal3Machine: mm, metal3Cluster: mc}
}

// FromMachineSetAndMetal3MachineTemplateAndMetal3Cluster wraps a CAPI MachineSet and CAPM3 Metal3MachineTemplate and CAPM3 Metal3Cluster into a capi2mapi MachineSetAndMachineTemplate.
func FromMachineSetAndMetal3MachineTemplateAndMetal3Cluster(ms *clusterv1.MachineSet, mts *metal3v1.Metal3MachineTemplate, mc *metal3v1.Metal3Cluster) MachineSetAndMachineTemplate {
	return &machineSetAndMetal3MachineTemplateAndMetal3Cluster{
		machineSet: ms,
		template:   mts,
		machineAndMetal3MachineAndMetal3Cluster: &machineAndMetal3MachineAndMetal3Cluster{
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      ms.Spec.Template.ObjectMeta.Labels,
					Annotations: ms.Spec.Template.ObjectMeta.Annotations,
				},
				Spec: ms.Spec.Template.Spec,
			},
			metal3Machine: &metal3v1.Metal3Machine{
				Spec: mts.Spec.Template.Spec,
			},
			metal3Cluster: mc,
		},
	}
}

// toProviderSpec converts a capi2mapi MachineAndMetal3MachineAndMetal3Cluster into a MAPI BareMetalMachineProviderSpec.
//
//nolint:unparam
func (m machineAndMetal3MachineAndMetal3Cluster) toProviderSpec() (*baremetalv1.BareMetalMachineProviderSpec, []string, field.ErrorList) {
	var (
		warnings []string
		errors   field.ErrorList
	)

	fldPath := field.NewPath("spec")

	mapbProviderConfig := baremetalv1.BareMetalMachineProviderSpec{
		TypeMeta: metav1.TypeMeta{
			Kind:       "BareMetalMachineProviderSpec",
			APIVersion: "baremetal.cluster.k8s.io/v1alpha1",
		},
		// ObjectMeta - Only present because it's needed to form part of the runtime.RawExtension, not actually used by the provider.
		Image:        convertMetal3ImageToBareMetal(m.metal3Machine.Spec.Image),
		CustomDeploy: convertMetal3CustomDeployToBareMetal(m.metal3Machine.Spec.CustomDeploy),
		UserData:     m.metal3Machine.Spec.UserData,
		HostSelector: convertMetal3HostSelectorToBareMetal(m.metal3Machine.Spec.HostSelector),
	}

	// Below this line are fields not used from the CAPI Metal3Machine.

	// ProviderID - Populated at a different level.
	// DataTemplate - Not present in MAPI, not used in OpenShift.
	// MetaData - Not present in MAPI, not used in OpenShift.
	// NetworkData - Not present in MAPI, not used in OpenShift.
	// AutomatedCleaningMode - Not present in MAPI, not used in OpenShift.

	// There are quite a few unsupported fields, so break them out for now.
	errors = append(errors, handleUnsupportedMetal3MachineFields(fldPath, m.metal3Machine.Spec)...)

	if len(errors) > 0 {
		return nil, warnings, errors
	}

	return &mapbProviderConfig, warnings, nil
}

// ToMachine converts a capi2mapi MachineAndMetal3Machine into a MAPI Machine.
func (m machineAndMetal3MachineAndMetal3Cluster) ToMachine() (*mapiv1beta1.Machine, []string, error) {
	if m.machine == nil || m.metal3Machine == nil || m.metal3Cluster == nil {
		return nil, nil, errCAPIMachineMetal3MachineMetal3ClusterCannotBeNil
	}

	var (
		errors   field.ErrorList
		warnings []string
	)

	mapbSpec, warn, err := m.toProviderSpec()
	if err != nil {
		errors = append(errors, err...)
	}

	bareMetalRawExt, errRaw := bareMetalRawExtensionFromProviderSpec(mapbSpec)
	if errRaw != nil {
		return nil, nil, fmt.Errorf("unable to convert BareMetal providerSpec to raw extension: %w", errRaw)
	}

	warnings = append(warnings, warn...)

	mapiMachine, err := fromCAPIMachineToMAPIMachine(m.machine)
	if err != nil {
		errors = append(errors, err...)
	}

	mapiMachine.Spec.ProviderSpec.Value = bareMetalRawExt

	if len(errors) > 0 {
		return nil, warnings, errors.ToAggregate()
	}

	return mapiMachine, warnings, nil
}

// ToMachineSet converts a capi2mapi MachineAndMetal3MachineTemplate into a MAPI MachineSet.
//
//nolint:dupl
func (m machineSetAndMetal3MachineTemplateAndMetal3Cluster) ToMachineSet() (*mapiv1beta1.MachineSet, []string, error) {
	if m.machineSet == nil || m.template == nil || m.metal3Cluster == nil || m.machineAndMetal3MachineAndMetal3Cluster == nil {
		return nil, nil, errCAPIMachineSetMetal3MachineTemplateMetal3ClusterCannotBeNil
	}

	var (
		errors   []error
		warnings []string
	)

	// Run the full ToMachine conversion so that we can check for
	// any Machine level conversion errors in the spec translation.
	mapiMachine, warn, err := m.ToMachine()
	if err != nil {
		errors = append(errors, err)
	}

	warnings = append(warnings, warn...)

	mapiMachineSet, err := fromCAPIMachineSetToMAPIMachineSet(m.machineSet)
	if err != nil {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		return nil, warnings, utilerrors.NewAggregate(errors)
	}

	mapiMachineSet.Spec.Template.Spec = mapiMachine.Spec

	// Copy the labels and annotations from the Machine to the template.
	mapiMachineSet.Spec.Template.ObjectMeta.Annotations = mapiMachine.ObjectMeta.Annotations
	mapiMachineSet.Spec.Template.ObjectMeta.Labels = mapiMachine.ObjectMeta.Labels

	return mapiMachineSet, warnings, nil
}

//////// Conversion helpers

func convertMetal3ImageToBareMetal(capiImage metal3v1.Image) baremetalv1.Image {
	return baremetalv1.Image{
		URL:      capiImage.URL,
		Checksum: capiImage.Checksum,
		// ChecksumType: Not present in MAPI, ignore.
		// DiskFormat: Not present in MAPI, ignore.
	}
}

func convertMetal3CustomDeployToBareMetal(capiCustomDeploy *metal3v1.CustomDeploy) baremetalv1.CustomDeploy {
	if capiCustomDeploy == nil {
		return baremetalv1.CustomDeploy{}
	}

	return baremetalv1.CustomDeploy{
		Method: capiCustomDeploy.Method,
	}
}

func convertMetal3HostSelectorToBareMetal(capiHostSelector metal3v1.HostSelector) baremetalv1.HostSelector {
	return baremetalv1.HostSelector{
		MatchLabels:      capiHostSelector.MatchLabels,
		MatchExpressions: convertMetal3HostSelectorRequirementsToBareMetal(capiHostSelector.MatchExpressions),
	}
}

func convertMetal3HostSelectorRequirementsToBareMetal(capiRequirements []metal3v1.HostSelectorRequirement) []baremetalv1.HostSelectorRequirement {
	if capiRequirements == nil {
		return nil
	}

	mapiRequirements := make([]baremetalv1.HostSelectorRequirement, 0, len(capiRequirements))
	for _, req := range capiRequirements {
		mapiRequirements = append(mapiRequirements, baremetalv1.HostSelectorRequirement{
			Key:      req.Key,
			Operator: req.Operator,
			Values:   req.Values,
		})
	}

	return mapiRequirements
}

// handleUnsupportedMetal3MachineFields returns an error for every present field in the Metal3MachineSpec that
// we are currently, or indefinitely not supporting.
func handleUnsupportedMetal3MachineFields(fldPath *field.Path, spec metal3v1.Metal3MachineSpec) field.ErrorList {
	errs := field.ErrorList{}

	if spec.DataTemplate != nil {
		// DataTemplate is not present in MAPI and not used in OpenShift.
		errs = append(errs, field.Invalid(fldPath.Child("dataTemplate"), spec.DataTemplate, "dataTemplate is not supported"))
	}

	if spec.MetaData != nil {
		// MetaData is not present in MAPI and not used in OpenShift.
		errs = append(errs, field.Invalid(fldPath.Child("metaData"), spec.MetaData, "metaData is not supported"))
	}

	if spec.NetworkData != nil {
		// NetworkData is not present in MAPI and not used in OpenShift.
		errs = append(errs, field.Invalid(fldPath.Child("networkData"), spec.NetworkData, "networkData is not supported"))
	}

	if spec.AutomatedCleaningMode != nil && ptr.Deref(spec.AutomatedCleaningMode, "") != "" {
		// AutomatedCleaningMode is not present in MAPI and not used in OpenShift.
		errs = append(errs, field.Invalid(fldPath.Child("automatedCleaningMode"), spec.AutomatedCleaningMode, "automatedCleaningMode is not supported"))
	}

	return errs
}

// bareMetalRawExtensionFromProviderSpec marshals the machine provider spec.
func bareMetalRawExtensionFromProviderSpec(spec *baremetalv1.BareMetalMachineProviderSpec) (*runtime.RawExtension, error) {
	if spec == nil {
		return &runtime.RawExtension{}, nil
	}

	rawBytes, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("error marshalling providerSpec: %w", err)
	}

	return &runtime.RawExtension{
		Raw: rawBytes,
	}, nil
}
