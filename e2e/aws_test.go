package e2e

import (
	"context"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	yaml "sigs.k8s.io/yaml"
)

const (
	awsMachineTemplateName      = "aws-machine-template"
	machineSetOpenshiftLabelKey = "machine.openshift.io/cluster-api-machineset"
)

var _ = Describe("Cluster API AWS MachineSet", Ordered, func() {
	var (
		awsMachineTemplate      *awsv1.AWSMachineTemplate
		machineSet              *clusterv1.MachineSet
		mapiDefaultMS           *mapiv1.MachineSet
		mapiDefaultProviderSpec *mapiv1.AWSMachineProviderConfig
		awsClient               *ec2.EC2
	)

	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip("Skipping AWS E2E tests")
		}
		mapiDefaultMS, mapiDefaultProviderSpec = getDefaultAWSMAPIProviderSpec(cl)
		awsClient = createAWSClient(mapiDefaultProviderSpec.Placement.Region)
	})

	AfterEach(func() {
		if platform != configv1.AWSPlatformType {
			// Because AfterEach always runs, even when tests are skipped, we have to
			// explicitly skip it here for other platforms.
			Skip("Skipping AWS E2E tests")
		}
		framework.DeleteMachineSets(cl, machineSet)
		framework.WaitForMachineSetsDeleted(cl, machineSet)
		framework.DeleteObjects(cl, awsMachineTemplate)
	})

	It("should be able to run a machine with a default provider spec", func() {
		awsMachineTemplate = newAWSMachineTemplate(mapiDefaultProviderSpec)
		if err := cl.Create(ctx, awsMachineTemplate); err != nil && !apierrors.IsAlreadyExists(err) {
			Expect(err).ToNot(HaveOccurred())
		}

		machineSet = framework.CreateMachineSet(cl, framework.NewMachineSetParams(
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

		framework.WaitForMachineSet(cl, machineSet.Name)

		compareInstances(awsClient, mapiDefaultMS.Name, "aws-machineset")
	})
})

func getDefaultAWSMAPIProviderSpec(cl client.Client) (*mapiv1.MachineSet, *mapiv1.AWSMachineProviderConfig) {
	machineSetList := &mapiv1.MachineSetList{}
	Expect(cl.List(ctx, machineSetList, client.InNamespace(framework.MAPINamespace))).To(Succeed())

	Expect(machineSetList.Items).ToNot(HaveLen(0))
	machineSet := &machineSetList.Items[0]
	Expect(machineSet.Spec.Template.Spec.ProviderSpec.Value).ToNot(BeNil())

	providerSpec := &mapiv1.AWSMachineProviderConfig{}
	Expect(yaml.Unmarshal(machineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, providerSpec)).To(Succeed())

	return machineSet, providerSpec
}

func newAWSMachineTemplate(mapiProviderSpec *mapiv1.AWSMachineProviderConfig) *awsv1.AWSMachineTemplate {
	By("Creating AWS machine template")

	Expect(mapiProviderSpec).ToNot(BeNil())
	Expect(mapiProviderSpec.IAMInstanceProfile).ToNot(BeNil())
	Expect(mapiProviderSpec.IAMInstanceProfile.ID).ToNot(BeNil())
	Expect(mapiProviderSpec.InstanceType).ToNot(BeEmpty())
	Expect(mapiProviderSpec.Placement.AvailabilityZone).ToNot(BeEmpty())
	Expect(mapiProviderSpec.AMI.ID).ToNot(BeNil())
	Expect(mapiProviderSpec.Subnet.Filters).ToNot(HaveLen(0))
	Expect(mapiProviderSpec.Subnet.Filters[0].Values).ToNot(HaveLen(0))
	Expect(mapiProviderSpec.SecurityGroups).ToNot(HaveLen(0))
	Expect(mapiProviderSpec.SecurityGroups[0].Filters).ToNot(HaveLen(0))
	Expect(mapiProviderSpec.SecurityGroups[0].Filters[0].Values).ToNot(HaveLen(0))

	uncompressedUserData := true

	awsMachineSpec := awsv1.AWSMachineSpec{
		UncompressedUserData: &uncompressedUserData,
		IAMInstanceProfile:   *mapiProviderSpec.IAMInstanceProfile.ID,
		InstanceType:         mapiProviderSpec.InstanceType,
		AMI: awsv1.AMIReference{
			ID: mapiProviderSpec.AMI.ID,
		},
		Ignition: &awsv1.Ignition{
			Version:     "3.4",
			StorageType: awsv1.IgnitionStorageTypeOptionUnencryptedUserData,
		},
		Subnet: &awsv1.AWSResourceReference{
			Filters: []awsv1.Filter{
				{
					Name:   "tag:Name",
					Values: mapiProviderSpec.Subnet.Filters[0].Values,
				},
			},
		},
		AdditionalSecurityGroups: []awsv1.AWSResourceReference{
			{
				Filters: []awsv1.Filter{
					{
						Name:   "tag:Name",
						Values: mapiProviderSpec.SecurityGroups[0].Filters[0].Values,
					},
				},
			},
		},
	}

	awsMachineTemplate := &awsv1.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      awsMachineTemplateName,
			Namespace: framework.CAPINamespace,
		},
		Spec: awsv1.AWSMachineTemplateSpec{
			Template: awsv1.AWSMachineTemplateResource{
				Spec: awsMachineSpec,
			},
		},
	}

	return awsMachineTemplate
}

func createAWSClient(region string) *ec2.EC2 {
	var secret corev1.Secret
	Expect(cl.Get(context.Background(), client.ObjectKey{
		Namespace: framework.CAPINamespace,
		Name:      "capa-manager-bootstrap-credentials",
	}, &secret)).To(Succeed())

	accessKey := secret.Data["aws_access_key_id"]
	Expect(accessKey).ToNot(BeNil())
	secretAccessKey := secret.Data["aws_secret_access_key"]
	Expect(secretAccessKey).ToNot(BeNil())

	awsConfig := &aws.Config{
		Region: aws.String(region),
		Credentials: credentials.NewStaticCredentials(
			string(accessKey),
			string(secretAccessKey),
			"",
		),
	}

	sess, err := session.NewSession(awsConfig)
	Expect(err).ToNot(HaveOccurred())

	return ec2.New(sess)
}

func getMAPICreatedInstance(awsClient *ec2.EC2, msName string) ec2.Instance {
	Expect(awsClient).ToNot(BeNil())
	Expect(msName).ToNot(BeEmpty())
	mapiMachineList := &mapiv1.MachineList{}
	Expect(cl.List(ctx, mapiMachineList, client.InNamespace(framework.MAPINamespace), client.MatchingLabels{
		machineSetOpenshiftLabelKey: msName,
	})).To(Succeed())
	Expect(len(mapiMachineList.Items)).To(BeNumerically(">", 0))

	mapiMachine := mapiMachineList.Items[0]
	Expect(mapiMachine.Status.ProviderStatus).ToNot(BeNil())

	mapiProviderStatus := &mapiv1.AWSMachineProviderStatus{}
	Expect(yaml.Unmarshal(mapiMachine.Status.ProviderStatus.Raw, mapiProviderStatus)).To(Succeed())

	Expect(mapiProviderStatus.InstanceID).ToNot(BeNil())
	Expect(*mapiProviderStatus.InstanceID).ToNot(BeEmpty())

	request := &ec2.DescribeInstancesInput{
		InstanceIds: aws.StringSlice([]string{*mapiProviderStatus.InstanceID}),
	}

	result, err := awsClient.DescribeInstances(request)
	Expect(err).ToNot(HaveOccurred())

	Expect(result.Reservations).To(HaveLen(1))
	Expect(result.Reservations[0].Instances).To(HaveLen(1))

	return *result.Reservations[0].Instances[0]
}

func getCAPICreatedInstance(awsClient *ec2.EC2, msName string) ec2.Instance {
	Expect(awsClient).ToNot(BeNil())
	Expect(msName).ToNot(BeEmpty())
	capiMachineList := &awsv1.AWSMachineList{}

	Expect(cl.List(ctx, capiMachineList, client.InNamespace(framework.CAPINamespace), client.MatchingLabels{
		machineSetOpenshiftLabelKey: msName,
	})).To(Succeed())
	Expect(capiMachineList.Items).To(HaveLen(1))

	capiMachine := capiMachineList.Items[0]
	Expect(capiMachine.Status).ToNot(BeNil())

	request := &ec2.DescribeInstancesInput{
		InstanceIds: aws.StringSlice([]string{*capiMachine.Spec.InstanceID}),
	}

	result, err := awsClient.DescribeInstances(request)
	Expect(err).ToNot(HaveOccurred())

	Expect(result.Reservations).To(HaveLen(1))
	Expect(result.Reservations[0].Instances).To(HaveLen(1))

	return *result.Reservations[0].Instances[0]
}

func compareInstances(awsClient *ec2.EC2, mapiMsName, capiMsName string) {
	By("Comparing instances created by MAPI and CAPI")
	mapiEC2Instance := getMAPICreatedInstance(awsClient, mapiMsName)
	capiEC2Instance := getCAPICreatedInstance(awsClient, capiMsName)

	// Ignore fields that are unique for each instance
	ignoreInstanceFields := cmpopts.IgnoreFields(ec2.Instance{},
		"InstanceId",
		"ClientToken",
		"LaunchTime",
		"PrivateDnsName",
		"PrivateIpAddress",
		"UsageOperationUpdateTime",
		"Tags", // tags won't match we should write a set of tests for comparing them manually
	)

	ignoreBlockDeviceFields := cmpopts.IgnoreFields(ec2.InstanceBlockDeviceMapping{},
		"Ebs.AttachTime",
		"Ebs.VolumeId",
	)

	ignoreNicFields := cmpopts.IgnoreFields(ec2.InstanceNetworkInterface{},
		"Attachment.AttachTime",
		"Attachment.AttachmentId",
		"MacAddress",
		"NetworkInterfaceId",
		"PrivateDnsName",
		"PrivateIpAddress",
	)

	ignorePrivateIpFields := cmpopts.IgnoreFields(ec2.InstancePrivateIpAddress{},
		"PrivateDnsName",
		"PrivateIpAddress",
	)

	// Tags won't match we should write a set of tests for comparing them manually
	ignoreTags := cmpopts.IgnoreTypes(ec2.Tag{})

	cmpOpts := []cmp.Option{
		ignoreInstanceFields,
		ignoreBlockDeviceFields,
		ignoreNicFields,
		ignorePrivateIpFields,
		ignoreTags,
	}

	if !cmp.Equal(mapiEC2Instance, capiEC2Instance, cmpOpts...) {
		GinkgoWriter.Print("Instances created by MAPI and CAPI are not equal\n" + cmp.Diff(mapiEC2Instance, capiEC2Instance, cmpOpts...))
	}
}
