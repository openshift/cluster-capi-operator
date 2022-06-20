package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	yaml "sigs.k8s.io/yaml"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
)

const (
	awsMachineTemplateName = "aws-machine-template"
)

var _ = Describe("Cluster API AWS MachineSet", func() {
	var awsMachineTemplate *awsv1.AWSMachineTemplate
	var machineSet *clusterv1.MachineSet

	BeforeEach(func() {
		if platform != configv1.AWSPlatformType {
			Skip("Skipping AWS E2E tests")
		}

		framework.CreateCoreCluster(cl, clusterName, "AWSCluster")
		createAWSCluster(cl, getAWSMAPIProviderSpec(cl))
	})

	AfterEach(func() {
		framework.DeleteMachineSets(cl, machineSet)
		framework.WaitForMachineSetsDeleted(cl, machineSet)
		framework.DeleteObjects(cl, awsMachineTemplate)
	})

	It("should be able to run a machine", func() {
		awsMachineTemplate = createAWSMachineTemplate(cl, getAWSMAPIProviderSpec(cl))

		machineSet = framework.CreateMachineSet(cl, framework.NewMachineSetParams(
			"aws-machineset",
			clusterName,
			1,
			corev1.ObjectReference{
				Kind:       "AWSMachineTemplate",
				APIVersion: infraAPIVersion,
				Name:       awsMachineTemplateName,
			},
		))

		framework.WaitForMachineSet(cl, machineSet.Name)
	})
})

func getAWSMAPIProviderSpec(cl client.Client) *mapiv1.AWSMachineProviderConfig {
	machineSetList := &mapiv1.MachineSetList{}
	Expect(cl.List(ctx, machineSetList, client.InNamespace(framework.MAPINamespace))).To(Succeed())

	Expect(machineSetList.Items).ToNot(HaveLen(0))
	machineSet := machineSetList.Items[0]
	Expect(machineSet.Spec.Template.Spec.ProviderSpec.Value).ToNot(BeNil())

	providerSpec := &mapiv1.AWSMachineProviderConfig{}
	Expect(yaml.Unmarshal(machineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, providerSpec)).To(Succeed())

	return providerSpec
}

func createAWSCluster(cl client.Client, mapiProviderSpec *mapiv1.AWSMachineProviderConfig) *awsv1.AWSCluster {
	By("Creating AWS cluster")

	awsCluster := &awsv1.AWSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: framework.CAPINamespace,
		},
		Spec: awsv1.AWSClusterSpec{
			Region: mapiProviderSpec.Placement.Region,
		},
	}

	if err := cl.Create(ctx, awsCluster); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred())
	}

	Eventually(func() (bool, error) {
		patchedAWSCluster := &awsv1.AWSCluster{}
		err := cl.Get(ctx, client.ObjectKeyFromObject(awsCluster), patchedAWSCluster)
		if err != nil {
			return false, err
		}

		if patchedAWSCluster.Annotations == nil {
			return false, nil
		}

		if _, ok := patchedAWSCluster.Annotations[clusterv1.ManagedByAnnotation]; !ok {
			return false, nil
		}

		return patchedAWSCluster.Status.Ready, nil
	}, framework.WaitShort).Should(BeTrue())

	return awsCluster
}

func createAWSMachineTemplate(cl client.Client, mapiProviderSpec *mapiv1.AWSMachineProviderConfig) *awsv1.AWSMachineTemplate {
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
		FailureDomain:        &mapiProviderSpec.Placement.AvailabilityZone,
		CloudInit: awsv1.CloudInit{
			InsecureSkipSecretsManager: true,
		},
		AMI: awsv1.AMIReference{
			ID: mapiProviderSpec.AMI.ID,
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

	Expect(cl.Create(ctx, awsMachineTemplate)).To(Succeed())

	return awsMachineTemplate
}
