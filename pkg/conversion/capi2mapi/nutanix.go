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

	nutanixv1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	mapiv1 "github.com/openshift/api/machine/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var (
	errCAPIMachineNutanixMachineNutanixClusterCannotBeNil            = errors.New("provided Machine, NutanixMachine and NutanixCluster can not be nil")
	errCAPIMachineSetNutanixMachineTemplateNutanixClusterCannotBeNil = errors.New("provided MachineSet, NutanixMachineTemplate and NutanixCluster can not be nil")
)

// machineAndNutanixMachineAndNutanixCluster stores the details of a Cluster API Machine and OpenStackMachine and OpenStackCluster.
type machineAndNutanixMachineAndNutanixCluster struct {
	machine        *clusterv1.Machine
	nutanixMachine *nutanixv1.NutanixMachine
	nutanixCluster *nutanixv1.NutanixCluster
}

// machineSetAndNutanixMachineTemplateAndNutanixCluster stores the details of a Cluster API MachineSet and NutanixMachineTemplate and NutanixCluster.
type machineSetAndNutanixMachineTemplateAndNutanixCluster struct {
	machineSet     *clusterv1.MachineSet
	template       *nutanixv1.NutanixMachineTemplate
	nutanixCluster *nutanixv1.NutanixCluster
	*machineAndNutanixMachineAndNutanixCluster
}

// FromMachineAndNutanixMachineAndNutanixCluster wraps a CAPI Machine and CAPO NutanixMachine and CAPO NutanixCluster into a capi2mapi MachineAndInfrastructureMachine.
func FromMachineAndNutanixMachineAndNutanixCluster(m *clusterv1.Machine, am *nutanixv1.NutanixMachine, ac *nutanixv1.NutanixCluster) MachineAndInfrastructureMachine {
	return &machineAndNutanixMachineAndNutanixCluster{machine: m, nutanixMachine: am, nutanixCluster: ac}
}

func FromMachineSetAndNutanixMachineTemplateAndNutanixCluster(
	ms *clusterv1.MachineSet, mts *nutanixv1.NutanixMachineTemplate, ac *nutanixv1.NutanixCluster,
) MachineSetAndMachineTemplate {
	return &machineSetAndNutanixMachineTemplateAndNutanixCluster{
		machineSet:     ms,
		template:       mts,
		nutanixCluster: ac,
		machineAndNutanixMachineAndNutanixCluster: &machineAndNutanixMachineAndNutanixCluster{
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      ms.Spec.Template.ObjectMeta.Labels,
					Annotations: ms.Spec.Template.ObjectMeta.Annotations,
				},
				Spec: ms.Spec.Template.Spec,
			},
			nutanixMachine: &nutanixv1.NutanixMachine{
				Spec: mts.Spec.Template.Spec,
			},
			nutanixCluster: ac,
		},
	}
}

func (m machineAndNutanixMachineAndNutanixCluster) toProviderSpec() (*mapiv1.NutanixMachineProviderConfig, []string, field.ErrorList) {
	var (
		errors   field.ErrorList
		warnings []string
	)

	mapiProviderConfig := &mapiv1.NutanixMachineProviderConfig{
		TypeMeta: metav1.TypeMeta{
			Kind:       "OpenstackProviderSpec",
			APIVersion: "machine.openshift.io/v1alpha1",
		},
		VCPUsPerSocket: m.nutanixMachine.Spec.VCPUsPerSocket,
		VCPUSockets:    m.nutanixMachine.Spec.VCPUSockets,
		MemorySize:     m.nutanixMachine.Spec.MemorySize,
		SystemDiskSize: m.nutanixMachine.Spec.SystemDiskSize,
		Image: func() mapiv1.NutanixResourceIdentifier {
			if m.nutanixMachine.Spec.Image != nil {
				return mapiv1.NutanixResourceIdentifier{
					Type: mapiv1.NutanixIdentifierType(m.nutanixMachine.Spec.Image.Type),
					Name: m.nutanixMachine.Spec.Image.Name,
					UUID: m.nutanixMachine.Spec.Image.UUID,
				}
			}
			return mapiv1.NutanixResourceIdentifier{}
		}(),
		Cluster: func() mapiv1.NutanixResourceIdentifier {
			cluster := m.nutanixMachine.Spec.Cluster
			return mapiv1.NutanixResourceIdentifier{
				Type: mapiv1.NutanixIdentifierType(cluster.Type),
				Name: cluster.Name,
				UUID: cluster.UUID,
			}
		}(),
		Subnets: func() []mapiv1.NutanixResourceIdentifier {
			subnets := m.nutanixMachine.Spec.Subnets
			result := make([]mapiv1.NutanixResourceIdentifier, len(subnets))
			for i, s := range subnets {
				result[i] = mapiv1.NutanixResourceIdentifier{
					Type: mapiv1.NutanixIdentifierType(s.Type),
					Name: s.Name,
					UUID: s.UUID,
				}
			}
			return result
		}(),
		Project: func() mapiv1.NutanixResourceIdentifier {
			if m.nutanixMachine.Spec.Project != nil {
				return mapiv1.NutanixResourceIdentifier{
					Type: mapiv1.NutanixIdentifierType(m.nutanixMachine.Spec.Project.Type),
					Name: m.nutanixMachine.Spec.Project.Name,
					UUID: m.nutanixMachine.Spec.Project.UUID,
				}
			}
			return mapiv1.NutanixResourceIdentifier{}
		}(),
		BootType: mapiv1.NutanixBootType(m.nutanixMachine.Spec.BootType),
		DataDisks: func() []mapiv1.NutanixVMDisk {
			disks := m.nutanixMachine.Spec.DataDisks
			result := make([]mapiv1.NutanixVMDisk, len(disks))
			for i, d := range disks {
				result[i] = mapiv1.NutanixVMDisk{
					DiskSize: d.DiskSize,
					DataSource: &mapiv1.NutanixResourceIdentifier{
						Type: mapiv1.NutanixIdentifierType(d.DataSource.Type),
						Name: d.DataSource.Name,
						UUID: d.DataSource.UUID,
					},
				}
			}
			return result
		}(),
		GPUs: func() []mapiv1.NutanixGPU {
			gpus := m.nutanixMachine.Spec.GPUs
			result := make([]mapiv1.NutanixGPU, len(gpus))
			for i, g := range gpus {
				result[i] = mapiv1.NutanixGPU{
					Type: mapiv1.NutanixGPUIdentifierType(g.Type),
					Name: g.Name,
					DeviceID: func(id *int64) *int32 {
						if id == nil {
							return nil
						}
						val := int32(*id)
						return &val
					}(g.DeviceID),
				}
			}
			return result
		}(),
	}
	if len(errors) > 0 {
		return nil, warnings, errors
	}

	return mapiProviderConfig, warnings, nil
}

func nutanixRawExtensionFromProviderSpec(spec *mapiv1.NutanixMachineProviderConfig) (*runtime.RawExtension, error) {
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

// ToMachine converts a capi2mapi MachineAndOpenStackMachineTemplate into a MAPI Machine.
func (m machineAndNutanixMachineAndNutanixCluster) ToMachine() (*mapiv1beta1.Machine, []string, error) {
	if m.machine == nil || m.nutanixMachine == nil || m.nutanixCluster == nil {
		return nil, nil, errCAPIMachineNutanixMachineNutanixClusterCannotBeNil
	}

	var (
		errors   field.ErrorList
		warnings []string
	)

	mapiSpec, warns, errs := m.toProviderSpec()
	if errs != nil {
		errors = append(errors, errs...)
	}

	nutanixRawExt, errRaw := nutanixRawExtensionFromProviderSpec(mapiSpec)
	if errRaw != nil {
		return nil, nil, fmt.Errorf("unable to convert OpenStack providerSpec to raw extension: %w", errRaw)
	}

	warnings = append(warnings, warns...)

	mapiMachine, fieldErrs := fromCAPIMachineToMAPIMachine(m.machine)
	if fieldErrs != nil {
		errors = append(errors, fieldErrs...)
	}

	mapiMachine.Spec.ProviderSpec.Value = nutanixRawExt

	if len(errors) > 0 {
		return nil, warnings, errors.ToAggregate()
	}

	return mapiMachine, warnings, nil
}

// ToMachineSet converts a capi2mapi MachineAndOpenStackMachineTemplate into a MAPI MachineSet.
func (m machineSetAndNutanixMachineTemplateAndNutanixCluster) ToMachineSet() (*mapiv1beta1.MachineSet, []string, error) { //nolint:dupl
	if m.machineSet == nil || m.template == nil || m.nutanixCluster == nil || m.machineAndNutanixMachineAndNutanixCluster == nil {
		return nil, nil, errCAPIMachineSetNutanixMachineTemplateNutanixClusterCannotBeNil
	}

	var (
		errors   []error
		warnings []string
	)

	// Run the full ToMachine conversion so that we can check for
	// any Machine level conversion errors in the spec translation.
	mapiMachine, warns, err := m.ToMachine()
	if err != nil {
		errors = append(errors, err)
	}

	warnings = append(warnings, warns...)

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
