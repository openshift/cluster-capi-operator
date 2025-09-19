package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Machine Migration MAPI Authoritative Tests", Ordered, func() {
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	var _ = Describe("Create standalone MAPI Machine", Ordered, func() {
		var mapiMachineAuthMAPIName = "machine-authoritativeapi-mapi"
		var newCapiMachine *clusterv1.Machine
		var newMapiMachine *mapiv1beta1.Machine

		Context("With spec.authoritativeAPI: MachineAPI and no existing CAPI Machine with that name", func() {
			BeforeAll(func() {
				newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI)

				DeferCleanup(func() {
					By("Cleaning up machine resources")
					cleanupMachineResources(
						ctx,
						cl,
						[]*clusterv1.Machine{},
						[]*mapiv1beta1.Machine{newMapiMachine},
					)
				})
			})

			It("should find MAPI Machine .status.authoritativeAPI to equal MachineAPI", func() {
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})
			It("should verify MAPI Machine Synchronized condition is True", func() {
				verifyMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})
			It("should verify MAPI Machine Paused condition is False", func() {
				verifyMAPIMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})
			It("should verify that the MAPI Machine has a CAPI Machine", func() {
				newCapiMachine = capiframework.GetMachine(cl, mapiMachineAuthMAPIName, capiframework.CAPINamespace)
			})
			It("should verify CAPI Machine Paused condition is True", func() {
				verifyCAPIMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})
		})
	})
})
