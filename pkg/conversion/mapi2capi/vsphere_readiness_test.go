/*
Copyright 2026 Red Hat, Inc.

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
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
)

var _ = Describe("vSphere provisioning readiness", func() {
	DescribeTable("should derive readiness independently of power and early creation success", func(phase *string, powerState string, expected bool) {
		providerStatus := mapiv1beta1.VSphereMachineProviderStatus{InstanceState: ptr.To(powerState), Conditions: []metav1.Condition{{Type: string(mapiv1beta1.MachineCreation), Status: metav1.ConditionTrue, Reason: mapiv1beta1.MachineCreationSucceededConditionReason}}}
		raw, err := json.Marshal(providerStatus)
		Expect(err).NotTo(HaveOccurred())

		machine := &mapiv1beta1.Machine{Status: mapiv1beta1.MachineStatus{Phase: phase, ProviderStatus: &runtime.RawExtension{Raw: raw}}}
		status, errs := convertMAPIMachineStatusToVSphereMachineStatus(machine)
		Expect(errs).To(BeEmpty())
		Expect(status.Ready).To(Equal(expected))
	},
		Entry("running powered-on Machine", ptr.To(mapiv1beta1.PhaseRunning), "poweredOn", true),
		Entry("provisioned powered-off Machine", ptr.To(mapiv1beta1.PhaseProvisioned), "poweredOff", true),
		Entry("deleting provisioned phase follows the core CAPI rule", ptr.To(mapiv1beta1.PhaseDeleting), "poweredOff", true),
		Entry("asynchronous provisioning is not complete despite creation success", ptr.To(mapiv1beta1.PhaseProvisioning), "poweredOn", false),
		Entry("phase overrides stale synthetic ready", ptr.To(mapiv1beta1.PhaseProvisioning), "ready", false),
		Entry("failed Machine", ptr.To(mapiv1beta1.PhaseFailed), "poweredOn", false),
		Entry("missing evidence", (*string)(nil), "poweredOn", false),
		Entry("legacy synthetic bridge without phase", (*string)(nil), "ready", true),
	)
})
