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

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"

	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
)

// newOpenStackMachineTemplate creates an OpenStackMachineTemplate from a MAPI MachineSet
// using the production mapi2capi conversion code.
func newOpenStackMachineTemplate(mapiMachineSet *mapiv1beta1.MachineSet, infraObj *configv1.Infrastructure) *openstackv1.OpenStackMachineTemplate {
	GinkgoHelper()
	By("Creating OpenStack machine template")

	_, templateObj, _, err := mapi2capi.FromOpenStackMachineSetAndInfra(mapiMachineSet, infraObj).ToMachineSetAndMachineTemplate()
	Expect(err).ToNot(HaveOccurred(), "should not fail converting MAPI MachineSet to CAPI OpenStackMachineTemplate")

	mt, ok := templateObj.(*openstackv1.OpenStackMachineTemplate)
	Expect(ok).To(BeTrue(), "expected template to be *openstackv1.OpenStackMachineTemplate, got %T", templateObj)

	mt.Namespace = capiframework.CAPINamespace

	return mt
}

// openStackMachineTemplateKind is the infrastructureRef kind the OpenStack mirrors use.
const openStackMachineTemplateKind = "OpenStackMachineTemplate"

// createOpenStackMachineTemplateFromMachineSet creates OpenStackMachineTemplate from an existing MAPI MachineSet.
func createOpenStackMachineTemplateFromMachineSet(mapiMS *mapiv1beta1.MachineSet, machineSetName string) client.Object {
	GinkgoHelper()

	mt := newOpenStackMachineTemplate(mapiMS, infra)
	mt.Name = machineSetName

	Eventually(func() error {
		return cl.Create(ctx, mt)
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Failed to create a new OpenStackMachineTemplate %s", mt.Name)

	return mt
}
