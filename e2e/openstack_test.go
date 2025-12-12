package e2e

import (
	"context"

	"github.com/onsi/gomega/format"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1alpha1 "github.com/openshift/api/machine/v1alpha1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	yaml "sigs.k8s.io/yaml"

	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	openStackMachineTemplateName = "openstack-machine-template"
)

var _ = Describe("Cluster API OpenStack MachineSet", Ordered, func() {
	var (
		machineSet               *clusterv1.MachineSet
		mapiMachineSpec          *mapiv1alpha1.OpenstackProviderSpec
		openStackMachineTemplate *openstackv1.OpenStackMachineTemplate
	)

	BeforeAll(func() {
		if platform != configv1.OpenStackPlatformType {
			Skip("Skipping OpenStack E2E tests")
		}
		mapiMachineSpec = getOpenStackMAPIProviderSpec(cl)
	})

	AfterEach(func() {
		if platform != configv1.OpenStackPlatformType {
			// Because AfterEach always runs, even when tests are skipped, we have to
			// explicitly skip it here for other platforms.
			Skip("Skipping OpenStack E2E tests")
		}

		framework.DeleteMachineSets(ctx, cl, machineSet)
		framework.WaitForMachineSetsDeleted(cl, machineSet)
		framework.DeleteObjects(ctx, cl, openStackMachineTemplate)
	})

	It("should be able to run a machine with implicit cluster default network", func() {
		openStackMachineTemplate = createOpenStackMachineTemplate(ctx, cl, mapiMachineSpec)

		machineSet = framework.CreateMachineSet(ctx, cl, framework.NewMachineSetParams(
			"openstack-machineset",
			clusterName,
			"",
			1,
			clusterv1.ContractVersionedObjectReference{
				Kind:     "OpenStackMachineTemplate",
				APIGroup: infraAPIGroup,
				Name:     openStackMachineTemplate.Name,
			},
			"worker-user-data",
		))

		framework.WaitForMachineSet(cl, machineSet.Name, machineSet.Namespace)
	})
})

func getOpenStackMAPIProviderSpec(cl client.Client) *mapiv1alpha1.OpenstackProviderSpec {
	machineSetList := &mapiv1beta1.MachineSetList{}
	Eventually(cl.List(ctx, machineSetList, client.InNamespace(framework.MAPINamespace))).Should(Succeed())

	Expect(machineSetList.Items).ToNot(HaveLen(0), "No MachineSets found in namespace %s", framework.MAPINamespace)
	machineSet := machineSetList.Items[0]
	Expect(machineSet.Spec.Template.Spec.ProviderSpec.Value).ToNot(BeNil())

	providerSpec := &mapiv1alpha1.OpenstackProviderSpec{}
	Expect(yaml.Unmarshal(machineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, providerSpec)).To(Succeed())

	return providerSpec
}

func createOpenStackMachineTemplate(ctx context.Context, cl client.Client, mapiProviderSpec *mapiv1alpha1.OpenstackProviderSpec) *openstackv1.OpenStackMachineTemplate {
	By("Creating OpenStack machine template")

	Expect(mapiProviderSpec).ToNot(BeNil())
	Expect(mapiProviderSpec.Flavor).ToNot(BeEmpty())
	// NOTE(stephenfin): Installer does not populate ps.Image when ps.RootVolume is set and will
	// instead populate ps.RootVolume.SourceUUID. Moreover, according to the ClusterOSImage option
	// definition this is always the name of the image and never the UUID. We should allow UUID
	// at some point and this will need an update.
	if mapiProviderSpec.RootVolume != nil {
		Expect(mapiProviderSpec.RootVolume.SourceUUID).ToNot(BeEmpty())
	} else {
		Expect(mapiProviderSpec.Image).ToNot(BeEmpty())
	}
	Expect(len(mapiProviderSpec.Networks)).To(BeNumerically(">", 0))
	Expect(len(mapiProviderSpec.Networks[0].Subnets)).To(BeNumerically(">", 0))
	Expect(mapiProviderSpec.Tags).ToNot(BeNil())
	Expect(len(mapiProviderSpec.Tags)).To(BeNumerically(">", 0))

	var image openstackv1.ImageParam
	var rootVolume *openstackv1.RootVolume

	if mapiProviderSpec.RootVolume != nil {
		rootVolume = &openstackv1.RootVolume{
			SizeGiB: mapiProviderSpec.RootVolume.Size,
			BlockDeviceVolume: openstackv1.BlockDeviceVolume{
				Type: mapiProviderSpec.RootVolume.VolumeType,
				AvailabilityZone: &openstackv1.VolumeAvailabilityZone{
					From: openstackv1.VolumeAZFromName,
					Name: ptr.To(openstackv1.VolumeAZName(mapiProviderSpec.RootVolume.Zone)),
				},
			},
		}
		image.ID = ptr.To(mapiProviderSpec.RootVolume.SourceUUID)
	} else {
		image.Filter = &openstackv1.ImageFilter{Name: &mapiProviderSpec.Image}
	}

	// NOTE(stephenfin): We intentionally ignore additional security for now.
	var securityGroupParam openstackv1.SecurityGroupParam
	Expect(len(mapiProviderSpec.SecurityGroups)).To(BeNumerically(">", 0))
	securityGroup := mapiProviderSpec.SecurityGroups[0]
	if securityGroup.UUID != "" {
		securityGroupParam = openstackv1.SecurityGroupParam{ID: &securityGroup.UUID}
	} else {
		securityGroupParam = openstackv1.SecurityGroupParam{Filter: &openstackv1.SecurityGroupFilter{Name: securityGroup.Name}}
	}
	securityGroups := []openstackv1.SecurityGroupParam{
		securityGroupParam,
	}

	// We intentionally omit ports so the machine will default its network
	// from the OpenStackCluster created by the infracluster controller.
	openStackMachineSpec := openstackv1.OpenStackMachineSpec{
		Flavor: ptr.To(mapiProviderSpec.Flavor),
		IdentityRef: &openstackv1.OpenStackIdentityReference{
			CloudName: "openstack",
			Name:      "openstack-cloud-credentials",
		},
		Image:          image,
		RootVolume:     rootVolume,
		SecurityGroups: securityGroups,
	}

	openStackMachineTemplate := &openstackv1.OpenStackMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: openStackMachineTemplateName + "-",
			Namespace:    framework.CAPINamespace,
		},
		Spec: openstackv1.OpenStackMachineTemplateSpec{
			Template: openstackv1.OpenStackMachineTemplateResource{
				Spec: openStackMachineSpec,
			},
		},
	}

	Expect(cl.Create(ctx, openStackMachineTemplate)).To(Succeed(), format.Object(openStackMachineTemplate, 1))

	return openStackMachineTemplate
}
