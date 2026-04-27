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
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var _ = Describe("Cluster API AWS MachineSet", Ordered, func() {
	var (
		awsMachineTemplate  *awsv1.AWSMachineTemplate
		machineSet          *clusterv1.MachineSet
		mapiDefaultMS       *mapiv1beta1.MachineSet
		awsClient           *ec2.EC2
	)

	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip("Skipping AWS E2E tests")
		}
		mapiDefaultMS = framework.GetFirstMAPIMachineSet(ctx, cl)

		mapiProviderSpec, err := mapi2capi.AWSProviderSpecFromRawExtension(mapiDefaultMS.Spec.Template.Spec.ProviderSpec.Value)
		Expect(err).ToNot(HaveOccurred(), "should not fail decoding MAPI provider spec")
		awsClient = createAWSClient(mapiProviderSpec.Placement.Region)
	})

	AfterEach(func() {
		if platform != configv1.AWSPlatformType {
			// Because AfterEach always runs, even when tests are skipped, we have to
			// explicitly skip it here for other platforms.
			Skip("Skipping AWS E2E tests")
		}
		framework.DeleteMachineSets(ctx, cl, machineSet)
		framework.WaitForMachineSetsDeleted(machineSet)
		framework.DeleteObjects(ctx, cl, awsMachineTemplate)
	})

	It("should be able to run a machine with a default provider spec", func() {
		awsMachineTemplate = newAWSMachineTemplate(mapiDefaultMS, infra)
		if err := cl.Create(ctx, awsMachineTemplate); err != nil && !apierrors.IsAlreadyExists(err) {
			Expect(err).ToNot(HaveOccurred(), "should not fail creating AWS machine template")
		}

		machineSet = framework.CreateMachineSet(ctx, cl, framework.NewMachineSetParams(
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

		framework.WaitForMachineSet(ctx, cl, machineSet.Name, machineSet.Namespace, framework.WaitLong)

		compareInstances(awsClient, mapiDefaultMS.Name, "aws-machineset")
	})
})
