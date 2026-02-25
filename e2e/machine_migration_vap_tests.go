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
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

// Constants for VAP testing - based on actual VAP: machine-api-machine-vap.
const (
	// Test values for MAPI machine updates.
	testProviderID            = "aws:///us-west-2a/i-test123456"
	testTaintValue            = "test-taint-value"
	testLabelValue            = "test-label-value"
	testInstanceType          = "m5.xlarge"
	testAMIID                 = "ami-test123456"
	testAvailabilityZone      = "us-west-2b"
	testSubnetID              = "subnet-test123456"
	testSecurityGroupID       = "sg-test123456"
	testCapacityReservationID = "cr-test123456"

	// VAP error messages - from actual VAP policy.
	vapSpecLockedMessage          = "You may only modify spec.authoritativeAPI. Any other change inside .spec is not allowed. This is because status.authoritativeAPI is set to Cluster API."
	vapProtectedLabelMessage      = "Cannot add, modify or delete any machine.openshift.io/*, kubernetes.io/* or cluster.x-k8s.io/* label. This is because status.authoritativeAPI is set to Cluster API."
	vapProtectedAnnotationMessage = "Cannot add, modify or delete any machine.openshift.io/* or cluster.x-k8s.io/* or clusters.x-k8s.io/* annotation. This is because status.authoritativeAPI is set to Cluster API."

	// CAPI Machine VAP error messages - from openshift-cluster-api-prevent-setting-of-capi-fields-unsupported-by-mapi.
	vapCAPIForbiddenFieldMessage = "spec.%s is a forbidden field"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] MAPI Machine VAP Tests", Ordered, func() {
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	Describe("VAP: machine-api-machine-vap enforcement", Ordered, func() {
		var testMachineName string
		var testMAPIMachine *mapiv1beta1.Machine
		var testCAPIMachine *clusterv1.Machine

		BeforeAll(func() {
			testMachineName = generateName("machine-vap-capi-auth-")
			// Create a MAPI machine with ClusterAPI authority to trigger VAP enforcement
			testMAPIMachine = createMAPIMachineWithAuthority(ctx, cl, testMachineName, mapiv1beta1.MachineAuthorityClusterAPI)

			// The VAP requires a matching CAPI machine as parameter
			testCAPIMachine = capiframework.GetMachine(testMachineName, capiframework.CAPINamespace)

			// Wait until VAP match conditions are met
			Eventually(komega.Object(testMAPIMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
				WithTransform(func(m *mapiv1beta1.Machine) mapiv1beta1.MachineAuthority {
					return m.Status.AuthoritativeAPI
				}, Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
				"VAP requires status.authoritativeAPI=Cluster API before enforcement",
			)

			DeferCleanup(func() {
				By("Cleaning up machine resources")
				cleanupMachineResources(
					ctx,
					cl,
					[]*clusterv1.Machine{testCAPIMachine},
					[]*mapiv1beta1.Machine{testMAPIMachine},
				)
			})
		})

		Context("spec field restrictions", func() {
			It("should prevent updating spec.providerID", func() {
				verifyUpdatePrevented(testMAPIMachine, func() {
					providerIDValue := testProviderID
					testMAPIMachine.Spec.ProviderID = &providerIDValue
				}, vapSpecLockedMessage)
			})

			It("should prevent updating spec.taints", func() {
				verifyUpdatePrevented(testMAPIMachine, func() {
					testMAPIMachine.Spec.Taints = []corev1.Taint{{
						Key:    "test-taint",
						Value:  testTaintValue,
						Effect: corev1.TaintEffectNoSchedule,
					}}
				}, vapSpecLockedMessage)
			})

			It("should prevent updating spec.metadata", func() {
				verifyUpdatePrevented(testMAPIMachine, func() {
					if testMAPIMachine.Spec.ObjectMeta.Labels == nil {
						testMAPIMachine.Spec.ObjectMeta.Labels = make(map[string]string)
					}
					testMAPIMachine.Spec.ObjectMeta.Labels["test-spec-label"] = testLabelValue
				}, vapSpecLockedMessage)
			})
		})

		Context("protected label restrictions", func() {
			It("should prevent modifying machine.openshift.io/* labels", func() {
				verifyUpdatePrevented(testMAPIMachine, func() {
					if testMAPIMachine.Labels == nil {
						testMAPIMachine.Labels = make(map[string]string)
					}
					testMAPIMachine.Labels["machine.openshift.io/test-label"] = testLabelValue
				}, vapProtectedLabelMessage)
			})

			It("should allow modifying non-protected labels", func() {
				verifyUpdateAllowed(testMAPIMachine, func() {
					if testMAPIMachine.Labels == nil {
						testMAPIMachine.Labels = make(map[string]string)
					}
					testMAPIMachine.Labels["test-label"] = "allowed-value"
				})
			})
		})

		Context("protected annotation restrictions", func() {
			It("should prevent modifying machine.openshift.io/* annotations", func() {
				verifyUpdatePrevented(testMAPIMachine, func() {
					if testMAPIMachine.Annotations == nil {
						testMAPIMachine.Annotations = make(map[string]string)
					}
					testMAPIMachine.Annotations["machine.openshift.io/test-annotation"] = "test-value"
				}, vapProtectedAnnotationMessage)
			})

			It("should allow modifying non-protected annotations", func() {
				verifyUpdateAllowed(testMAPIMachine, func() {
					if testMAPIMachine.Annotations == nil {
						testMAPIMachine.Annotations = make(map[string]string)
					}
					testMAPIMachine.Annotations["test-annotation"] = "allowed-value"
				})
			})
		})

		Context("AWS provider spec field restrictions", func() {
			It("should prevent updating providerSpec.instanceType", func() {
				verifyAWSProviderSpecUpdatePrevented(testMAPIMachine, "instanceType", func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
					providerSpec.InstanceType = testInstanceType
				}, vapSpecLockedMessage)
			})

			It("should prevent updating providerSpec.amiID", func() {
				verifyAWSProviderSpecUpdatePrevented(testMAPIMachine, "amiID", func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
					amiID := testAMIID
					if providerSpec.AMI.ID == nil {
						providerSpec.AMI.ID = &amiID
					} else {
						*providerSpec.AMI.ID = amiID
					}
				}, vapSpecLockedMessage)
			})

			It("should prevent updating providerSpec.availabilityZone", func() {
				verifyAWSProviderSpecUpdatePrevented(testMAPIMachine, "availabilityZone", func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
					providerSpec.Placement.AvailabilityZone = testAvailabilityZone
				}, vapSpecLockedMessage)
			})

			It("should prevent updating providerSpec.subnetID", func() {
				verifyAWSProviderSpecUpdatePrevented(testMAPIMachine, "subnetID", func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
					subnetID := testSubnetID
					providerSpec.Subnet = mapiv1beta1.AWSResourceReference{
						ID: &subnetID,
					}
				}, vapSpecLockedMessage)
			})

			It("should prevent updating providerSpec.securityGroups", func() {
				verifyAWSProviderSpecUpdatePrevented(testMAPIMachine, "securityGroups", func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
					sgID := testSecurityGroupID
					providerSpec.SecurityGroups = []mapiv1beta1.AWSResourceReference{{
						ID: &sgID,
					}}
				}, vapSpecLockedMessage)
			})

			It("should prevent updating providerSpec.tags", func() {
				verifyAWSProviderSpecUpdatePrevented(testMAPIMachine, "tags", func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
					providerSpec.Tags = []mapiv1beta1.TagSpecification{{
						Name:  "test-key",
						Value: "test-value",
					}}
				}, vapSpecLockedMessage)
			})

			It("should prevent updating providerSpec.capacityReservationId", func() {
				verifyAWSProviderSpecUpdatePrevented(testMAPIMachine, "capacityReservationId", func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
					providerSpec.CapacityReservationID = testCapacityReservationID
				}, vapSpecLockedMessage)
			})
		})

		Context("VAP match conditions verification", func() {
			It("should not apply VAP when authoritativeAPI is MachineAPI", func() {
				verifyVAPNotAppliedForMachineAPIAuthority()
			})
		})
	})

	Describe("CAPI Machine VAP: openshift-cluster-api-prevent-setting-of-capi-fields-unsupported-by-mapi enforcement", Ordered, func() {
		var testMachineName string
		var testMAPIMachine *mapiv1beta1.Machine
		var testCAPIMachine *clusterv1.Machine

		BeforeAll(func() {
			testMachineName = generateName("machine-vap-capi-forbidden-")
			// Create a MAPI machine with ClusterAPI authority to trigger CAPI machine creation
			testMAPIMachine = createMAPIMachineWithAuthority(ctx, cl, testMachineName, mapiv1beta1.MachineAuthorityClusterAPI)

			// Get the corresponding CAPI machine from openshift-cluster-api namespace
			testCAPIMachine = capiframework.GetMachine(testMachineName, capiframework.CAPINamespace)

			// Wait until VAP match conditions are met
			Eventually(komega.Object(testMAPIMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
				WithTransform(func(m *mapiv1beta1.Machine) mapiv1beta1.MachineAuthority {
					return m.Status.AuthoritativeAPI
				}, Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
				"VAP requires status.authoritativeAPI=Cluster API before enforcement",
			)

			DeferCleanup(func() {
				By("Cleaning up machine resources")
				cleanupMachineResources(
					ctx,
					cl,
					[]*clusterv1.Machine{testCAPIMachine},
					[]*mapiv1beta1.Machine{testMAPIMachine},
				)
			})
		})

		Context("forbidden CAPI field restrictions", func() {
			It("should prevent updating spec.version", func() {
				verifyCAPIUpdatePrevented(testCAPIMachine, func() {
					testCAPIMachine.Spec.Version = "v1"
				}, fmt.Sprintf(vapCAPIForbiddenFieldMessage, "version"))
			})

			It("should prevent updating spec.readinessGates[0].conditionType", func() {
				verifyCAPIUpdatePrevented(testCAPIMachine, func() {
					if len(testCAPIMachine.Spec.ReadinessGates) > 0 {
						// Try to modify existing readinessGates[0].conditionType
						testCAPIMachine.Spec.ReadinessGates[0].ConditionType = "test-condition"
					} else {
						// If readinessGates doesn't exist or is empty, try to add one
						testCAPIMachine.Spec.ReadinessGates = []clusterv1.MachineReadinessGate{{
							ConditionType: "test-condition",
						}}
					}
				}, fmt.Sprintf(vapCAPIForbiddenFieldMessage, "readinessGates"))
			})
		})
	})
})

// verifyUpdatePrevented verifies that a machine update is prevented by VAP.
func verifyUpdatePrevented(machine *mapiv1beta1.Machine, updateFunc func(), expectedError string) {
	GinkgoHelper()

	By("Verifying that machine update is prevented by VAP")

	Eventually(komega.Update(machine, updateFunc), capiframework.WaitShort, capiframework.RetryShort).Should(
		MatchError(ContainSubstring(expectedError)),
		"Expected machine update to be blocked by VAP")
}

// verifyUpdateAllowed verifies that a machine update is allowed (not blocked by VAP).
func verifyUpdateAllowed(machine *mapiv1beta1.Machine, updateFunc func()) {
	GinkgoHelper()

	By("Verifying that machine update is allowed")

	Eventually(komega.Update(machine, updateFunc), capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(),
		"Expected machine update to succeed")
}

// verifyCAPIUpdatePrevented verifies that a CAPI machine update is prevented by VAP.
func verifyCAPIUpdatePrevented(machine *clusterv1.Machine, updateFunc func(), expectedError string) {
	GinkgoHelper()

	By("Verifying that CAPI machine update is prevented by VAP")

	Eventually(komega.Update(machine, updateFunc), capiframework.WaitShort, capiframework.RetryShort).Should(
		MatchError(ContainSubstring(expectedError)),
		"Expected CAPI machine update to be blocked by VAP")
}

// updateAWSMachineProviderSpec updates an AWS machine's provider spec using the provided update function.
func updateAWSMachineProviderSpec(machine *mapiv1beta1.Machine, updateFunc func(*mapiv1beta1.AWSMachineProviderConfig)) error {
	providerSpec := getAWSProviderSpecFromMachine(machine)
	if providerSpec == nil {
		return fmt.Errorf("failed to extract AWS ProviderSpec from Machine %s", machine.Name)
	}

	updateFunc(providerSpec)

	modifiedRaw, err := json.Marshal(providerSpec)
	if err != nil {
		return fmt.Errorf("failed to marshal modified providerSpec: %w", err)
	}

	machine.Spec.ProviderSpec.Value = &runtime.RawExtension{Raw: modifiedRaw}

	return nil
}

// getAWSProviderSpecFromMachine extracts the AWS provider spec from a machine.
// Returns nil if the ProviderSpec is nil or unmarshalling fails, so it is safe
// to use inside WithTransform (no Expect/panic in retry loops).
func getAWSProviderSpecFromMachine(machine *mapiv1beta1.Machine) *mapiv1beta1.AWSMachineProviderConfig {
	if machine.Spec.ProviderSpec.Value == nil {
		return nil
	}

	providerSpec := &mapiv1beta1.AWSMachineProviderConfig{}
	if err := json.Unmarshal(machine.Spec.ProviderSpec.Value.Raw, providerSpec); err != nil {
		GinkgoWriter.Printf("Warning: failed to unmarshal ProviderSpec for Machine %s: %v\n", machine.Name, err)
		return nil
	}

	return providerSpec
}

// verifyAWSProviderSpecUpdatePrevented verifies that AWS providerSpec field updates are prevented by VAP.
func verifyAWSProviderSpecUpdatePrevented(machine *mapiv1beta1.Machine, fieldName string, updateFunc func(*mapiv1beta1.AWSMachineProviderConfig), expectedError string) {
	GinkgoHelper()

	By(fmt.Sprintf("Verifying that updating AWS providerSpec.%s is prevented by VAP", fieldName))

	Eventually(komega.Update(machine, func() {
		Expect(updateAWSMachineProviderSpec(machine, updateFunc)).To(Succeed())
	}), capiframework.WaitShort, capiframework.RetryShort).Should(
		MatchError(ContainSubstring(expectedError)),
		"Expected AWS providerSpec.%s update to be blocked by VAP", fieldName)
}

// verifyVAPNotAppliedForMachineAPIAuthority verifies that VAP is not applied when authoritativeAPI is MachineAPI.
func verifyVAPNotAppliedForMachineAPIAuthority() {
	GinkgoHelper()

	By("Verifying that VAP is not applied when authoritativeAPI is MachineAPI")

	// Create a test machine with MachineAPI authority
	testMachine := createMAPIMachineWithAuthority(ctx, cl, generateName("vap-test-mapi-auth-"), mapiv1beta1.MachineAuthorityMachineAPI)

	// Wait until status reflects the expected authority
	Eventually(komega.Object(testMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		WithTransform(func(m *mapiv1beta1.Machine) mapiv1beta1.MachineAuthority {
			return m.Status.AuthoritativeAPI
		}, Equal(mapiv1beta1.MachineAuthorityMachineAPI)),
		"Expected status.authoritativeAPI=MachineAPI before VAP bypass test",
	)

	DeferCleanup(func() {
		By("Cleaning up test machine")
		Expect(mapiframework.DeleteMachines(ctx, cl, testMachine)).To(Succeed())
	})

	// Verify we can update spec fields (VAP should not apply)
	Eventually(komega.Update(testMachine, func() {
		// Try to update a spec field - this should be allowed since VAP doesn't apply
		providerIDValue := testProviderID
		testMachine.Spec.ProviderID = &providerIDValue
	}), capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(),
		"Expected spec update to succeed when authoritativeAPI is MachineAPI (VAP should not apply)")
}
