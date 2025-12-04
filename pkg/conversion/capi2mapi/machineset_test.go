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
package capi2mapi

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	capibuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta1"
	capabuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	capierrors "sigs.k8s.io/cluster-api/errors"
)

var _ = Describe("capi2mapi MachineSet conversion", func() {
	var (
		capiMachineSetBase = capibuilder.MachineSet()
	)

	type capi2MAPIMachinesetConversionInput struct {
		machineSetBuilder capibuilder.MachineSetBuilder
		expectedErrors    []string
		expectedWarnings  []string
	}

	var _ = DescribeTable("capi2mapi convert CAPI MachineSet/InfraMachineTemplate/InfraCluster to MAPI MachineSet",
		func(in capi2MAPIMachinesetConversionInput) {
			_, warns, err := FromMachineSetAndAWSMachineTemplateAndAWSCluster(
				in.machineSetBuilder.Build(),
				capabuilder.AWSMachineTemplate().Build(),
				capabuilder.AWSCluster().Build(),
			).ToMachineSet()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors),
				"should match expected errors while converting CAPI resources to MAPI MachineSet")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings),
				"should match expected warnings while converting CAPI resources to MAPI MachineSet")
		},

		// Base Case.
		Entry("With a Base configuration", capi2MAPIMachinesetConversionInput{
			machineSetBuilder: capiMachineSetBase,
			expectedErrors:    []string{},
			expectedWarnings:  []string{},
		}),
	)
})

var _ = Describe("capi2mapi MachineSet Status Conversion", func() {
	Context("when converting CAPI MachineSet status to MAPI", func() {
		It("should set all MAPI MachineSet status fields and conditions to the expected values", func() {
			capiMachineSet := capibuilder.MachineSet().
				WithSelector(metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "test"},
				}).
				WithReplicas(5).
				WithStatusReplicas(5).
				WithStatusFullyLabeledReplicas(5).
				WithStatusReadyReplicas(4).
				WithStatusAvailableReplicas(3).
				WithStatusFailureReason(capierrors.MachineSetStatusError("InvalidConfiguration")).
				WithStatusFailureMessage("Test failure message").
				WithStatusConditions([]clusterv1beta1.Condition{
					{
						Type:     "Available",
						Status:   corev1.ConditionTrue,
						Severity: clusterv1beta1.ConditionSeverityNone,
						Reason:   "MachineSetAvailable",
						Message:  "MachineSet is available",
					},
				}).
				Build()

			mapiStatus := convertCAPIMachineSetStatusToMAPI(capiMachineSet.Status)

			Expect(mapiStatus.Replicas).To(Equal(int32(5)))
			Expect(mapiStatus.FullyLabeledReplicas).To(Equal(int32(5)))
			Expect(mapiStatus.ReadyReplicas).To(Equal(int32(4)))
			Expect(mapiStatus.AvailableReplicas).To(Equal(int32(3)))
			Expect(mapiStatus.ErrorReason).ToNot(BeNil())
			Expect(string(*mapiStatus.ErrorReason)).To(Equal("InvalidConfiguration"))
			Expect(mapiStatus.ErrorMessage).ToNot(BeNil())
			Expect(*mapiStatus.ErrorMessage).To(Equal("Test failure message"))
			// The only two conditions normally used for MAPI MachineSets are Paused and Synchronized.
			// We do not convert these conditions to MAPI conditions as they are managed directly by the machineSet sync and migration controllers.
			Expect(mapiStatus.Conditions).To(BeNil())
		})

		It("should set all MAPI MachineSet status fields and conditions to empty when CAPI MachineSetStatus is empty", func() {
			capiStatus := clusterv1beta1.MachineSetStatus{}

			mapiStatus := convertCAPIMachineSetStatusToMAPI(capiStatus)

			Expect(mapiStatus.Replicas).To(Equal(int32(0)))
			Expect(mapiStatus.FullyLabeledReplicas).To(Equal(int32(0)))
			Expect(mapiStatus.ReadyReplicas).To(Equal(int32(0)))
			Expect(mapiStatus.AvailableReplicas).To(Equal(int32(0)))
			Expect(mapiStatus.ObservedGeneration).To(Equal(int64(0)))
			Expect(mapiStatus.ErrorReason).To(BeNil())
			Expect(mapiStatus.ErrorMessage).To(BeNil())
			Expect(mapiStatus.Conditions).To(BeNil())
		})
	})
})
