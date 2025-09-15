package e2e

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	yaml "sigs.k8s.io/yaml"
)

const (
	awsMachineTemplateName      = "aws-machine-template"
	machineSetOpenshiftLabelKey = "machine.openshift.io/cluster-api-machineset"
)

func getDefaultAWSMAPIProviderSpec() (*mapiv1beta1.MachineSet, *mapiv1beta1.AWSMachineProviderConfig) {
	machineSetList := &mapiv1beta1.MachineSetList{}
	Eventually(komega.List(machineSetList, client.InNamespace(framework.MAPINamespace))).Should(Succeed(), "Failed to list MachineSets in MAPI namespace")

	Expect(machineSetList.Items).ToNot(BeNil())
	Expect(machineSetList.Items).ToNot(HaveLen(0), "No MachineSets found in namespace %s", framework.MAPINamespace)
	machineSet := &machineSetList.Items[0]
	Expect(machineSet.Spec.Template.Spec.ProviderSpec.Value).ToNot(BeNil())

	providerSpec := &mapiv1beta1.AWSMachineProviderConfig{}
	Expect(yaml.Unmarshal(machineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, providerSpec)).To(Succeed())

	return machineSet, providerSpec
}

func newAWSMachineTemplate(mapiProviderSpec *mapiv1beta1.AWSMachineProviderConfig) *awsv1.AWSMachineTemplate {
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

	awsMachineSpec := awsv1.AWSMachineSpec{
		IAMInstanceProfile: *mapiProviderSpec.IAMInstanceProfile.ID,
		InstanceType:       mapiProviderSpec.InstanceType,
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
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "capa-manager-bootstrap-credentials",
			Namespace: framework.CAPINamespace,
		},
	}
	Eventually(komega.Get(secret)).Should(Succeed(), "Failed to get AWS credentials secret")

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
	mapiMachineList := &mapiv1beta1.MachineList{}
	Eventually(komega.ObjectList(mapiMachineList, client.InNamespace(framework.MAPINamespace), client.MatchingLabels{
		machineSetOpenshiftLabelKey: msName,
	})).Should(HaveField("Items", Not(BeEmpty())), "Failed to find MAPI machines for MachineSet %s", msName)

	mapiMachine := mapiMachineList.Items[0]
	Expect(mapiMachine.Status.ProviderStatus).ToNot(BeNil())

	mapiProviderStatus := &mapiv1beta1.AWSMachineProviderStatus{}
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

	Eventually(komega.ObjectList(capiMachineList, client.InNamespace(framework.CAPINamespace), client.MatchingLabels{
		machineSetOpenshiftLabelKey: msName,
	})).Should(HaveField("Items", HaveLen(1)), "Failed to find exactly one CAPI AWSMachine for MachineSet %s", msName)

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
