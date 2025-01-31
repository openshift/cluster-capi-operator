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
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	yaml "sigs.k8s.io/yaml"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	bmv1alpha1 "github.com/openshift/cluster-api-provider-baremetal/pkg/apis/baremetal/v1alpha1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
)

const (
	baremetalMachineTemplateName = "baremetal-machine-template"
)

var _ = Describe("Cluster API Baremetal MachineSet", Ordered, func() {
	var baremetalMachineTemplate *metal3v1.Metal3MachineTemplate
	var machineSet *clusterv1.MachineSet
	var mapiMachineSpec *bmv1alpha1.BareMetalMachineProviderSpec

	BeforeAll(func() {
		if platform != configv1.BareMetalPlatformType {
			Skip("Skipping Baremetal E2E tests")
		}
		framework.CreateCoreCluster(cl, clusterName, "Metal3Cluster")
		mapiMachineSpec = getBaremetalMAPIProviderSpec(cl)
		createBaremetalCluster(cl, mapiMachineSpec)
	})

	AfterEach(func() {
		if platform != configv1.BareMetalPlatformType {
			// Because AfterEach always runs, even when tests are skipped, we have to
			// explicitly skip it here for other platforms.
			Skip("Skipping Baremetal E2E tests")
		}
		framework.DeleteMachineSets(cl, machineSet)
		framework.WaitForMachineSetsDeleted(cl, machineSet)
		framework.DeleteObjects(cl, baremetalMachineTemplate)
	})

	It("should be able to run a machine", func() {
		key := client.ObjectKey{
			Namespace: "openshift-cluster-api",
			Name:      "ostest-extraworker-0",
		}

		waitForBaremetalHostState(cl, key, bmov1alpha1.StateAvailable)

		baremetalMachineTemplate = createBaremetalMachineTemplate(cl, mapiMachineSpec)

		params := framework.NewMachineSetParams(
			"baremetal-machineset",
			clusterName,
			"", // mapiMachineSpec.Zone,
			1,
			corev1.ObjectReference{
				Kind:       "Metal3MachineTemplate",
				APIVersion: infraAPIVersion,
				Name:       baremetalMachineTemplateName,
			},
		)

		params.UserDataSecret = "worker-user-data-managed"

		machineSet = framework.CreateMachineSet(cl, params)

		err := annotateMetal3Machine(machineSet)
		Expect(err).ToNot(HaveOccurred(), "should not fail to annotate")

		By("host has been annotated")

		waitForBaremetalHostState(cl, key, bmov1alpha1.StateProvisioned)

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

		fmt.Println("baremetalhost:", bmh.Name, bmh.Namespace, bmh.Status.Provisioning.State)
		if bmh.Status.Provisioning.State != state {
			return fmt.Errorf("baremetalhost is not %s", state)
		}

		return nil
	}, framework.WaitBaremetal, framework.RetryMedium).Should(Succeed())

}

func annotateMetal3Machine(machineSet *clusterv1.MachineSet) error {
	machines, err := framework.GetMachinesFromMachineSet(cl, machineSet)
	if err != nil {
		return err
	}

	if len(machines) == 0 {
		By("Waiting for machines")
		Eventually(func() bool {
			machines, _ = framework.GetMachinesFromMachineSet(cl, machineSet)
			return len(machines) != 0
		}, framework.WaitLong, framework.RetryShort).Should(BeTrue())
	}

	metal3Machine := &metal3v1.Metal3Machine{}

	// machines and metal3machines share a name
	err = cl.Get(
		ctx,
		types.NamespacedName{
			Name:      machines[0].Name,
			Namespace: machines[0].Namespace,
		},
		metal3Machine,
	)

	if err != nil {
		return fmt.Errorf("failed to get a metal3machine: %v", err)
	}

	if metal3Machine.Annotations == nil {
		metal3Machine.Annotations = make(map[string]string)
	}

	// The baremetalhost is created by dev-scripts and the naming is consistent.
	// TODO: Should we try to make sure that the baremetalhost exists?
	metal3Machine.Annotations["metal3.io/BareMetalHost"] = "openshift-cluster-api/ostest-extraworker-0"

	err = cl.Update(ctx, metal3Machine)
	if err != nil {
		return fmt.Errorf("failed to update metal3machine: %v", err)
	}

	return nil
}

func getBaremetalMAPIProviderSpec(cl client.Client) *bmv1alpha1.BareMetalMachineProviderSpec {
	machineSetList := &mapiv1.MachineSetList{}
	Expect(cl.List(ctx, machineSetList, client.InNamespace(framework.MAPINamespace))).To(Succeed())

	Expect(machineSetList.Items).ToNot(HaveLen(0))
	machineSet := machineSetList.Items[0]
	Expect(machineSet.Spec.Template.Spec.ProviderSpec.Value).ToNot(BeNil())

	providerSpec := &bmv1alpha1.BareMetalMachineProviderSpec{}
	Expect(yaml.Unmarshal(machineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, providerSpec)).To(Succeed())

	return providerSpec
}

func createBaremetalCluster(cl client.Client, mapiProviderSpec *bmv1alpha1.BareMetalMachineProviderSpec) *metal3v1.Metal3Cluster {
	By("Creating Baremetal cluster")

	host, port, err := framework.GetControlPlaneHostAndPort(cl)
	if err != nil {
		Expect(err).ToNot(HaveOccurred(), "should not fail getting the Control Plane host and port")
	}

	baremetalCluster := &metal3v1.Metal3Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: framework.CAPINamespace,
			// The ManagedBy Annotation is set so CAPI infra providers ignore the InfraCluster object,
			// as that's managed externally, in this case by the cluster-capi-operator's infracluster controller.
			Annotations: map[string]string{
				clusterv1.ManagedByAnnotation: managedByAnnotationValueClusterCAPIOperatorInfraClusterController,
			},
		},
		Spec: metal3v1.Metal3ClusterSpec{
			ControlPlaneEndpoint: metal3v1.APIEndpoint{
				Host: host,
				Port: int(port),
			},
			// Network: metal3v1.Network{
			// 	Name: &mapiProviderSpec.NetworkInterfaces[0].Network,
			// },
			// Region:  mapiProviderSpec.Region,
			// Project: mapiProviderSpec.ProjectID,
		},
	}

	if err := cl.Create(ctx, baremetalCluster); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred())
	}

	Eventually(func() (bool, error) {
		patchedBaremetalCluster := &metal3v1.Metal3Cluster{}
		err := cl.Get(ctx, client.ObjectKeyFromObject(baremetalCluster), patchedBaremetalCluster)
		if err != nil {
			return false, err
		}

		if patchedBaremetalCluster.Annotations == nil {
			return false, nil
		}

		if _, ok := patchedBaremetalCluster.Annotations[clusterv1.ManagedByAnnotation]; !ok {
			return false, nil
		}

		return patchedBaremetalCluster.Status.Ready, nil
	}, framework.WaitShort).Should(BeTrue())

	return baremetalCluster
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
