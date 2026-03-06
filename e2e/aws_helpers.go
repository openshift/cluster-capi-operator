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
	"context"
	"fmt"
	"strings"
	"time"

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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// newAWSMachineTemplate creates an AWSMachineTemplate from a MAPI AWS providerSpec by converting relevant fields.
func newAWSMachineTemplate(mapiProviderSpec *mapiv1beta1.AWSMachineProviderConfig) *awsv1.AWSMachineTemplate {
	GinkgoHelper()
	By("Creating AWS machine template")

	Expect(mapiProviderSpec).ToNot(BeNil(), "expected MAPI ProviderSpec to not be nil")
	Expect(mapiProviderSpec.IAMInstanceProfile).ToNot(BeNil(), "expected IAMInstanceProfile to not be nil")
	Expect(mapiProviderSpec.IAMInstanceProfile.ID).ToNot(BeNil(), "expected IAMInstanceProfile ID to not be nil")
	Expect(mapiProviderSpec.InstanceType).ToNot(BeEmpty(), "expected InstanceType to not be empty")
	Expect(mapiProviderSpec.Placement.AvailabilityZone).ToNot(BeEmpty(), "expected AvailabilityZone to not be empty")
	Expect(mapiProviderSpec.AMI.ID).ToNot(BeNil(), "expected AMI ID to not be nil")
	Expect(mapiProviderSpec.Subnet.Filters).ToNot(BeEmpty(), "expected Subnet Filters to not be empty")
	Expect(mapiProviderSpec.Subnet.Filters[0].Values).ToNot(BeEmpty(), "expected Subnet Filter values to not be empty")
	Expect(mapiProviderSpec.SecurityGroups).ToNot(BeEmpty(), "expected SecurityGroups to not be empty")
	Expect(mapiProviderSpec.SecurityGroups[0].Filters).ToNot(BeEmpty(), "expected SecurityGroup Filters to not be empty")
	Expect(mapiProviderSpec.SecurityGroups[0].Filters[0].Values).ToNot(BeEmpty(), "expected SecurityGroup Filter values to not be empty")

	awsMachineSpec := awsv1.AWSMachineSpec{
		IAMInstanceProfile: *mapiProviderSpec.IAMInstanceProfile.ID,
		InstanceType:       mapiProviderSpec.InstanceType,
		AMI: awsv1.AMIReference{
			ID: mapiProviderSpec.AMI.ID,
		},
		Ignition: &awsv1.Ignition{
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

// createAWSClient creates an AWS EC2 client using credentials from the CAPI bootstrap credentials secret.
func createAWSClient(region string) *ec2.EC2 {
	GinkgoHelper()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "capa-manager-bootstrap-credentials",
			Namespace: framework.CAPINamespace,
		},
	}
	Eventually(komega.Get(secret)).Should(Succeed(), "Failed to get AWS credentials secret")

	accessKey := secret.Data["aws_access_key_id"]
	Expect(accessKey).ToNot(BeNil(), "expected aws_access_key_id to be present in credentials secret")

	secretAccessKey := secret.Data["aws_secret_access_key"]
	Expect(secretAccessKey).ToNot(BeNil(), "expected aws_secret_access_key to be present in credentials secret")

	awsConfig := &aws.Config{
		Region: aws.String(region),
		Credentials: credentials.NewStaticCredentials(
			string(accessKey),
			string(secretAccessKey),
			"",
		),
	}

	sess, err := session.NewSession(awsConfig)
	Expect(err).ToNot(HaveOccurred(), "should not fail creating AWS session")

	return ec2.New(sess)
}

// getMAPICreatedInstance retrieves the EC2 instance created by a MAPI MachineSet using the AWS API.
func getMAPICreatedInstance(awsClient *ec2.EC2, msName string) ec2.Instance {
	GinkgoHelper()
	Expect(awsClient).ToNot(BeNil())
	Expect(msName).ToNot(BeEmpty())

	mapiMachineList := &mapiv1beta1.MachineList{}
	Eventually(komega.ObjectList(mapiMachineList, client.InNamespace(framework.MAPINamespace), client.MatchingLabels{
		machineSetOpenshiftLabelKey: msName,
	})).Should(HaveField("Items", Not(BeEmpty())), "Failed to find MAPI machines for MachineSet %s", msName)

	framework.SortListByName(mapiMachineList)
	mapiMachine := mapiMachineList.Items[0]
	Expect(mapiMachine.Status.ProviderStatus).ToNot(BeNil(), "expected MAPI Machine ProviderStatus to not be nil")

	mapiProviderStatus := &mapiv1beta1.AWSMachineProviderStatus{}
	Expect(yaml.Unmarshal(mapiMachine.Status.ProviderStatus.Raw, mapiProviderStatus)).To(Succeed(),
		"should not fail YAML decoding MAPI Machine provider status")

	Expect(mapiProviderStatus.InstanceID).ToNot(BeNil(), "expected MAPI Machine InstanceID to not be nil")
	Expect(*mapiProviderStatus.InstanceID).ToNot(BeEmpty(), "expected MAPI Machine InstanceID to not be empty")

	request := &ec2.DescribeInstancesInput{
		InstanceIds: aws.StringSlice([]string{*mapiProviderStatus.InstanceID}),
	}

	result, err := awsClient.DescribeInstances(request)
	Expect(err).ToNot(HaveOccurred(), "should not fail describing EC2 instance")

	Expect(result.Reservations).To(HaveLen(1), "expected exactly one reservation")
	Expect(result.Reservations[0].Instances).To(HaveLen(1), "expected exactly one instance in reservation")

	return *result.Reservations[0].Instances[0]
}

// getCAPICreatedInstance retrieves the EC2 instance created by a CAPI MachineSet using the AWS API.
func getCAPICreatedInstance(awsClient *ec2.EC2, msName string) ec2.Instance {
	GinkgoHelper()
	Expect(awsClient).ToNot(BeNil())
	Expect(msName).ToNot(BeEmpty())

	capiMachineList := &awsv1.AWSMachineList{}

	Eventually(komega.ObjectList(capiMachineList, client.InNamespace(framework.CAPINamespace), client.MatchingLabels{
		machineSetOpenshiftLabelKey: msName,
	})).Should(HaveField("Items", HaveLen(1)), "Failed to find exactly one CAPI AWSMachine for MachineSet %s", msName)

	capiMachine := capiMachineList.Items[0]
	Expect(capiMachine.Spec.InstanceID).ToNot(BeNil(), "AWSMachine InstanceID not set in Spec")
	Expect(*capiMachine.Spec.InstanceID).ToNot(BeEmpty(), "AWSMachine InstanceID is empty")

	request := &ec2.DescribeInstancesInput{
		InstanceIds: aws.StringSlice([]string{*capiMachine.Spec.InstanceID}),
	}

	result, err := awsClient.DescribeInstances(request)
	Expect(err).ToNot(HaveOccurred(), "should not fail describing EC2 instance")

	Expect(result.Reservations).To(HaveLen(1), "expected exactly one reservation")
	Expect(result.Reservations[0].Instances).To(HaveLen(1), "expected exactly one instance in reservation")

	return *result.Reservations[0].Instances[0]
}

// compareInstances compares EC2 instances created by MAPI and CAPI MachineSets, logging any differences while ignoring instance-specific fields.
func compareInstances(awsClient *ec2.EC2, mapiMsName, capiMsName string) {
	GinkgoHelper()
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

	ignoreNicFields := cmpopts.IgnoreFields(ec2.InstanceNetworkInterface{},
		"MacAddress",
		"NetworkInterfaceId",
		"PrivateDnsName",
		"PrivateIpAddress",
	)
	ignorePrivateIpFields := cmpopts.IgnoreFields(ec2.InstancePrivateIpAddress{},
		"PrivateDnsName",
		"PrivateIpAddress",
	)
	// Ignore variable fields on nested types explicitly
	ignoreEbsFields := cmpopts.IgnoreFields(ec2.EbsInstanceBlockDevice{},
		"AttachTime",
		"VolumeId",
	)
	ignoreNicAttachmentFields := cmpopts.IgnoreFields(ec2.NetworkInterfaceAttachment{},
		"AttachTime",
		"AttachmentId",
	)
	// Tags won't match we should write a set of tests for comparing them manually
	ignoreTags := cmpopts.IgnoreTypes(ec2.Tag{})
	cmpOpts := []cmp.Option{
		ignoreInstanceFields,
		ignoreEbsFields,
		ignoreNicFields,
		ignoreNicAttachmentFields,
		ignorePrivateIpFields,
		ignoreTags,
		cmpopts.EquateEmpty(),
	}

	if !cmp.Equal(mapiEC2Instance, capiEC2Instance, cmpOpts...) {
		GinkgoWriter.Print("Instances created by MAPI and CAPI are not equal\n" + cmp.Diff(mapiEC2Instance, capiEC2Instance, cmpOpts...))
	}
}

// deleteAWSMachineTemplates deletes the specified AWSMachineTemplates.
func deleteAWSMachineTemplates(ctx context.Context, cl client.Client, templates ...*awsv1.AWSMachineTemplate) {
	GinkgoHelper()

	for _, template := range templates {
		if template == nil {
			continue
		}

		By(fmt.Sprintf("Deleting awsMachineTemplate %q", template.GetName()))
		Eventually(func() error {
			return cl.Delete(ctx, template)
		}, time.Minute, framework.RetryShort).Should(SatisfyAny(
			Succeed(),
			WithTransform(apierrors.IsNotFound, BeTrue()),
		), "Delete awsMachineTemplate %s/%s should succeed, or awsMachineTemplate should not be found.",
			template.Namespace, template.Name)
	}
}

// getAWSMachineTemplateByPrefix gets an AWSMachineTemplate by name prefix.
func getAWSMachineTemplateByPrefix(prefix string, namespace string) (*awsv1.AWSMachineTemplate, error) {
	if prefix == "" {
		return nil, fmt.Errorf("prefix cannot be empty")
	}

	templateList := &awsv1.AWSMachineTemplateList{}
	if err := komega.List(templateList, client.InNamespace(namespace))(); err != nil {
		return nil, fmt.Errorf("list AWSMachineTemplates in namespace %s: %w", namespace, err)
	}

	var matches []*awsv1.AWSMachineTemplate

	for i, t := range templateList.Items {
		if strings.HasPrefix(t.Name, prefix) {
			matches = append(matches, &templateList.Items[i])
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no AWSMachineTemplate found with prefix %q", prefix)
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("multiple AWSMachineTemplates found with prefix %q (%d matches)", prefix, len(matches))
	}
}
