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
package controllers

import (
	"errors"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	capav1beta2 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capibmv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// errPlatformNotSupported is returned when the platform is not supported.
	errPlatformNotSupported = errors.New("error determining InfraMachine type, platform not supported")
)

// InitInfraMachineAndInfraClusterFromProvider returns the correct InfraMachine and InfraCluster implementation
// for a given provider.
//
// As we implement other cloud providers, we'll need to update this list.
func InitInfraMachineAndInfraClusterFromProvider(platform configv1.PlatformType) (client.Object, client.Object, error) {
	switch platform {
	case configv1.AWSPlatformType:
		return &capav1beta2.AWSMachine{}, &capav1beta2.AWSCluster{}, nil
	case configv1.PowerVSPlatformType:
		return &capibmv1.IBMPowerVSMachine{}, &capibmv1.IBMPowerVSCluster{}, nil
	default:
		return nil, nil, fmt.Errorf("%w: %s", errPlatformNotSupported, platform)
	}
}

// InitInfraMachineTemplateAndInfraClusterFromProvider returns the correct InfraMachineTemplate and InfraCluster implementation
// for a given provider.
//
// As we implement other cloud providers, we'll need to update this list.
func InitInfraMachineTemplateAndInfraClusterFromProvider(platform configv1.PlatformType) (client.Object, client.Object, error) {
	switch platform {
	case configv1.AWSPlatformType:
		return &capav1beta2.AWSMachineTemplate{}, &capav1beta2.AWSCluster{}, nil
	case configv1.PowerVSPlatformType:
		return &capibmv1.IBMPowerVSMachineTemplate{}, &capibmv1.IBMPowerVSCluster{}, nil
	default:
		return nil, nil, fmt.Errorf("%w: %s", errPlatformNotSupported, platform)
	}
}
