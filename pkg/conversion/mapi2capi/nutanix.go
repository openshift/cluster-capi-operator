package mapi2capi

import (
	"fmt"

	nutanixv1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	nutanixMachineKind         = "NutanixMachine"
	nutanixMachineTemplateKind = "NutanixMachineTemplate"
)

type nutanixMachineAndInfra struct {
	machine        *mapiv1beta1.Machine
	infrastructure *configv1.Infrastructure
}

type nutanixMachineSetAndInfra struct {
	machineSet     *mapiv1beta1.MachineSet
	infrastructure *configv1.Infrastructure
	*nutanixMachineAndInfra
}

// FromNutanixMachineAndInfra wraps a Machine API Machine for Nutanix and the OCP Infrastructure object into a mapi2capi NutanixProviderSpec.
func FromNutanixMachineAndInfra(m *mapiv1beta1.Machine, i *configv1.Infrastructure) Machine {
	return &nutanixMachineAndInfra{machine: m, infrastructure: i}
}

// FromNutanixMachineSetAndInfra wraps a Machine API MachineSet for OpenStack and the OCP Infrastructure object into a mapi2capi OpenstackProviderSpec.
func FromNutanixMachineSetAndInfra(m *mapiv1beta1.MachineSet, i *configv1.Infrastructure) MachineSet {
	return &nutanixMachineSetAndInfra{
		machineSet:     m,
		infrastructure: i,
		nutanixMachineAndInfra: &nutanixMachineAndInfra{
			machine: &mapiv1beta1.Machine{
				Spec: m.Spec.Template.Spec,
			},
			infrastructure: i,
		},
	}
}

func nutanixMachineToNutanixMachineTemplate(nutanixMachine *nutanixv1.NutanixMachine, name string, namespace string) *nutanixv1.NutanixMachineTemplate {
	return &nutanixv1.NutanixMachineTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: nutanixv1.GroupVersion.Version,
			Kind:       nutanixMachineKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: nutanixv1.NutanixMachineTemplateSpec{
			Template: nutanixv1.NutanixMachineTemplateResource{
				Spec: nutanixMachine.Spec,
			},
		},
	}
}

func (m *nutanixMachineSetAndInfra) ToMachineSetAndMachineTemplate() (*clusterv1.MachineSet, client.Object, []string, error) {
	var (
		errors   []error
		warnings []string
	)
	capiMachine, capxMachineObj, warns, err := m.toMachineAndInfrastructureMachine()
	if err != nil {
		errors = append(errors, err.ToAggregate().Errors()...)
	}
	warnings = append(warnings, warns...)
	capxMachine, ok := capxMachineObj.(*nutanixv1.NutanixMachine)
	if !ok {
		panic(fmt.Errorf("%w: %T", errUnexpectedObjectTypeForMachine, capxMachineObj))
	}
	capxMachineTemplate := nutanixMachineToNutanixMachineTemplate(capxMachine, m.machineSet.Name, capiNamespace)
	capiMachineSet, machineSetErrs := fromMAPIMachineSetToCAPIMachineSet(m.machineSet)
	if machineSetErrs != nil {
		errors = append(errors, machineSetErrs.Errors()...)
	}
	capiMachineSet.Spec.Template.Spec = capiMachine.Spec
	capiMachineSet.Spec.Template.Spec.InfrastructureRef.Kind = nutanixMachineTemplateKind
	capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name = capxMachineTemplate.Name

	if m.infrastructure == nil || m.infrastructure.Status.InfrastructureName == "" {
		errors = append(errors, field.Invalid(field.NewPath("infrastructure", "status", "infrastructureName"), m.infrastructure.Status.InfrastructureName, "infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachineSet.Spec.Template.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		capiMachineSet.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		capiMachineSet.Labels[clusterv1.ClusterNameLabel] = m.infrastructure.Status.InfrastructureName
	}

	if len(errors) > 0 {
		return nil, nil, warnings, utilerrors.NewAggregate(errors)
	}
	return capiMachineSet, capxMachineTemplate, warnings, nil
}

// ToMachineAndInfrastructureMachine is used to generate a CAPI Machine and the corresponding InfrastructureMachine
// from the stored MAPI Machine and Infrastructure objects.
func (m *nutanixMachineAndInfra) ToMachineAndInfrastructureMachine() (*clusterv1.Machine, client.Object, []string, error) {
	capiMachine, capoMachine, warnings, errors := m.toMachineAndInfrastructureMachine()

	if len(errors) > 0 {
		return nil, nil, warnings, errors.ToAggregate()
	}

	return capiMachine, capoMachine, warnings, nil
}

func (m *nutanixMachineAndInfra) nutanixProviderSpecFromRawExtension(rawExtension *runtime.RawExtension) (*mapiv1.NutanixMachineProviderConfig, error) {
	if rawExtension == nil {
		return &mapiv1.NutanixMachineProviderConfig{}, nil
	}

	spec := &mapiv1.NutanixMachineProviderConfig{}
	if err := yaml.Unmarshal(rawExtension.Raw, &spec); err != nil {
		return &mapiv1.NutanixMachineProviderConfig{}, fmt.Errorf("error unmarshalling providerSpec: %w", err)
	}

	return spec, nil
}

func (m *nutanixMachineAndInfra) toNutanixMachine(providerConfig *mapiv1.NutanixMachineProviderConfig) (*nutanixv1.NutanixMachine, []string, field.ErrorList) {
	var (
		errors   field.ErrorList
		warnings []string
	)
	// Convert Subnets from []mapiv1.NutanixResourceIdentifier to []nutanixv1.NutanixResourceIdentifier
	var subnets []nutanixv1.NutanixResourceIdentifier
	for _, s := range providerConfig.Subnets {
		subnets = append(subnets, nutanixv1.NutanixResourceIdentifier{
			Type: nutanixv1.NutanixIdentifierType(s.Type),
			Name: s.Name,
			UUID: s.UUID,
		})
	}

	// Convert DataDisks from []mapiv1.NutanixVMDisk to []nutanixv1.NutanixMachineVMDisk
	var dataDisks []nutanixv1.NutanixMachineVMDisk
	for _, d := range providerConfig.DataDisks {
		dataDisks = append(dataDisks, nutanixv1.NutanixMachineVMDisk{
			DiskSize: d.DiskSize,
			DataSource: &nutanixv1.NutanixResourceIdentifier{
				Type: nutanixv1.NutanixIdentifierType(d.DataSource.Type),
				Name: d.DataSource.Name,
				UUID: d.DataSource.UUID,
			},
		})
	}

	// Convert GPUs from []mapiv1.NutanixGPU to []nutanixv1.NutanixGPU
	var gpus []nutanixv1.NutanixGPU
	for _, g := range providerConfig.GPUs {
		var deviceID *int64
		if g.DeviceID != nil {
			val := int64(*g.DeviceID)
			deviceID = &val
		}
		gpus = append(gpus, nutanixv1.NutanixGPU{
			DeviceID: deviceID,
			Type:     nutanixv1.NutanixGPUIdentifierType(g.Type),
			Name:     g.Name,
		})
	}

	spec := &nutanixv1.NutanixMachineSpec{
		VCPUsPerSocket: providerConfig.VCPUsPerSocket,
		VCPUSockets:    providerConfig.VCPUSockets,
		MemorySize:     providerConfig.MemorySize,
		Image: &nutanixv1.NutanixResourceIdentifier{
			Type: nutanixv1.NutanixIdentifierType(providerConfig.Image.Type),
			Name: providerConfig.Image.Name,
			UUID: providerConfig.Image.UUID,
		},
		Cluster: nutanixv1.NutanixResourceIdentifier{
			Type: nutanixv1.NutanixIdentifierType(providerConfig.Cluster.Type),
			Name: providerConfig.Cluster.Name,
			UUID: providerConfig.Cluster.UUID,
		},
		BootType:       nutanixv1.NutanixBootType(providerConfig.BootType),
		SystemDiskSize: providerConfig.SystemDiskSize,
		DataDisks:      dataDisks,
		GPUs:           gpus,
		Subnets:        subnets,
	}
	return &nutanixv1.NutanixMachine{
		TypeMeta: metav1.TypeMeta{
			APIVersion: nutanixv1.SchemeBuilder.GroupVersion.Version,
			Kind:       nutanixMachineKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.machine.Name,
			Namespace: capiNamespace,
		},
		Spec: *spec,
	}, warnings, errors

}

func (m *nutanixMachineAndInfra) toMachineAndInfrastructureMachine() (*clusterv1.Machine, client.Object, []string, field.ErrorList) {
	var (
		errors   field.ErrorList
		warnings []string
	)

	nutanixProviderConfig, err := m.nutanixProviderSpecFromRawExtension(m.machine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, nil, nil, field.ErrorList{field.Invalid(field.NewPath("spec", "providerSpec", "value"), m.machine.Spec.ProviderSpec.Value, err.Error())}
	}
	capxMachine, warns, errs := m.toNutanixMachine(nutanixProviderConfig)
	if errs != nil {
		errors = append(errors, errs...)
	}

	warnings = append(warnings, warns...)

	capiMachine, errs := fromMAPIMachineToCAPIMachine(m.machine, nutanixv1.SchemeBuilder.GroupVersion.Version, nutanixMachineTemplateKind)
	if errs != nil {
		errors = append(errors, errs...)
	}

	if m.infrastructure == nil || m.infrastructure.Status.InfrastructureName == "" {
		errors = append(errors, field.Invalid(field.NewPath("infrastructure", "status", "infrastructureName"), m.infrastructure.Status.InfrastructureName, "infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachine.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		capiMachine.Labels[clusterv1.ClusterNameLabel] = m.infrastructure.Status.InfrastructureName
	}

	capiMachineAnnotations := capiMachine.GetAnnotations()
	if len(capiMachineAnnotations) > 0 {
		capxMachine.SetAnnotations(capiMachineAnnotations)
	}

	capiMachineLabels := capiMachine.GetLabels()
	if len(capiMachineLabels) > 0 {
		capxMachine.SetLabels(capiMachineLabels)
	}

	return capiMachine, capxMachine, warnings, errors
}
