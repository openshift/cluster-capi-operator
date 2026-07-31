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
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// GetFirstMAPIMachineSet returns a MAPI worker MachineSet suitable for use as a read-only
// template, e.g. to copy a ProviderSpec from. The returned MachineSet is not guaranteed to
// exist as a live object.
//
// It prefers a real Machine API worker MachineSet. On clusters where Cluster API provisions
// workers and no MAPI worker MachineSet exists (e.g. CAPI-only clusters), it falls back to
// synthesizing one in-memory from a real Cluster API worker MachineSet, via
// mapiframework.GetSampleMAPIWorkerMachineSet.
func GetFirstMAPIMachineSet(ctx context.Context, cl client.Client) *mapiv1beta1.MachineSet {
	GinkgoHelper()

	machineSet, err := mapiframework.GetSampleMAPIWorkerMachineSet(ctx, cl)
	Expect(err).ToNot(HaveOccurred(), "getting a sample worker MachineSet should not error")
	Expect(machineSet).ToNot(BeNil(), "expected to find a Machine API or Cluster API worker MachineSet")

	return machineSet
}

// GetMAPIProviderSpec returns a sample worker MachineSet, obtained via GetFirstMAPIMachineSet,
// and unmarshals its ProviderSpec into the given type T.
func GetMAPIProviderSpec[T any](ctx context.Context, cl client.Client) *T {
	GinkgoHelper()

	machineSet := GetFirstMAPIMachineSet(ctx, cl)
	Expect(machineSet.Spec.Template.Spec.ProviderSpec.Value).ToNot(BeNil(),
		"expected MAPI MachineSet ProviderSpec to not be nil")

	providerSpec := new(T)
	Expect(yaml.Unmarshal(machineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, providerSpec)).To(Succeed(),
		"should not fail YAML decoding MAPI MachineSet provider spec")

	return providerSpec
}
