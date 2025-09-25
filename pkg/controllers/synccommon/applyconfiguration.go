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

package synccommon

import (
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
)

// syncObjApplyConfiguration is an apply configuration for objects managed by
// the sync controller. This is currently MAPI Machine and MachineSet.
type syncObjApplyConfiguration[objPT any, statusPT syncStatusApplyConfiguration[statusPT]] interface {
	WithStatus(statusPT) objPT
	WithResourceVersion(string) objPT
}

// syncObjApplyConfigurationP asserts that a syncObjApplyConfiguration is a pointer to a specific concrete type.
type syncObjApplyConfigurationP[objT any, objPT *objT, statusPT syncStatusApplyConfiguration[statusPT]] interface {
	*objT
	syncObjApplyConfiguration[objPT, statusPT]
}

// syncStatusApplyConfiguration is an apply configuration for the status of
// objects managed by the sync controller. This is currently MAPI Machine and
// MachineSet.
type syncStatusApplyConfiguration[statusPT any] interface {
	WithConditions(...*machinev1applyconfigs.ConditionApplyConfiguration) statusPT
	WithSynchronizedGeneration(int64) statusPT
	WithAuthoritativeAPI(mapiv1beta1.MachineAuthority) statusPT
}

// syncStatusApplyConfigurationP asserts that a syncStatusApplyConfiguration is a pointer to a specific concrete type.
type syncStatusApplyConfigurationP[statusT any, statusPT any] interface {
	*statusT
	syncStatusApplyConfiguration[statusPT]
}

// syncObjApplyConfigurationConstructor is a constructor for
// SyncObjApplyConfigurations. It takes a name and namespace and returns an
// apply configuration. This constructor will have been generated automatically by
// the applyconfig generator.
type syncObjApplyConfigurationConstructor[objPT syncObjApplyConfiguration[objPT, statusPT], statusPT syncStatusApplyConfiguration[statusPT]] func(string, string) objPT
