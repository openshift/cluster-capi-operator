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
	"github.com/openshift/api/features"
	mapiv1alpha1 "github.com/openshift/api/machine/v1alpha1"
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
	testAWSProviderID         = "aws:///us-west-2a/i-test123456"
	testTaintValue            = "test-taint-value"
	testLabelValue            = "test-label-value"
	testInstanceType          = "m5.xlarge"
	testAMIID                 = "ami-test123456"
	testSubnetID              = "subnet-test123456"
	testSecurityGroupID       = "sg-test123456"
	testCapacityReservationID = "cr-test123456"

	// Test values for OpenStack provider spec updates.
	testOpenStackProviderID        = "openstack:///da952699-e577-4462-a8ae-686e804fb5c7"
	testOpenStackFlavor            = "flavor-non-existing"
	testOpenStackImage             = "test-image"
	testOpenStackAvailabilityZone  = "nova-test"
	testOpenStackNetworkID         = "network-test123456"
	testOpenStackSecurityGroupName = "test-security-group"
	testOpenStackTag               = "test-tag"

	// VAP error messages - from actual VAP policy.
	vapSpecLockedMessage          = "You may only modify spec.authoritativeAPI. Any other change inside .spec is not allowed. This is because status.authoritativeAPI is set to Cluster API."
	vapProtectedLabelMessage      = "Cannot add, modify or delete any machine.openshift.io/*, kubernetes.io/* or cluster.x-k8s.io/* label. This is because status.authoritativeAPI is set to Cluster API."
	vapProtectedAnnotationMessage = "Cannot add, modify or delete any machine.openshift.io/* or cluster.x-k8s.io/* or clusters.x-k8s.io/* annotation. This is because status.authoritativeAPI is set to Cluster API."

	// CAPI Machine VAP error messages - from openshift-cluster-api-prevent-setting-of-capi-fields-unsupported-by-mapi.
	vapCAPIForbiddenFieldMessage = "spec.%s is a forbidden field"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] MAPI Machine VAP Tests", Ordered, func() {
	BeforeAll(func() {
		skipUnsupportedPlatforms()

		if !capiframework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateMachineAPIMigration) {
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
			testCAPIMachine = capiframework.GetMachineWithRetry(testMachineName, capiframework.CAPINamespace)

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
					providerIDValue := testAWSProviderID
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
			BeforeEach(func() {
				if platform != configv1.AWSPlatformType {
					Skip("Skipping AWS-specific tests on non-AWS platform.")
				}
			})

			It("should prevent updating providerSpec.instanceType", func() {
				verifyProviderSpecUpdatePrevented(testMAPIMachine, "AWS", "instanceType", func() error {
					return updateMachineProviderSpec(testMAPIMachine, func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
						providerSpec.InstanceType = testInstanceType
					})
				}, vapSpecLockedMessage)
			})

			It("should prevent updating providerSpec.amiID", func() {
				verifyProviderSpecUpdatePrevented(testMAPIMachine, "AWS", "amiID", func() error {
					return updateMachineProviderSpec(testMAPIMachine, func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
						amiID := testAMIID
						if providerSpec.AMI.ID == nil {
							providerSpec.AMI.ID = &amiID
						} else {
							*providerSpec.AMI.ID = amiID
						}
					})
				}, vapSpecLockedMessage)
			})

			It("should prevent updating providerSpec.availabilityZone", func() {
				verifyProviderSpecUpdatePrevented(testMAPIMachine, "AWS", "availabilityZone", func() error {
					return updateMachineProviderSpec(testMAPIMachine, func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
						providerSpec.Placement.AvailabilityZone = providerSpec.Placement.AvailabilityZone + "-changed"
					})
				}, vapSpecLockedMessage)
			})

			It("should prevent updating providerSpec.subnetID", func() {
				verifyProviderSpecUpdatePrevented(testMAPIMachine, "AWS", "subnetID", func() error {
					return updateMachineProviderSpec(testMAPIMachine, func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
						subnetID := testSubnetID
						providerSpec.Subnet = mapiv1beta1.AWSResourceReference{
							ID: &subnetID,
						}
					})
				}, vapSpecLockedMessage)
			})

			It("should prevent updating providerSpec.securityGroups", func() {
				verifyProviderSpecUpdatePrevented(testMAPIMachine, "AWS", "securityGroups", func() error {
					return updateMachineProviderSpec(testMAPIMachine, func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
						sgID := testSecurityGroupID
						providerSpec.SecurityGroups = []mapiv1beta1.AWSResourceReference{{
							ID: &sgID,
						}}
					})
				}, vapSpecLockedMessage)
			})

			It("should prevent updating providerSpec.tags", func() {
				verifyProviderSpecUpdatePrevented(testMAPIMachine, "AWS", "tags", func() error {
					return updateMachineProviderSpec(testMAPIMachine, func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
						providerSpec.Tags = []mapiv1beta1.TagSpecification{{
							Name:  "test-key",
							Value: "test-value",
						}}
					})
				}, vapSpecLockedMessage)
			})

			It("should prevent updating providerSpec.capacityReservationId", func() {
				verifyProviderSpecUpdatePrevented(testMAPIMachine, "AWS", "capacityReservationId", func() error {
					return updateMachineProviderSpec(testMAPIMachine, func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
						providerSpec.CapacityReservationID = testCapacityReservationID
					})
				}, vapSpecLockedMessage)
			})
		})

		Context("OpenStack provider spec field restrictions", func() {
			BeforeEach(func() {
				if platform != configv1.OpenStackPlatformType {
					Skip("Skipping OpenStack-specific tests on non-OpenStack platform.")
				}
			})

			DescribeTable(
				"should prevent updating OpenStack providerSpec fields",
				func(fieldName string, updateFunc func(providerSpec *mapiv1alpha1.OpenstackProviderSpec)) {
					verifyProviderSpecUpdatePrevented(testMAPIMachine, "OpenStack", fieldName, func() error {
						return updateMachineProviderSpec(testMAPIMachine, updateFunc)
					}, vapSpecLockedMessage)
				},
				Entry("flavor", "flavor", func(providerSpec *mapiv1alpha1.OpenstackProviderSpec) {
					providerSpec.Flavor = testOpenStackFlavor
				}),
				Entry("image", "image", func(providerSpec *mapiv1alpha1.OpenstackProviderSpec) {
					providerSpec.Image = testOpenStackImage
				}),
				Entry("availabilityZone", "availabilityZone", func(providerSpec *mapiv1alpha1.OpenstackProviderSpec) {
					providerSpec.AvailabilityZone = testOpenStackAvailabilityZone
				}),
				Entry("networks", "networks", func(providerSpec *mapiv1alpha1.OpenstackProviderSpec) {
					providerSpec.Networks = []mapiv1alpha1.NetworkParam{{UUID: testOpenStackNetworkID}}
				}),
				Entry("securityGroups", "securityGroups", func(providerSpec *mapiv1alpha1.OpenstackProviderSpec) {
					providerSpec.SecurityGroups = []mapiv1alpha1.SecurityGroupParam{{Name: testOpenStackSecurityGroupName}}
				}),
				Entry("tags", "tags", func(providerSpec *mapiv1alpha1.OpenstackProviderSpec) {
					providerSpec.Tags = []string{testOpenStackTag}
				}),
			)
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
			testCAPIMachine = capiframework.GetMachineWithRetry(testMachineName, capiframework.CAPINamespace)

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

// updateMachineProviderSpec updates a machine's provider spec using the provided update function.
func updateMachineProviderSpec[T any](machine *mapiv1beta1.Machine, updateFunc func(*T)) error {
	if machine.Spec.ProviderSpec.Value == nil {
		return fmt.Errorf("failed to extract ProviderSpec from Machine %s", machine.Name)
	}

	providerSpec := new(T)
	if err := json.Unmarshal(machine.Spec.ProviderSpec.Value.Raw, providerSpec); err != nil {
		return fmt.Errorf("failed to unmarshal ProviderSpec from Machine %s: %w", machine.Name, err)
	}

	updateFunc(providerSpec)

	modifiedRaw, err := json.Marshal(providerSpec)
	if err != nil {
		return fmt.Errorf("failed to marshal modified ProviderSpec: %w", err)
	}

	machine.Spec.ProviderSpec.Value = &runtime.RawExtension{Raw: modifiedRaw}

	return nil
}

// verifyProviderSpecUpdatePrevented verifies that providerSpec field updates are prevented by VAP.
func verifyProviderSpecUpdatePrevented(machine *mapiv1beta1.Machine, providerName, fieldName string, updateProviderSpec func() error, expectedError string) {
	GinkgoHelper()

	By(fmt.Sprintf("Verifying that updating %s providerSpec.%s is prevented by VAP", providerName, fieldName))

	Eventually(komega.Update(machine, func() {
		Expect(updateProviderSpec()).To(Succeed())
	}), capiframework.WaitShort, capiframework.RetryShort).Should(
		MatchError(ContainSubstring(expectedError)),
		"Expected %s providerSpec.%s update to be blocked by VAP", providerName, fieldName)
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
		var providerIDValue string
		switch platform {
		case configv1.AWSPlatformType:
			providerIDValue = testAWSProviderID
		case configv1.OpenStackPlatformType:
			providerIDValue = testOpenStackProviderID
		default:
			Fail(fmt.Sprintf("Platform %q is not supported yet", platform))
		}

		testMachine.Spec.ProviderID = &providerIDValue
	}), capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(),
		"Expected spec update to succeed when authoritativeAPI is MachineAPI (VAP should not apply)")
}
