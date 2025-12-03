package e2e

import (
	"fmt"

	bmov1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	metal3v1 "github.com/metal3-io/cluster-api-provider-metal3/api/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	yaml "sigs.k8s.io/yaml"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	bmv1alpha1 "github.com/openshift/cluster-api-provider-baremetal/pkg/apis/baremetal/v1alpha1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
)

const (
	baremetalMachineTemplateName = "baremetal-machine-template"
)

var _ = Describe("Cluster API Baremetal MachineSet", Ordered, func() {
	var baremetalMachineTemplate *metal3v1.Metal3MachineTemplate
	var machineSet *clusterv1beta1.MachineSet
	var mapiMachineSpec *bmv1alpha1.BareMetalMachineProviderSpec

	BeforeAll(func() {
		if platform != configv1.BareMetalPlatformType {
			Skip("Skipping Baremetal E2E tests")
		}
		mapiMachineSpec = getBaremetalMAPIProviderSpec(cl)
	})

	AfterEach(func() {
		if platform != configv1.BareMetalPlatformType {
			// Because AfterEach always runs, even when tests are skipped, we have to
			// explicitly skip it here for other platforms.
			Skip("Skipping Baremetal E2E tests")
		}
		framework.DeleteMachineSets(ctx, cl, machineSet)
		framework.WaitForMachineSetsDeleted(cl, machineSet)
		framework.DeleteObjects(ctx, cl, baremetalMachineTemplate)
	})

	It("should be able to run a machine", func() {
		key := client.ObjectKey{
			Namespace: "openshift-cluster-api",
			Name:      "ostest-extraworker-0", // name provided by dev-scripts in CI
		}

		waitForBaremetalHostState(cl, key, bmov1alpha1.StateAvailable)

		baremetalMachineTemplate = createBaremetalMachineTemplate(cl, mapiMachineSpec)

		machineSet = framework.CreateMachineSet(ctx, cl, framework.NewMachineSetParams(
			"baremetal-machineset",
			clusterName,
			"", // mapiMachineSpec.Zone,
			1,
			corev1.ObjectReference{
				Kind:       "Metal3MachineTemplate",
				APIVersion: infraAPIVersion,
				Name:       baremetalMachineTemplateName,
			},
			"worker-user-data-managed",
		))

		framework.WaitForMachineSet(cl, machineSet.Name, machineSet.Namespace)
	})
})

func waitForBaremetalHostState(cl client.Client, key client.ObjectKey, state bmov1alpha1.ProvisioningState) {
	By(fmt.Sprintf("waiting for baremetal host to become %s", state))

	Eventually(func() error {
		bmh := bmov1alpha1.BareMetalHost{}

		err := cl.Get(ctx, key, &bmh)
		if err != nil {
			return err
		}

		if bmh.Status.Provisioning.State != state {
			return fmt.Errorf("baremetalhost is not %s", state)
		}

		return nil
	}, framework.WaitOverLong, framework.RetryLong).Should(Succeed())

}

func getBaremetalMAPIProviderSpec(cl client.Client) *bmv1alpha1.BareMetalMachineProviderSpec {
	machineSetList := &mapiv1beta1.MachineSetList{}
	Expect(cl.List(ctx, machineSetList, client.InNamespace(framework.MAPINamespace))).To(Succeed())

	Expect(machineSetList.Items).ToNot(HaveLen(0))
	machineSet := machineSetList.Items[0]
	Expect(machineSet.Spec.Template.Spec.ProviderSpec.Value).ToNot(BeNil())

	providerSpec := &bmv1alpha1.BareMetalMachineProviderSpec{}
	Expect(yaml.Unmarshal(machineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, providerSpec)).To(Succeed())

	return providerSpec
}

func createBaremetalMachineTemplate(cl client.Client, mapiProviderSpec *bmv1alpha1.BareMetalMachineProviderSpec) *metal3v1.Metal3MachineTemplate {
	By("Creating Baremetal machine template")

	baremetalMachineSpec := metal3v1.Metal3MachineSpec{
		CustomDeploy: &metal3v1.CustomDeploy{
			Method: "install_coreos",
		},
		UserData: mapiProviderSpec.UserData,
	}

	baremetalMachineTemplate := &metal3v1.Metal3MachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      baremetalMachineTemplateName,
			Namespace: framework.CAPINamespace,
		},
		Spec: metal3v1.Metal3MachineTemplateSpec{
			Template: metal3v1.Metal3MachineTemplateResource{
				Spec: baremetalMachineSpec,
			},
		},
	}

	if err := cl.Create(ctx, baremetalMachineTemplate); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred())
	}

	return baremetalMachineTemplate
}
