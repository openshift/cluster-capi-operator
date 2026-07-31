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
	"github.com/aws/aws-sdk-go/service/ec2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
)

var _ = Describe("Cluster API AWS MachineSet", Ordered, func() {
	var (
		capiMachineSet *clusterv1.MachineSet
		mapiMachineSet *mapiv1beta1.MachineSet
		awsClient      *ec2.EC2
	)

	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip("Skipping AWS E2E tests")
		}

		mapiMachineSets, err := mapiframework.GetWorkerMachineSets(ctx, cl)
		Expect(err).ToNot(HaveOccurred(), "should not fail listing MAPI worker MachineSets")

		capiMachineSets, err := mapiframework.GetCAPIWorkerMachineSets(ctx, cl)
		Expect(err).ToNot(HaveOccurred(), "should not fail listing CAPI worker MachineSets")

		// Remove this once cluster-api-actuator-pkg is updated to use v1beta2
		var v1beta1MachineSet *clusterv1beta1.MachineSet

		// If MachineAPIMigration is enabled, ensure we've got a
		// MAPI-authoritative MAPI MachineSet, and a CAPI-only CAPI MachineSet
		if framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateMachineAPIMigration) {
			// Find a MAPI authoritative MachineSet
			for _, ms := range mapiMachineSets {
				if ms.Status.AuthoritativeAPI == machinev1.MachineAuthorityMachineAPI {
					mapiMachineSet = ms
					break
				}
			}

			for _, ms := range capiMachineSets {
				hasMAPI := func() bool {
					for _, ms := range mapiMachineSets {
						if ms.Status.AuthoritativeAPI == machinev1.MachineAuthorityMachineAPI {
							return true
						}
					}
					return false
				}()
				if !hasMAPI {
					v1beta1MachineSet = ms
					break
				}
			}
		} else {
			// Otherwise everything is native, just use the first one of each
			if len(mapiMachineSets) > 0 {
				mapiMachineSet = mapiMachineSets[0]
			}

			if len(capiMachineSets) > 0 {
				v1beta1MachineSet = capiMachineSets[0]
			}
		}

		if v1beta1MachineSet != nil {
			// Fetch v1beta2 MachineSet
			capiMachineSet = &clusterv1.MachineSet{}
			Expect(cl.Get(ctx, client.ObjectKey{Namespace: v1beta1MachineSet.Namespace, Name: v1beta1MachineSet.Name}, capiMachineSet)).To(Succeed(), "should not fail getting CAPI v1beta2 MachineSet")
		}

		if capiMachineSet == nil {
			if mapiMachineSet == nil {
				Fail("should find at least one MAPI or CAPI worker MachineSet")
			}

			By("creating a sample CAPI-only worker MachineSet", func() {
				capiMachineSet = createCAPIMachineSetFromMAPI(mapiMachineSet)

				capiMachineSets, err = mapiframework.GetCAPIWorkerMachineSets(ctx, cl)
				Expect(err).ToNot(HaveOccurred(), "should not fail listing CAPI worker MachineSets")
				Expect(capiMachineSets).ToNot(BeEmpty(), "should find at least one CAPI worker MachineSet")
			})
		}

		By("initializing AWS client for current region", func() {
			awsClient = createAWSClient(getRegionFromMachineSet(mapiMachineSet, capiMachineSet))
		})
	})

	It("should match MAPI native AWS MachineSet instances", func() {
		if mapiMachineSet == nil {
			Skip("cannot execute on CAPI native cluster")
		}

		compareInstances(awsClient, mapiMachineSet.Name, capiMachineSet.Name)
	})

	It("should contain ownership tag required for cluster deletion", func() {
		verifyCAPIInstanceOwnershipTag(awsClient, capiMachineSet.Name, clusterName)
	})
})

func getRegionFromMachineSet(mapiMachineSet *machinev1.MachineSet, capiMachineSet *clusterv1.MachineSet) string {
	if mapiMachineSet != nil {
		mapiProviderSpec, err := mapi2capi.AWSProviderSpecFromRawExtension(mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value)
		Expect(err).ToNot(HaveOccurred(), "should not fail decoding MAPI provider spec")
		return mapiProviderSpec.Placement.Region
	}

	if capiMachineSet != nil {
		awcluster := &awsv1.AWSCluster{}
		Expect(cl.Get(ctx, client.ObjectKey{Namespace: capiMachineSet.Namespace, Name: capiMachineSet.Spec.ClusterName}, awcluster)).To(Succeed(), "should not fail getting AWS Cluster")
		return awcluster.Spec.Region
	}

	Fail("should find at least one MAPI or CAPI worker MachineSet")
	return ""
}

func createCAPIMachineSetFromMAPI(mapiMachineSet *mapiv1beta1.MachineSet) *clusterv1.MachineSet {
	awsMachineTemplate := newAWSMachineTemplate(mapiMachineSet, infra)
	if err := cl.Create(ctx, awsMachineTemplate); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred(), "should not fail creating AWS machine template")
	}
	DeferCleanup(func() {
		framework.DeleteObjects(ctx, cl, awsMachineTemplate)
	})

	capiMachineSet := framework.CreateMachineSet(ctx, cl, framework.NewMachineSetParams(
		"aws-machineset",
		clusterName,
		"",
		1,
		clusterv1.ContractVersionedObjectReference{
			Kind:     "AWSMachineTemplate",
			APIGroup: infraAPIGroup,
			Name:     awsMachineTemplate.Name,
		},
		"worker-user-data",
	))
	DeferCleanup(func() {
		framework.DeleteMachineSets(ctx, cl, capiMachineSet)
		framework.WaitForMachineSetsDeleted(capiMachineSet)
	})

	framework.WaitForMachineSet(ctx, cl, capiMachineSet.Name, capiMachineSet.Namespace, framework.WaitLong)

	return capiMachineSet
}
