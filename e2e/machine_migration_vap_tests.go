package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	yaml "sigs.k8s.io/yaml"
)

// Constants for VAP testing - based on actual VAP: machine-api-machine-vap
const (
	// Test values for MAPI machine updates
	testProviderID            = "aws:///us-west-2a/i-test123456"
	testTaintValue            = "test-taint-value"
	testLabelValue            = "test-label-value"
	testInstanceType          = "m5.xlarge"
	testAMIID                 = "ami-test123456"
	testAvailabilityZone      = "us-west-2b"
	testSubnetID              = "subnet-test123456"
	testSecurityGroupID       = "sg-test123456"
	testVolumeSize            = int64(120)
	testCapacityReservationID = "cr-test123456"

	// VAP error messages - from actual VAP policy
	vapSpecLockedMessage          = "You may only modify spec.authoritativeAPI. Any other change inside .spec is not allowed. This is because status.authoritativeAPI is set to Cluster API."
	vapProtectedLabelMessage      = "Cannot add, modify or delete any machine.openshift.io/* or kubernetes.io/* label. This is because status.authoritativeAPI is set to Cluster API."
	vapProtectedAnnotationMessage = "Cannot add, modify or delete any machine.openshift.io/* annotation. This is because status.authoritativeAPI is set to Cluster API."
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] MAPI Machine VAP Tests", Ordered, func() {
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	var _ = Describe("VAP: machine-api-machine-vap enforcement", Ordered, func() {
		var testMachineName = "machine-vap-test-capi-auth"
		var testMAPIMachine *mapiv1beta1.Machine
		var testCAPIMachine *clusterv1.Machine

		BeforeAll(func() {
			// Create a MAPI machine with ClusterAPI authority to trigger VAP enforcement
			testMAPIMachine = createMAPIMachineWithAuthority(ctx, cl, testMachineName, mapiv1beta1.MachineAuthorityClusterAPI)

			// The VAP requires a matching CAPI machine as parameter
			testCAPIMachine = capiframework.GetMachine(cl, testMachineName, capiframework.CAPINamespace)

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

		Context("VAP match conditions verification", func() {
			It("should not apply VAP when authoritativeAPI is MachineAPI", func() {
				verifyVAPNotAppliedForMachineAPIAuthority()
			})
		})
	})
})

// verifyUpdatePrevented verifies that a machine update is prevented by VAP
func verifyUpdatePrevented(machine *mapiv1beta1.Machine, updateFunc func(), expectedError string) {
	By("Verifying that machine update is prevented by VAP")

	Eventually(komega.Update(machine, updateFunc), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		MatchError(ContainSubstring(expectedError)),
		"Expected machine update to be blocked by VAP")
}

// verifyUpdateAllowed verifies that a machine update is allowed (not blocked by VAP)
func verifyUpdateAllowed(machine *mapiv1beta1.Machine, updateFunc func()) {
	By("Verifying that machine update is allowed")

	Eventually(komega.Update(machine, updateFunc), capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(),
		"Expected machine update to succeed")
}

// verifyAWSProviderSpecUpdatePrevented verifies that AWS providerSpec field updates are prevented by VAP
func verifyAWSProviderSpecUpdatePrevented(machine *mapiv1beta1.Machine, fieldName string, testValue interface{}, expectedError string) {
	By(fmt.Sprintf("Verifying that updating AWS providerSpec.%s is prevented by VAP", fieldName))

	Eventually(func() error {
		// Get fresh copy to avoid conflicts
		freshMachine := &mapiv1beta1.Machine{}
		if err := cl.Get(ctx, client.ObjectKeyFromObject(machine), freshMachine); err != nil {
			return err
		}

		// Parse the current providerSpec
		if freshMachine.Spec.ProviderSpec.Value == nil {
			return fmt.Errorf("providerSpec is nil")
		}

		providerSpec := &mapiv1beta1.AWSMachineProviderConfig{}
		if err := yaml.Unmarshal(freshMachine.Spec.ProviderSpec.Value.Raw, providerSpec); err != nil {
			return fmt.Errorf("failed to unmarshal providerSpec: %v", err)
		}

		// Modify the specified field
		switch fieldName {
		case "instanceType":
			providerSpec.InstanceType = testValue.(string)
		case "amiID":
			testValueStr := testValue.(string)
			if providerSpec.AMI.ID == nil {
				providerSpec.AMI.ID = &testValueStr
			} else {
				*providerSpec.AMI.ID = testValueStr
			}
		case "availabilityZone":
			providerSpec.Placement.AvailabilityZone = testValue.(string)
		case "subnetID":
			testValueStr := testValue.(string)
			providerSpec.Subnet = mapiv1beta1.AWSResourceReference{
				ID: &testValueStr,
			}
		case "securityGroups":
			providerSpec.SecurityGroups = []mapiv1beta1.AWSResourceReference{{
				ID: &[]string{testValue.(string)}[0],
			}}
		case "volumeSize":
			if len(providerSpec.BlockDevices) > 0 {
				if providerSpec.BlockDevices[0].EBS != nil {
					providerSpec.BlockDevices[0].EBS.VolumeSize = &[]int64{testValue.(int64)}[0]
				}
			}
		case "volumeType":
			if len(providerSpec.BlockDevices) > 0 {
				if providerSpec.BlockDevices[0].EBS != nil {
					providerSpec.BlockDevices[0].EBS.VolumeType = &[]string{testValue.(string)}[0]
				}
			}
		case "encryption":
			if len(providerSpec.BlockDevices) > 0 {
				if providerSpec.BlockDevices[0].EBS != nil {
					providerSpec.BlockDevices[0].EBS.Encrypted = &[]bool{testValue.(bool)}[0]
				}
			}
		case "tags":
			// Convert map to TagSpecification slice
			tagMap := testValue.(map[string]string)
			var tags []mapiv1beta1.TagSpecification
			for key, value := range tagMap {
				tags = append(tags, mapiv1beta1.TagSpecification{
					Name:  key,
					Value: value,
				})
			}
			providerSpec.Tags = tags
		case "capacityReservationId":
			providerSpec.SpotMarketOptions = &mapiv1beta1.SpotMarketOptions{
				MaxPrice: &[]string{"0.10"}[0],
			}
			// Note: capacityReservationId might be in different location based on AWS API version
		default:
			return fmt.Errorf("unsupported field: %s", fieldName)
		}

		// Marshal back to raw extension
		modifiedRaw, err := yaml.Marshal(providerSpec)
		if err != nil {
			return fmt.Errorf("failed to marshal modified providerSpec: %v", err)
		}

		freshMachine.Spec.ProviderSpec.Value = &runtime.RawExtension{Raw: modifiedRaw}
		return cl.Update(ctx, freshMachine)
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(
		MatchError(ContainSubstring(expectedError)),
		"Expected AWS providerSpec.%s update to be blocked by VAP", fieldName)
}

// verifyVAPNotAppliedForMachineAPIAuthority verifies that VAP is not applied when authoritativeAPI is MachineAPI
func verifyVAPNotAppliedForMachineAPIAuthority() {
	By("Verifying that VAP is not applied when authoritativeAPI is MachineAPI")

	// Create a test machine with MachineAPI authority
	testMachine := createMAPIMachineWithAuthority(ctx, cl, "vap-test-mapi-authority", mapiv1beta1.MachineAuthorityMachineAPI)

	DeferCleanup(func() {
		By("Cleaning up test machine")
		mapiframework.DeleteMachines(ctx, cl, testMachine)
	})

	// Verify we can update spec fields (VAP should not apply)
	Eventually(komega.Update(testMachine, func() {
		// Try to update a spec field - this should be allowed since VAP doesn't apply
		providerIDValue := testProviderID
		testMachine.Spec.ProviderID = &providerIDValue
	}), capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(),
		"Expected spec update to succeed when authoritativeAPI is MachineAPI (VAP should not apply)")
}
