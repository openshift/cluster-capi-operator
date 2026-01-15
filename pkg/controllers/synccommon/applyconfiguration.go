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

// ObjApplyConfiguration is an apply configuration for objects managed by the
// sync and migration controllers. This is currently MAPI Machine and
// MachineSet.
type ObjApplyConfiguration[objPT any, statusPT StatusApplyConfiguration[statusPT]] interface {
	WithStatus(statusPT) objPT
	WithResourceVersion(string) objPT
}

// ObjApplyConfigurationP asserts that an ObjApplyConfiguration is a pointer to
// a specific concrete type.
type ObjApplyConfigurationP[objT any, objPT *objT, statusPT StatusApplyConfiguration[statusPT]] interface {
	*objT
	ObjApplyConfiguration[objPT, statusPT]
}

// StatusApplyConfiguration is an apply configuration for the status of objects
// managed by the sync and migration controllers. This is currently MAPI
// Machine and MachineSet.
type StatusApplyConfiguration[statusPT any] interface {
	WithConditions(...*machinev1applyconfigs.ConditionApplyConfiguration) statusPT
	WithSynchronizedGeneration(int64) statusPT
	WithAuthoritativeAPI(mapiv1beta1.MachineAuthority) statusPT
	WithSynchronizedAPI(mapiv1beta1.SynchronizedAPI) statusPT
}

// StatusApplyConfigurationP asserts that a StatusApplyConfiguration is a
// pointer to a specific concrete type.
type StatusApplyConfigurationP[statusT any, statusPT any] interface {
	*statusT
	StatusApplyConfiguration[statusPT]
}

// ObjApplyConfigurationConstructor is a constructor for
// ObjApplyConfigurations. It takes a name and namespace and returns an apply
// configuration. This constructor will have been generated automatically by
// the applyconfig generator.
type ObjApplyConfigurationConstructor[objPT ObjApplyConfiguration[objPT, statusPT], statusPT StatusApplyConfiguration[statusPT]] func(string, string) objPT
