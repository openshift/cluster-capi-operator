package mapi2capi

import (
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	capgv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

type GCPProviderSpec struct {
	Spec *mapiv1.GCPMachineProviderSpec
}

func FromGCPProviderSpec(s *mapiv1.GCPMachineProviderSpec) GCPProviderSpec {
	return GCPProviderSpec{Spec: s}
}

func (p GCPProviderSpec) ToMachineTemplateSpec() (*capgv1.GCPMachineTemplateSpec, []string, error) {
	var errs []error
	var warnings []string

	spec := capgv1.GCPMachineTemplateSpec{
		Template: capgv1.GCPMachineTemplateResource{
			Spec: capgv1.GCPMachineSpec{
				InstanceType: p.Spec.MachineType,
			},
		},
	}

	if len(errs) > 0 {
		return nil, warnings, utilerrors.NewAggregate(errs)
	}

	return &spec, warnings, nil
}
