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

package machinesync

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("Machine API provider status synchronization", func() {
	DescribeTable("should preserve vSphere provider status through the status update",
		func(existingRaw string, convertedRaw string, expectedInstanceID string, expectedTaskRef string, preserveCondition bool) {
			machine := &mapiv1beta1.Machine{
				ObjectMeta: metav1.ObjectMeta{Name: "test-machine", Namespace: "openshift-machine-api"},
			}
			if existingRaw != "" {
				machine.Status.ProviderStatus = &runtime.RawExtension{Raw: []byte(existingRaw)}
			}

			converted := machine.DeepCopy()
			converted.Status.ProviderStatus = &runtime.RawExtension{Raw: []byte(convertedRaw)}

			fakeClient := fake.NewClientBuilder().
				WithScheme(testEnv.Scheme).
				WithStatusSubresource(machine).
				WithObjects(machine).
				Build()
			reconciler := &MachineSyncReconciler{Client: fakeClient, Platform: configv1.VSpherePlatformType}

			By("Updating status through the controller's provider-status merge path")

			diff, err := util.NewDefaultDiffer().Diff(machine, converted)
			Expect(err).NotTo(HaveOccurred())
			updated, err := reconciler.ensureMAPIMachineStatusUpdated(ctx, machine, converted, diff, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated).To(BeTrue())

			stored := &mapiv1beta1.Machine{}
			Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(machine), stored)).To(Succeed())
			status, err := mapi2capi.VSphereProviderStatusFromRawExtension(stored.Status.ProviderStatus)
			Expect(err).NotTo(HaveOccurred())
			Expect(ptr.Deref(status.InstanceID, "")).To(Equal(expectedInstanceID))
			Expect(status.TaskRef).To(Equal(expectedTaskRef))
			Expect(status.Conditions).To(ContainElement(SatisfyAll(
				HaveField("Type", Equal("MachineCreation")),
				HaveField("Status", Equal(metav1.ConditionTrue)),
			)))

			if preserveCondition {
				Expect(status.Conditions).To(ContainElement(SatisfyAll(
					HaveField("Type", Equal("ActuatorCondition")),
					HaveField("Status", Equal(metav1.ConditionTrue)),
				)))
			}
		},
		Entry("when actuator-populated fields and conditions already exist",
			`{"instanceId":"vm-uuid","taskRef":"task-123","conditions":[{"type":"ActuatorCondition","status":"True","reason":"Observed"},{"type":"MachineCreation","status":"False","reason":"Pending"}]}`,
			`{"instanceState":"ready","conditions":[{"type":"MachineCreation","status":"True","reason":"MachineCreationSucceeded"}]}`,
			"vm-uuid", "task-123", true),
		Entry("when the existing provider status is nil",
			"",
			`{"instanceState":"ready","conditions":[{"type":"MachineCreation","status":"True","reason":"MachineCreationSucceeded"}]}`,
			"", "", false),
		Entry("when converted fields supersede actuator-populated fields",
			`{"instanceId":"old-uuid","taskRef":"old-task"}`,
			`{"instanceId":"new-uuid","taskRef":"new-task","conditions":[{"type":"MachineCreation","status":"True","reason":"MachineCreationSucceeded"}]}`,
			"new-uuid", "new-task", false),
	)
})
