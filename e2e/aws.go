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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("[sig-cluster-lifecycle][Feature:ClusterAPI][platform:aws][Disruptive] Cluster API AWS MachineSet", Ordered, Label("Conformance"), Label("Serial"), func() {
	var (
		awsMachineTemplate      *awsv1.AWSMachineTemplate
		machineSet              *clusterv1.MachineSet
		mapiDefaultMSName       string
		mapiDefaultProviderSpec *mapiv1beta1.AWSMachineProviderConfig
		awsClient               *ec2.EC2
	)

	BeforeAll(func() {
		InitCommonVariables()
		if platform != configv1.AWSPlatformType {
			Skip("Skipping AWS E2E tests")
		}
		mapiDefaultProviderSpec = framework.GetMAPIProviderSpec[mapiv1beta1.AWSMachineProviderConfig](ctx, cl)

		machineSetList := &mapiv1beta1.MachineSetList{}
		Expect(cl.List(ctx, machineSetList, client.InNamespace(framework.MAPINamespace))).To(Succeed(),
			"should not fail listing MAPI MachineSets")
		framework.SortListByName(machineSetList)
		mapiDefaultMSName = machineSetList.Items[0].Name

		awsClient = createAWSClient(mapiDefaultProviderSpec.Placement.Region)
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
		awsMachineTemplate = newAWSMachineTemplate(mapiDefaultProviderSpec)
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
				Name:     awsMachineTemplateName,
			},
			"worker-user-data",
		))

		framework.WaitForMachineSet(ctx, cl, machineSet.Name, machineSet.Namespace, framework.WaitLong)

		compareInstances(awsClient, mapiDefaultMSName, "aws-machineset")
	})
})
