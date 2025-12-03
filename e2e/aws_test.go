package e2e

import (
	"github.com/aws/aws-sdk-go/service/ec2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var _ = Describe("Cluster API AWS MachineSet", Ordered, func() {
	var (
		awsMachineTemplate      *awsv1.AWSMachineTemplate
		machineSet              *clusterv1beta1.MachineSet
		mapiDefaultMS           *mapiv1beta1.MachineSet
		mapiDefaultProviderSpec *mapiv1beta1.AWSMachineProviderConfig
		awsClient               *ec2.EC2
	)

	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip("Skipping AWS E2E tests")
		}
		mapiDefaultMS, mapiDefaultProviderSpec = getDefaultAWSMAPIProviderSpec()
		awsClient = createAWSClient(mapiDefaultProviderSpec.Placement.Region)
	})

	AfterEach(func() {
		if platform != configv1.AWSPlatformType {
			// Because AfterEach always runs, even when tests are skipped, we have to
			// explicitly skip it here for other platforms.
			Skip("Skipping AWS E2E tests")
		}
		framework.DeleteMachineSets(ctx, cl, machineSet)
		framework.WaitForMachineSetsDeleted(cl, machineSet)
		framework.DeleteObjects(ctx, cl, awsMachineTemplate)
	})

	It("should be able to run a machine with a default provider spec", func() {
		awsMachineTemplate = newAWSMachineTemplate(mapiDefaultProviderSpec)
		if err := cl.Create(ctx, awsMachineTemplate); err != nil && !apierrors.IsAlreadyExists(err) {
			Expect(err).ToNot(HaveOccurred())
		}

		machineSet = framework.CreateMachineSet(ctx, cl, framework.NewMachineSetParams(
			"aws-machineset",
			clusterName,
			"",
			1,
			corev1.ObjectReference{
				Kind:       "AWSMachineTemplate",
				APIVersion: infraAPIVersion,
				Name:       awsMachineTemplateName,
			},
			"worker-user-data",
		))

		framework.WaitForMachineSet(cl, machineSet.Name, machineSet.Namespace)

		compareInstances(awsClient, mapiDefaultMS.Name, "aws-machineset")
	})
})
