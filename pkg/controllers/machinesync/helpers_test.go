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
	. "github.com/onsi/gomega"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

// transitionToAuthoritativeAPI transitions the AuthoritativeAPI of a machine to the target API.
// It sets spec.AuthoritativeAPI to the target API and then transitions status.AuthoritativeAPI through the Migrating state to the target API.
// This simulates the migration controller behavior for unit tests.
func transitionToAuthoritativeAPI(k komega.Komega, machine *mapiv1beta1.Machine, targetAPI mapiv1beta1.MachineAuthority, timeout interface{}) {
	if machine.Spec.AuthoritativeAPI == targetAPI && machine.Status.AuthoritativeAPI == targetAPI {
		return
	}

	if machine.Spec.AuthoritativeAPI != targetAPI {
		Eventually(k.Update(machine, func() {
			machine.Spec.AuthoritativeAPI = targetAPI
		}), timeout).Should(Succeed(), "Failed to update Machine spec.AuthoritativeAPI to %s", targetAPI)
	}

	// AuthoritativeAPI must transition through Migrating state
	if machine.Status.AuthoritativeAPI != mapiv1beta1.MachineAuthorityMigrating {
		Eventually(k.UpdateStatus(machine, func() {
			machine.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMigrating
		}), timeout).Should(Succeed(), "Failed to transition Machine status.AuthoritativeAPI to Migrating")
	}

	Eventually(k.UpdateStatus(machine, func() {
		machine.Status.AuthoritativeAPI = targetAPI
	}), timeout).Should(Succeed(), "Failed to transition Machine status.AuthoritativeAPI to %s", targetAPI)
}
