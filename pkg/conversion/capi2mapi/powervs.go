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

package capi2mapi

import (
	"errors"
	"fmt"

	mapiv1 "github.com/openshift/api/machine/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	capibmv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var (
	errCAPIMachinePowerVSMachinePowerVSClusterCannotBeNil            = errors.New("provided Machine, IBMPowerVSMachine and IBMPowerVSCluster can not be nil")
	errCAPIMachineSetPowerVSMachineTemplatePowerVSClusterCannotBeNil = errors.New("provided MachineSet, IBMPowerVSMachineTemplate and IBMPowerVSCluster can not be nil")
)

// machineAndPowerVSMachineAndPowerVSCluster stores the details of a Cluster API Machine and PowerVSMachine and PowerVSCluster.
type machineAndPowerVSMachineAndPowerVSCluster struct {
	machine        *capiv1.Machine
	powerVSMachine *capibmv1.IBMPowerVSMachine
	powerVSCluster *capibmv1.IBMPowerVSCluster
}

// machineSetAndPowerVSMachineTemplateAndPowerVSCluster stores the details of a Cluster API MachineSet and PowerVSMachineTemplate and AWSCluster.
type machineSetAndPowerVSMachineTemplateAndPowerVSCluster struct {
	machineSet     *capiv1.MachineSet
	template       *capibmv1.IBMPowerVSMachineTemplate
	powerVSCluster *capibmv1.IBMPowerVSCluster
	*machineAndPowerVSMachineAndPowerVSCluster
}

// FromMachineAndPowerVSMachineAndPowerVSCluster wraps a CAPI Machine and CAPIBM PowerVSMachine and CAPIBM PowerVSCluster into a capi2mapi MachineAndInfrastructureMachine.
func FromMachineAndPowerVSMachineAndPowerVSCluster(m *capiv1.Machine, pm *capibmv1.IBMPowerVSMachine, pc *capibmv1.IBMPowerVSCluster) MachineAndInfrastructureMachine {
	return &machineAndPowerVSMachineAndPowerVSCluster{machine: m, powerVSMachine: pm, powerVSCluster: pc}
}

// FromMachineSetAndPowerVSMachineTemplateAndPowerVSCluster wraps a CAPI MachineSet and CAPIBM PowerVSMachineTemplate and CAPIBM PowerVSCluster into a capi2mapi MachineSetAndAWSMachineTemplateAndAWSCluster.
func FromMachineSetAndPowerVSMachineTemplateAndPowerVSCluster(ms *capiv1.MachineSet, mts *capibmv1.IBMPowerVSMachineTemplate, pc *capibmv1.IBMPowerVSCluster) MachineSetAndMachineTemplate {
	return machineSetAndPowerVSMachineTemplateAndPowerVSCluster{
		machineSet:     ms,
		template:       mts,
		powerVSCluster: pc,
		machineAndPowerVSMachineAndPowerVSCluster: &machineAndPowerVSMachineAndPowerVSCluster{
			machine: &capiv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      ms.Spec.Template.ObjectMeta.Labels,
					Annotations: ms.Spec.Template.ObjectMeta.Annotations,
				},
				Spec: ms.Spec.Template.Spec,
			},
			powerVSMachine: &capibmv1.IBMPowerVSMachine{
				Spec: mts.Spec.Template.Spec,
			},
			powerVSCluster: pc,
		},
	}
}

// ToMachine converts a capi2mapi MachineAndPowerVSMachineTemplate into a MAPI Machine.
func (m machineAndPowerVSMachineAndPowerVSCluster) ToMachine() (*mapiv1beta1.Machine, []string, error) {
	if m.machine == nil || m.powerVSMachine == nil || m.powerVSCluster == nil {
		return nil, nil, errCAPIMachinePowerVSMachinePowerVSClusterCannotBeNil
	}

	var (
		errors   field.ErrorList
		warnings []string
	)

	mapiPowerVSSpec, err := m.toProviderSpec()
	if err != nil {
		errors = append(errors, err...)
	}

	powerVSRawExt, errRaw := RawExtensionFromProviderSpec(mapiPowerVSSpec)
	if errRaw != nil {
		return nil, nil, fmt.Errorf("unable to convert PowerVS providerSpec to raw extension: %w", errRaw)
	}

	mapiMachine, err := fromCAPIMachineToMAPIMachine(m.machine)
	if err != nil {
		errors = append(errors, err...)
	}

	mapiMachine.Spec.ProviderSpec.Value = powerVSRawExt

	if len(errors) > 0 {
		return nil, warnings, errors.ToAggregate()
	}

	return mapiMachine, warnings, nil
}

// ToMachineSet converts a capi2mapi MachineAndPowerVSMachineTemplate into a MAPI MachineSet.
//
//nolint:dupl
func (m machineSetAndPowerVSMachineTemplateAndPowerVSCluster) ToMachineSet() (*mapiv1beta1.MachineSet, []string, error) {
	if m.machineSet == nil || m.template == nil || m.powerVSCluster == nil || m.machineAndPowerVSMachineAndPowerVSCluster == nil {
		return nil, nil, errCAPIMachineSetPowerVSMachineTemplatePowerVSClusterCannotBeNil
	}

	var (
		errs     []error
		warnings []string
	)

	// Run the full ToMachine conversion so that we can check for
	// any Machine level conversion errors in the spec translation.
	mapiPowerVSMachine, warn, err := m.ToMachine()
	if err != nil {
		errs = append(errs, err)
	}

	warnings = append(warnings, warn...)

	mapiMachineSet, err := fromCAPIMachineSetToMAPIMachineSet(m.machineSet)
	if err != nil {
		errs = append(errs, err)
	}

	mapiMachineSet.Spec.Template.Spec = mapiPowerVSMachine.Spec

	// Copy the labels and annotations from the Machine to the template.
	mapiMachineSet.Spec.Template.ObjectMeta.Annotations = mapiPowerVSMachine.ObjectMeta.Annotations
	mapiMachineSet.Spec.Template.ObjectMeta.Labels = mapiPowerVSMachine.ObjectMeta.Labels

	if len(errs) > 0 {
		return nil, warnings, utilerrors.NewAggregate(errs)
	}

	return mapiMachineSet, warnings, nil
}

// toProviderSpec converts a capi2mapi machineAndPowerVSMachineAndPowerVSCluster into a MAPI PowerVSMachineProviderConfig.
func (m machineAndPowerVSMachineAndPowerVSCluster) toProviderSpec() (*mapiv1.PowerVSMachineProviderConfig, field.ErrorList) {
	errs := field.ErrorList{}

	fldPath := field.NewPath("spec")

	serviceInstance, err := convertPowerVSServiceInstanceToMAPI(fldPath.Child("serviceInstance"), m.powerVSMachine.Spec.ServiceInstanceID, m.powerVSMachine.Spec.ServiceInstance)
	if err != nil {
		errs = append(errs, err)
	}

	image, err := convertPowerVSImageToMAPI(fldPath.Child("image"), m.powerVSMachine.Spec.Image, m.powerVSMachine.Spec.ImageRef)
	if err != nil {
		errs = append(errs, err)
	}

	network, err := convertPowerVSNetworkToMAPI(fldPath.Child("network"), m.powerVSMachine.Spec.Network)
	if err != nil {
		errs = append(errs, err)
	}

	mapiProviderConfig := mapiv1.PowerVSMachineProviderConfig{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PowerVSMachineProviderConfig",
			APIVersion: "machine.openshift.io/v1",
		},
		ServiceInstance: serviceInstance,
		Image:           image,
		Network:         network,
		KeyPairName:     m.powerVSMachine.Spec.SSHKey,
		SystemType:      m.powerVSMachine.Spec.SystemType,
		ProcessorType:   mapiv1.PowerVSProcessorType(m.powerVSMachine.Spec.ProcessorType),
		Processors:      m.powerVSMachine.Spec.Processors,
		MemoryGiB:       m.powerVSMachine.Spec.MemoryGiB,
		//CredentialsSecret: TODO(MULTIARCH-5195): Explore on what needs to happen regarding credentials secrets.
		//LoadBalancers: TODO(MULTIARCH-5041): Not supported for workers.
	}

	userDataSecretName := ptr.Deref(m.machine.Spec.Bootstrap.DataSecretName, "")
	if userDataSecretName != "" {
		mapiProviderConfig.UserDataSecret = &mapiv1.PowerVSSecretReference{
			Name: userDataSecretName,
		}
	}

	if len(errs) > 0 {
		return nil, errs
	}

	return &mapiProviderConfig, nil
}

// Conversion helpers.

func convertPowerVSNetworkToMAPI(fldPath *field.Path, network capibmv1.IBMPowerVSResourceReference) (mapiv1.PowerVSResource, *field.Error) {
	var networkResource mapiv1.PowerVSResource

	// In mapi provider the network resource is checked in the order of ID, Name followed by RegEx.
	switch {
	case network.ID != nil:
		networkResource.Type = mapiv1.PowerVSResourceTypeID
		networkResource.ID = network.ID

		return networkResource, nil
	case network.Name != nil:
		networkResource.Type = mapiv1.PowerVSResourceTypeName
		networkResource.Name = network.Name

		return networkResource, nil
	case network.RegEx != nil:
		networkResource.Type = mapiv1.PowerVSResourceTypeRegEx
		networkResource.RegEx = network.RegEx

		return networkResource, nil
	}

	return networkResource, field.Invalid(fldPath, network, "unable to convert network to Machine API")
}

func convertPowerVSImageToMAPI(fldPath *field.Path, image *capibmv1.IBMPowerVSResourceReference, imageRef *corev1.LocalObjectReference) (mapiv1.PowerVSResource, *field.Error) {
	if image == nil && imageRef == nil {
		return mapiv1.PowerVSResource{}, field.Invalid(fldPath, image, "unable to convert image, image and imageref is nil")
	}

	var imageResource mapiv1.PowerVSResource

	if image == nil {
		imageResource.Type = mapiv1.PowerVSResourceTypeName
		imageResource.Name = &imageRef.Name

		return imageResource, nil
	}

	// In mapi provider the image resource is checked in the order of ID, Name followed by RegEx.
	switch {
	case image.ID != nil:
		imageResource.Type = mapiv1.PowerVSResourceTypeID
		imageResource.ID = image.ID

		return imageResource, nil
	case image.Name != nil:
		imageResource.Type = mapiv1.PowerVSResourceTypeName
		imageResource.Name = image.Name

		return imageResource, nil
	case image.RegEx != nil:
		imageResource.Type = mapiv1.PowerVSResourceTypeRegEx
		imageResource.RegEx = image.RegEx

		return imageResource, nil
	}

	return mapiv1.PowerVSResource{}, field.Invalid(fldPath, image, "unable to convert image, image id, name and regex all are nil")
}

func convertPowerVSServiceInstanceToMAPI(fldPath *field.Path, serviceInstanceID string, serviceInstance *capibmv1.IBMPowerVSResourceReference) (mapiv1.PowerVSResource, *field.Error) {
	var serviceInstanceResource mapiv1.PowerVSResource
	if serviceInstanceID != "" {
		serviceInstanceResource.Type = mapiv1.PowerVSResourceTypeID
		serviceInstanceResource.ID = &serviceInstanceID

		return serviceInstanceResource, nil
	}

	if serviceInstance == nil {
		return serviceInstanceResource, field.Invalid(fldPath, serviceInstance, "unable to convert service instance, service instance is nil")
	}

	// In mapi provider the service instance resource is checked in the order of ID, Name followed by RegEx.
	switch {
	case serviceInstance.ID != nil:
		serviceInstanceResource.Type = mapiv1.PowerVSResourceTypeID
		serviceInstanceResource.ID = serviceInstance.ID

		return serviceInstanceResource, nil
	case serviceInstance.Name != nil:
		serviceInstanceResource.Type = mapiv1.PowerVSResourceTypeName
		serviceInstanceResource.Name = serviceInstance.Name

		return serviceInstanceResource, nil
	case serviceInstance.RegEx != nil:
		serviceInstanceResource.Type = mapiv1.PowerVSResourceTypeRegEx
		serviceInstanceResource.RegEx = serviceInstance.RegEx

		return serviceInstanceResource, nil
	}

	return serviceInstanceResource, field.Invalid(fldPath, serviceInstance, "unable to convert service instance to Machine API")
}
