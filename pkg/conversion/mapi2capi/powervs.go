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

package mapi2capi

import (
	"fmt"
	"reflect"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/util"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	ibmPowerVSMachineKind  = "IBMPowerVSMachine"
	ibmPowerVSTemplateKind = "IBMPowerVSMachineTemplate"
)

// powerVSMachineAndInfra stores the details of a Machine API PowerVSMachine and Infra.
type powerVSMachineAndInfra struct {
	machine        *mapiv1beta1.Machine
	infrastructure *configv1.Infrastructure
}

// powerVSMachineSetAndInfra stores the details of a Machine API PowerVSMachine and Infra.
type powerVSMachineSetAndInfra struct {
	machineSet     *mapiv1beta1.MachineSet
	infrastructure *configv1.Infrastructure
	*powerVSMachineAndInfra
}

// FromPowerVSMachineAndInfra wraps a Machine API Machine for PowerVS and the OCP Infrastructure object into a mapi2capi PowerVSProviderSpec.
func FromPowerVSMachineAndInfra(m *mapiv1beta1.Machine, i *configv1.Infrastructure) Machine {
	return &powerVSMachineAndInfra{machine: m, infrastructure: i}
}

// FromPowerVSMachineSetAndInfra wraps a Machine API MachineSet for Power VS and the OCP Infrastructure object into a mapi2capi PowerVSProviderSpec.
func FromPowerVSMachineSetAndInfra(m *mapiv1beta1.MachineSet, i *configv1.Infrastructure) MachineSet {
	return &powerVSMachineSetAndInfra{
		machineSet:     m,
		infrastructure: i,
		powerVSMachineAndInfra: &powerVSMachineAndInfra{
			machine: &mapiv1beta1.Machine{
				Spec: m.Spec.Template.Spec,
			},
			infrastructure: i,
		},
	}
}

// ToMachineAndInfrastructureMachine is used to generate a CAPI Machine and the corresponding InfrastructureMachine
// from the stored MAPI Machine and Infrastructure objects.
func (m *powerVSMachineAndInfra) ToMachineAndInfrastructureMachine() (*clusterv1.Machine, client.Object, []string, error) {
	capiMachine, powerVSMachine, warnings, errs := m.toMachineAndInfrastructureMachine()

	if len(errs) > 0 {
		return nil, nil, warnings, errs.ToAggregate()
	}

	return capiMachine, powerVSMachine, warnings, nil
}

func (m *powerVSMachineAndInfra) toMachineAndInfrastructureMachine() (*clusterv1.Machine, client.Object, []string, field.ErrorList) {
	var (
		errs     field.ErrorList
		warnings []string
	)

	powerVSProviderConfig, err := powerVSProviderSpecFromRawExtension(m.machine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, nil, nil, field.ErrorList{field.Invalid(field.NewPath("spec", "providerSpec", "value"), m.machine.Spec.ProviderSpec.Value, err.Error())}
	}

	capIBMPowerVSMachine, machineErrs := m.toPowerVSMachine(powerVSProviderConfig)
	if machineErrs != nil {
		errs = append(errs, machineErrs...)
	}

	capiMachine, machineErrs := fromMAPIMachineToCAPIMachine(m.machine, ibmpowervsv1.GroupVersion.Group, ibmPowerVSMachineKind)
	if machineErrs != nil {
		errs = append(errs, machineErrs...)
	}

	if powerVSProviderConfig.UserDataSecret != nil && powerVSProviderConfig.UserDataSecret.Name != "" {
		capiMachine.Spec.Bootstrap = clusterv1.Bootstrap{
			DataSecretName: &powerVSProviderConfig.UserDataSecret.Name,
		}
	}

	// Power VS does not support failure domains
	capiMachine.Spec.FailureDomain = ""

	// Populate the CAPI Machine ClusterName from the OCP Infrastructure object.
	if m.infrastructure == nil || m.infrastructure.Status.InfrastructureName == "" {
		errs = append(errs, field.Invalid(field.NewPath("infrastructure", "status", "infrastructureName"), m.infrastructure.Status.InfrastructureName, "infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachine.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		capiMachine.Labels[clusterv1.ClusterNameLabel] = m.infrastructure.Status.InfrastructureName
	}

	// The InfraMachine should always have the same labels and annotations as the Machine.
	// See https://github.com/kubernetes-sigs/cluster-api/blob/f88d7ae5155700c2cc367b31ddcc151c9ad579e4/internal/controllers/machineset/machineset_controller.go#L578-L579
	capIBMPowerVSMachine.SetAnnotations(capiMachine.GetAnnotations())
	capIBMPowerVSMachine.SetLabels(capiMachine.GetLabels())

	return capiMachine, capIBMPowerVSMachine, warnings, errs
}

// ToMachineSetAndMachineTemplate converts a mapi2capi PowerVSMachineSetAndInfra into a CAPI MachineSet and CAPIBM IBMPowerVSMachineTemplate.
//
//nolint:dupl
func (m *powerVSMachineSetAndInfra) ToMachineSetAndMachineTemplate() (*clusterv1.MachineSet, client.Object, []string, error) {
	var (
		errs     []error
		warnings []string
	)

	capiMachine, powerVSMachineObj, warn, errList := m.toMachineAndInfrastructureMachine()
	if errList != nil {
		errs = append(errs, errList.ToAggregate().Errors()...)
	}

	warnings = append(warnings, warn...)

	powerVSMachine, ok := powerVSMachineObj.(*ibmpowervsv1.IBMPowerVSMachine)
	if !ok {
		panic(fmt.Errorf("%w: %T", errUnexpectedObjectTypeForMachine, powerVSMachineObj))
	}

	powerVSMachineTemplate, err := powerVSMachineToPowerVSMachineTemplate(powerVSMachine, m.machineSet.Name, capiNamespace)
	if err != nil {
		errs = append(errs, err)
	}

	powerVSMachineSet, machineSetErrs := fromMAPIMachineSetToCAPIMachineSet(m.machineSet)
	if machineSetErrs != nil {
		errs = append(errs, machineSetErrs.Errors()...)
	}

	if capiMachine.Spec.MinReadySeconds == nil {
		capiMachine.Spec.MinReadySeconds = powerVSMachineSet.Spec.Template.Spec.MinReadySeconds
	}

	powerVSMachineSet.Spec.Template.Spec = capiMachine.Spec

	// We have to merge these two maps so that labels and annotations added to the template objectmeta are persisted
	// along with the labels and annotations from the machine objectmeta.
	powerVSMachineSet.Spec.Template.ObjectMeta.Labels = util.MergeMaps(powerVSMachineSet.Spec.Template.ObjectMeta.Labels, capiMachine.Labels)
	powerVSMachineSet.Spec.Template.ObjectMeta.Annotations = util.MergeMaps(powerVSMachineSet.Spec.Template.ObjectMeta.Annotations, capiMachine.Annotations)

	// Override the reference so that it matches the AWSMachineTemplate.
	powerVSMachineSet.Spec.Template.Spec.InfrastructureRef.Kind = ibmPowerVSTemplateKind
	powerVSMachineSet.Spec.Template.Spec.InfrastructureRef.Name = powerVSMachineTemplate.Name

	if m.infrastructure == nil || m.infrastructure.Status.InfrastructureName == "" {
		errs = append(errs, field.Invalid(field.NewPath("infrastructure", "status", "infrastructureName"), m.infrastructure.Status.InfrastructureName, "infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		powerVSMachineSet.Spec.Template.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		powerVSMachineSet.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		powerVSMachineSet.Labels[clusterv1.ClusterNameLabel] = m.infrastructure.Status.InfrastructureName
	}

	if len(errs) > 0 {
		return nil, nil, warnings, utilerrors.NewAggregate(errs)
	}

	return powerVSMachineSet, powerVSMachineTemplate, warnings, nil
}

// powerVSProviderSpecFromRawExtension unmarshalls a raw extension into an PowerVSMachineProviderSpec type.
func powerVSProviderSpecFromRawExtension(rawExtension *runtime.RawExtension) (mapiv1.PowerVSMachineProviderConfig, error) {
	if rawExtension == nil {
		return mapiv1.PowerVSMachineProviderConfig{}, nil
	}

	spec := mapiv1.PowerVSMachineProviderConfig{}
	if err := yaml.Unmarshal(rawExtension.Raw, &spec); err != nil {
		return mapiv1.PowerVSMachineProviderConfig{}, fmt.Errorf("error unmarshalling providerSpec: %w", err)
	}

	return spec, nil
}

// toPowerVSMachine converts PowerVSMachineProviderConfig to IBMPowerVSMachineSpec.
func (m *powerVSMachineAndInfra) toPowerVSMachine(providerSpec mapiv1.PowerVSMachineProviderConfig) (*ibmpowervsv1.IBMPowerVSMachine, field.ErrorList) {
	fldPath := field.NewPath("spec", "providerSpec", "value")

	var errs field.ErrorList

	serviceInstance, err := convertServiceInstanceToCAPI(fldPath.Child("serviceInstance"), providerSpec.ServiceInstance)
	if err != nil {
		errs = append(errs, err)
	}

	image, err := convertImageToCAPI(fldPath.Child("image"), providerSpec.Image)
	if err != nil {
		errs = append(errs, err)
	}

	network, err := convertNetworkToCAPI(fldPath.Child("network"), providerSpec.Network)
	if err != nil {
		errs = append(errs, err)
	}

	spec := ibmpowervsv1.IBMPowerVSMachineSpec{
		//	ServiceInstanceID: Deprecated, Use ServiceInstance.
		ServiceInstance: serviceInstance,
		SSHKey:          providerSpec.KeyPairName,
		Image:           image,
		// ImageRef: Not required as image is set above
		SystemType:    providerSpec.SystemType,
		ProcessorType: ibmpowervsv1.PowerVSProcessorType(providerSpec.ProcessorType),
		Processors:    providerSpec.Processors,
		MemoryGiB:     providerSpec.MemoryGiB,
		Network:       network,
		// ProviderID. This is populated when this is called in higher level funcs (ToMachine(), ToMachineSet()).
	}

	// CredentialsSecret - TODO(MULTIARCH-5195): Explore on what needs to happen regarding credentials secrets.

	if !reflect.DeepEqual(providerSpec.ObjectMeta, metav1.ObjectMeta{}) {
		// We don't support setting the object metadata in the provider spec.
		// It's only present for the purpose of the raw extension and doesn't have any functionality.
		errs = append(errs, field.Invalid(fldPath.Child("metadata"), providerSpec.ObjectMeta, "metadata is not supported"))
	}

	if len(providerSpec.LoadBalancers) > 0 {
		// TODO(MULTIARCH-5041): CAPIBM only applies load balancers to the control plane nodes. We should always reject LBs on non-control plane and work out how to connect the control plane LBs correctly otherwise.
		errs = append(errs, field.Invalid(fldPath.Child("loadBalancers"), providerSpec.LoadBalancers, "loadBalancers are not supported"))
	}

	return &ibmpowervsv1.IBMPowerVSMachine{
		TypeMeta: metav1.TypeMeta{
			APIVersion: ibmpowervsv1.GroupVersion.String(),
			Kind:       ibmPowerVSMachineKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.machine.Name,
			Namespace: capiNamespace,
		},
		Spec: spec,
	}, errs
}

func powerVSMachineToPowerVSMachineTemplate(powerVSMachine *ibmpowervsv1.IBMPowerVSMachine, name string, namespace string) (*ibmpowervsv1.IBMPowerVSMachineTemplate, error) {
	nameWithHash, err := util.GenerateInfraMachineTemplateNameWithSpecHash(name, powerVSMachine.Spec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate infrastructure machine template name with spec hash: %w", err)
	}

	return &ibmpowervsv1.IBMPowerVSMachineTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: ibmpowervsv1.GroupVersion.String(),
			Kind:       ibmPowerVSTemplateKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      nameWithHash,
			Namespace: namespace,
		},
		Spec: ibmpowervsv1.IBMPowerVSMachineTemplateSpec{
			Template: ibmpowervsv1.IBMPowerVSMachineTemplateResource{
				Spec: powerVSMachine.Spec,
			},
		},
	}, nil
}

func convertServiceInstanceToCAPI(fldPath *field.Path, serviceInstance mapiv1.PowerVSResource) (*ibmpowervsv1.IBMPowerVSResourceReference, *field.Error) {
	switch serviceInstance.Type {
	case mapiv1.PowerVSResourceTypeID:
		return &ibmpowervsv1.IBMPowerVSResourceReference{ID: serviceInstance.ID}, nil
	case mapiv1.PowerVSResourceTypeName:
		return &ibmpowervsv1.IBMPowerVSResourceReference{Name: serviceInstance.Name}, nil
	case mapiv1.PowerVSResourceTypeRegEx:
		return &ibmpowervsv1.IBMPowerVSResourceReference{RegEx: serviceInstance.RegEx}, nil
	default:
		return nil, field.Invalid(fldPath.Child("type"), serviceInstance.Type, "unknown type")
	}
}

func convertImageToCAPI(fldPath *field.Path, image mapiv1.PowerVSResource) (*ibmpowervsv1.IBMPowerVSResourceReference, *field.Error) {
	switch image.Type {
	case mapiv1.PowerVSResourceTypeID:
		return &ibmpowervsv1.IBMPowerVSResourceReference{ID: image.ID}, nil
	case mapiv1.PowerVSResourceTypeName:
		return &ibmpowervsv1.IBMPowerVSResourceReference{Name: image.Name}, nil
	case mapiv1.PowerVSResourceTypeRegEx:
		return &ibmpowervsv1.IBMPowerVSResourceReference{RegEx: image.RegEx}, nil
	default:
		return nil, field.Invalid(fldPath.Child("type"), image.Type, "unknown type")
	}
}

func convertNetworkToCAPI(fldPath *field.Path, network mapiv1.PowerVSResource) (ibmpowervsv1.IBMPowerVSResourceReference, *field.Error) {
	switch network.Type {
	case mapiv1.PowerVSResourceTypeID:
		return ibmpowervsv1.IBMPowerVSResourceReference{ID: network.ID}, nil
	case mapiv1.PowerVSResourceTypeName:
		return ibmpowervsv1.IBMPowerVSResourceReference{Name: network.Name}, nil
	case mapiv1.PowerVSResourceTypeRegEx:
		return ibmpowervsv1.IBMPowerVSResourceReference{RegEx: network.RegEx}, nil
	default:
		return ibmpowervsv1.IBMPowerVSResourceReference{}, field.Invalid(fldPath.Child("type"), network.Type, "unknown type")
	}
}
