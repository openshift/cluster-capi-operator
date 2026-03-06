// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package framework

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// GetMAPIProviderSpec lists MAPI MachineSets, sorts by name, and unmarshals
// the first MachineSet's ProviderSpec into the given type T.
func GetMAPIProviderSpec[T any](ctx context.Context, cl client.Client) *T {
	GinkgoHelper()

	machineSetList := &mapiv1beta1.MachineSetList{}
	Expect(cl.List(ctx, machineSetList, client.InNamespace(MAPINamespace))).To(Succeed(),
		"should not fail listing MAPI MachineSets")
	Expect(machineSetList.Items).ToNot(BeEmpty(), "expected to have at least a MachineSet")

	SortListByName(machineSetList)
	machineSet := machineSetList.Items[0]
	Expect(machineSet.Spec.Template.Spec.ProviderSpec.Value).ToNot(BeNil(),
		"expected MAPI MachineSet ProviderSpec to not be nil")

	providerSpec := new(T)
	Expect(yaml.Unmarshal(machineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, providerSpec)).To(Succeed(),
		"should not fail YAML decoding MAPI MachineSet provider spec")

	return providerSpec
}
