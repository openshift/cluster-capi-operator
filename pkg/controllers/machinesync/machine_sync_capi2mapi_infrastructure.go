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

package machinesync

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

// setChangedMAPIMachineProviderStatusFields unmarshals the existing and converted ProviderStatus, copies over the fields and marshals it back to the existingMAPIMachine.
func setChangedMAPIMachineProviderStatusFields(platform configv1.PlatformType, existingMAPIMachine, convertedMAPIMachine *mapiv1beta1.Machine) error {
	var newProviderStatus interface{}

	switch platform {
	case configv1.AWSPlatformType:
		existingStatus, err := mapi2capi.AWSProviderStatusFromRawExtension(existingMAPIMachine.Status.ProviderStatus)
		if err != nil {
			return fmt.Errorf("unable to convert RawExtension to AWS ProviderStatus: %w", err)
		}

		convertedStatus, err := mapi2capi.AWSProviderStatusFromRawExtension(convertedMAPIMachine.Status.ProviderStatus)
		if err != nil {
			return fmt.Errorf("unable to convert RawExtension to AWS ProviderStatus: %w", err)
		}

		for i := range convertedStatus.Conditions {
			existingStatus.Conditions = util.SetMAPIProviderCondition(existingStatus.Conditions, &convertedStatus.Conditions[i])
		}

		convertedStatus.Conditions = existingStatus.Conditions

		newProviderStatus = convertedStatus
	case configv1.OpenStackPlatformType:
		// TODO(openstack): implement
		return nil
	case configv1.PowerVSPlatformType:
		// TODO(powervs): implement
		return nil
	}

	rawExtension, err := capi2mapi.RawExtensionFromInterface(newProviderStatus)
	if err != nil {
		return fmt.Errorf("unable to convert ProviderStatus to RawExtension: %w", err)
	}

	existingMAPIMachine.Status.ProviderStatus = rawExtension

	return nil
}
