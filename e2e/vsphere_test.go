package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	yaml "sigs.k8s.io/yaml"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
)

const (
	vSphereMachineTemplateName = "vsphere-machine-template"
	kubeSystemnamespace        = "kube-system"
	vSphereCredentialsName     = "vsphere-creds"
)

var _ = Describe("Cluster API vSphere MachineSet", Ordered, func() {
	var vSphereMachineTemplate *vspherev1.VSphereMachineTemplate
	var machineSet *clusterv1.MachineSet
	var mapiMachineSpec *mapiv1.VSphereMachineProviderSpec

	BeforeAll(func() {
		if platform != configv1.VSpherePlatformType {
			Skip("Skipping vSphere E2E tests")
		}
		mapiMachineSpec = getVSphereMAPIProviderSpec(cl)
		createVSphereSecret(cl, mapiMachineSpec)
		framework.CreateCoreCluster(cl, clusterName, "VSphereCluster")
		createVSphereCluster(cl, mapiMachineSpec)
	})

	AfterEach(func() {
		if platform != configv1.VSpherePlatformType {
			// Because AfterEach always runs, even when tests are skipped, we have to
			// explicitly skip it here for other platforms.
			Skip("Skipping vSphere E2E tests")
		}
		framework.DeleteMachineSets(cl, machineSet)
		framework.WaitForMachineSetsDeleted(cl, machineSet)
		framework.DeleteObjects(cl, vSphereMachineTemplate)
	})

	It("should be able to run a machine", func() {
		vSphereMachineTemplate = createVSphereMachineTemplate(cl, mapiMachineSpec)

		machineSet = framework.CreateMachineSet(cl, framework.NewMachineSetParams(
			"vsphere-machineset",
			clusterName,
			"",
			1,
			corev1.ObjectReference{
				Kind:       "VSphereMachineTemplate",
				APIVersion: infraAPIVersion,
				Name:       vSphereMachineTemplateName,
			},
		))

		framework.WaitForMachineSet(cl, machineSet.Name)
	})
})

func getVSphereMAPIProviderSpec(cl client.Client) *mapiv1.VSphereMachineProviderSpec {
	machineSetList := &mapiv1.MachineSetList{}
	Expect(cl.List(ctx, machineSetList, client.InNamespace(framework.MAPINamespace))).To(Succeed(),
		"should not fail listing MAPI MachineSets")

	Expect(machineSetList.Items).ToNot(HaveLen(0), "expected to have at least a MachineSet")
	machineSet := machineSetList.Items[0]
	Expect(machineSet.Spec.Template.Spec.ProviderSpec.Value).ToNot(BeNil(),
		"expected not to have an empty MAPI MachineSet ProviderSpec")

	providerSpec := &mapiv1.VSphereMachineProviderSpec{}
	Expect(yaml.Unmarshal(machineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, providerSpec)).To(Succeed(),
		"should not fail YAML decoding MAPI MachineSet provider spec")

	return providerSpec
}

func createVSphereSecret(cl client.Client, mapiProviderSpec *mapiv1.VSphereMachineProviderSpec) {
	By("Creating a vSphere credentials secret")

	username, password := getVSphereCredentials(cl, mapiProviderSpec)

	vSphereSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: framework.CAPINamespace,
		},
		StringData: map[string]string{
			"username": username,
			"password": password,
		},
	}

	if err := cl.Create(ctx, vSphereSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred(), "should not fail creating a VSphere credentials secret")
	}
}

func getVSphereCredentials(cl client.Client, mapiProviderSpec *mapiv1.VSphereMachineProviderSpec) (string, string) {
	vSphereCredentialsSecret := &corev1.Secret{}
	err := cl.Get(ctx, types.NamespacedName{
		Namespace: kubeSystemnamespace,
		Name:      vSphereCredentialsName,
	}, vSphereCredentialsSecret)
	Expect(err).ToNot(HaveOccurred(), "should not fail getting the VSphere credentials secret")

	username, ok := vSphereCredentialsSecret.Data[fmt.Sprintf("%s.username", mapiProviderSpec.Workspace.Server)]
	Expect(ok).To(BeTrue(), "expected to find a username in the VSphere credentials secret")

	password, ok := vSphereCredentialsSecret.Data[fmt.Sprintf("%s.password", mapiProviderSpec.Workspace.Server)]
	Expect(ok).To(BeTrue(), "expected to find a password in the VSphere credentials secret")

	return string(username), string(password)
}

func createVSphereCluster(cl client.Client, mapiProviderSpec *mapiv1.VSphereMachineProviderSpec) *vspherev1.VSphereCluster {
	By("Creating vSphere cluster")

	host, port, err := framework.GetControlPlaneHostAndPort(cl)
	if err != nil {
		Expect(err).ToNot(HaveOccurred(), "should not fail getting the Control Plane host and port")
	}

	vSphereCluster := &vspherev1.VSphereCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: framework.CAPINamespace,
		},
		Spec: vspherev1.VSphereClusterSpec{
			Server: mapiProviderSpec.Workspace.Server,
			IdentityRef: &vspherev1.VSphereIdentityReference{
				Kind: "Secret",
				Name: clusterName,
			},
			ControlPlaneEndpoint: vspherev1.APIEndpoint{
				Host: host,
				Port: port,
			},
		},
	}

	if err := cl.Create(ctx, vSphereCluster); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred(), "should not error creating the VSphere Cluster object")
	}

	Eventually(func() (bool, error) {
		patchedVSphereCluster := &vspherev1.VSphereCluster{}
		err := cl.Get(ctx, client.ObjectKeyFromObject(vSphereCluster), patchedVSphereCluster)
		if err != nil {
			return false, err
		}

		if patchedVSphereCluster.Annotations == nil {
			return false, nil
		}

		if _, ok := patchedVSphereCluster.Annotations[clusterv1.ManagedByAnnotation]; !ok {
			return false, nil
		}

		return patchedVSphereCluster.Status.Ready, nil
	}, framework.WaitShort).Should(BeTrue(), "should not time out waiting for the VSphere Cluster to become 'Ready'")

	return vSphereCluster
}

func createVSphereMachineTemplate(cl client.Client, mapiProviderSpec *mapiv1.VSphereMachineProviderSpec) *vspherev1.VSphereMachineTemplate {
	By("Creating vSphere machine template")

	Expect(mapiProviderSpec).ToNot(BeNil(), "expected MAPI ProviderSpec to not be nil")
	Expect(mapiProviderSpec.Network).ToNot(BeNil(), "expected MAPI ProviderSpec's network to not be nil")
	Expect(len(mapiProviderSpec.Network.Devices)).To(BeNumerically(">", 0), "expected MAPI ProviderSpec's Network to have Devices")
	Expect(mapiProviderSpec.Network.Devices[0].NetworkName).ToNot(BeEmpty(), "expected MAPI ProviderSpec's Network Device to have a network name")
	Expect(mapiProviderSpec.Template).ToNot(BeEmpty(), "expected MAPI ProviderSpec's Template to not be empty")

	vSphereMachineSpec := vspherev1.VSphereMachineSpec{
		VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
			Template:     mapiProviderSpec.Template,
			Server:       mapiProviderSpec.Workspace.Server,
			DiskGiB:      mapiProviderSpec.DiskGiB,
			CloneMode:    vspherev1.CloneMode("linkedClone"),
			Datacenter:   mapiProviderSpec.Workspace.Datacenter,
			Datastore:    mapiProviderSpec.Workspace.Datastore,
			Folder:       mapiProviderSpec.Workspace.Folder,
			ResourcePool: mapiProviderSpec.Workspace.ResourcePool,
			NumCPUs:      mapiProviderSpec.NumCPUs,
			MemoryMiB:    mapiProviderSpec.MemoryMiB,
			Network: vspherev1.NetworkSpec{
				Devices: []vspherev1.NetworkDeviceSpec{
					{
						DHCP4:       true,
						NetworkName: mapiProviderSpec.Network.Devices[0].NetworkName,
					},
				},
			},
		},
	}

	vSphereMachineTemplate := &vspherev1.VSphereMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vSphereMachineTemplateName,
			Namespace: framework.CAPINamespace,
		},
		Spec: vspherev1.VSphereMachineTemplateSpec{
			Template: vspherev1.VSphereMachineTemplateResource{
				Spec: vSphereMachineSpec,
			},
		},
	}

	if err := cl.Create(ctx, vSphereMachineTemplate); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred(), "should not error creating the VSphere Cluster object")
	}

	return vSphereMachineTemplate
}
