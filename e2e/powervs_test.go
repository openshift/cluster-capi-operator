package e2e

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	powerVSMachineTemplateName    = "powervs-machine-template"
	powerVSMachineTemplateVersion = "infrastructure.cluster.x-k8s.io/v1beta2"
)

var _ = Describe("Cluster API IBMPowerVS MachineSet", Ordered, func() {
	var powerVSMachineTemplate *ibmpowervsv1.IBMPowerVSMachineTemplate
	var machineSet *clusterv1.MachineSet
	var mapiMachineSpec *mapiv1.PowerVSMachineProviderConfig

	BeforeAll(func() {
		if platform != configv1.PowerVSPlatformType {
			Skip("Skipping PowerVS E2E tests")
		}
		mapiMachineSpec = getPowerVSMAPIProviderSpec(cl)
	})

	AfterEach(func() {
		if platform != configv1.PowerVSPlatformType {
			// Because AfterEach always runs, even when tests are skipped, we have to
			// explicitly skip it here for other platforms.
			Skip("Skipping PowerVS E2E tests")
		}
		framework.DeleteMachineSets(cl, machineSet)
		framework.WaitForMachineSetsDeleted(cl, machineSet)
		framework.DeleteObjects(cl, powerVSMachineTemplate)
	})

	It("should be able to run a machine", func() {
		powerVSMachineTemplate = createIBMPowerVSMachineTemplate(cl, mapiMachineSpec)

		machineSet = framework.CreateMachineSet(cl, framework.NewMachineSetParams(
			"ibmpowervs-machineset",
			clusterName,
			"",
			1,
			corev1.ObjectReference{
				Kind:       "IBMPowerVSMachineTemplate",
				APIVersion: powerVSMachineTemplateVersion,
				Name:       powerVSMachineTemplateName,
			},
			"worker-user-data",
		))
		framework.WaitForMachineSet(cl, machineSet.Name, machineSet.Namespace)
	})

})

func getPowerVSMAPIProviderSpec(cl client.Client) *mapiv1.PowerVSMachineProviderConfig {
	machineSetList := &mapiv1beta1.MachineSetList{}
	Expect(cl.List(ctx, machineSetList, client.InNamespace(framework.MAPINamespace))).To(Succeed())

	Expect(machineSetList.Items).ToNot(HaveLen(0))
	machineSet := machineSetList.Items[0]
	Expect(machineSet.Spec.Template.Spec.ProviderSpec.Value).ToNot(BeNil())

	providerSpec := &mapiv1.PowerVSMachineProviderConfig{}
	Expect(yaml.Unmarshal(machineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, providerSpec)).To(Succeed())

	return providerSpec
}

func createIBMPowerVSMachineTemplate(cl client.Client, mapiProviderSpec *mapiv1.PowerVSMachineProviderConfig) *ibmpowervsv1.IBMPowerVSMachineTemplate {
	By("Creating IBMPowerVS machine template")

	Expect(mapiProviderSpec).ToNot(BeNil())
	Expect(mapiProviderSpec.ServiceInstance).ToNot(BeNil())
	Expect(mapiProviderSpec.KeyPairName).ToNot(BeEmpty())
	Expect(mapiProviderSpec.Image).ToNot(BeNil())
	Expect(mapiProviderSpec.SystemType).ToNot(BeEmpty())
	Expect(mapiProviderSpec.ProcessorType).ToNot(BeEmpty())

	ibmPowerVSMachineSpec := ibmpowervsv1.IBMPowerVSMachineSpec{
		ServiceInstance: getServiceInstance(mapiProviderSpec.ServiceInstance),
		SSHKey:          mapiProviderSpec.KeyPairName,
		Image: &ibmpowervsv1.IBMPowerVSResourceReference{
			Name: mapiProviderSpec.Image.Name,
		},
		SystemType:    mapiProviderSpec.SystemType,
		ProcessorType: ibmpowervsv1.PowerVSProcessorType(mapiProviderSpec.ProcessorType),
		Processors:    mapiProviderSpec.Processors,
		MemoryGiB:     mapiProviderSpec.MemoryGiB,
		Network:       getNetworkResourceReference(mapiProviderSpec.Network),
	}

	ibmPowerVSMachineTemplate := &ibmpowervsv1.IBMPowerVSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      powerVSMachineTemplateName,
			Namespace: framework.CAPINamespace,
		},
		Spec: ibmpowervsv1.IBMPowerVSMachineTemplateSpec{
			Template: ibmpowervsv1.IBMPowerVSMachineTemplateResource{
				Spec: ibmPowerVSMachineSpec,
			},
		},
	}

	if err := cl.Create(ctx, ibmPowerVSMachineTemplate); err != nil && !apierrors.IsAlreadyExists(err) {
		fmt.Println(err)
		Expect(err).ToNot(HaveOccurred())
	}

	return ibmPowerVSMachineTemplate
}

func getNetworkResourceReference(networkResource mapiv1.PowerVSResource) ibmpowervsv1.IBMPowerVSResourceReference {
	switch networkResource.Type {
	case mapiv1.PowerVSResourceTypeID:
		if networkResource.ID == nil {
			panic("networkResource reference is specified as ID but it is nil")
		}
		return ibmpowervsv1.IBMPowerVSResourceReference{
			ID: networkResource.ID,
		}
	case mapiv1.PowerVSResourceTypeName:
		if networkResource.Name == nil {
			panic("networkResource reference is specified as Name but it is nil")
		}
		return ibmpowervsv1.IBMPowerVSResourceReference{
			Name: networkResource.Name,
		}
	case mapiv1.PowerVSResourceTypeRegEx:
		if networkResource.RegEx == nil {
			panic("networkResource reference is specified as RegEx but it is nil")
		}
		return ibmpowervsv1.IBMPowerVSResourceReference{
			RegEx: networkResource.RegEx,
		}
	default:
		panic("networkResource reference is not specified")
	}
}

func getServiceInstance(serviceInstance mapiv1.PowerVSResource) *ibmpowervsv1.IBMPowerVSResourceReference {
	switch serviceInstance.Type {
	case mapiv1.PowerVSResourceTypeID:
		return &ibmpowervsv1.IBMPowerVSResourceReference{ID: serviceInstance.ID}
	case mapiv1.PowerVSResourceTypeName:
		return &ibmpowervsv1.IBMPowerVSResourceReference{Name: serviceInstance.Name}
	case mapiv1.PowerVSResourceTypeRegEx:
		return &ibmpowervsv1.IBMPowerVSResourceReference{RegEx: serviceInstance.RegEx}
	default:
		panic("unknown type for service instance")
	}
}
