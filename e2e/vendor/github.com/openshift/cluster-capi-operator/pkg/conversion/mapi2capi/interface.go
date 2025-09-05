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
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Machine represents a type holding MAPI Machine.
type Machine interface {
	ToMachineAndInfrastructureMachine() (*clusterv1.Machine, client.Object, []string, error)
}

// MachineSet represents a type holding MAPI MachineSet.
type MachineSet interface {
	ToMachineSetAndMachineTemplate() (*clusterv1.MachineSet, client.Object, []string, error)
}
