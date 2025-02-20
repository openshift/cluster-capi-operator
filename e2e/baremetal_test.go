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
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	yaml "sigs.k8s.io/yaml"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	bmv1alpha1 "github.com/openshift/cluster-api-provider-baremetal/pkg/apis/baremetal/v1alpha1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"

	certificatesv1 "k8s.io/api/certificates/v1"
	clientset "k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const (
	baremetalMachineTemplateName = "baremetal-machine-template"
)

var bmhKey = client.ObjectKey{
	Namespace: "openshift-cluster-api",
	Name:      "ostest-extraworker-0",
}

var nodeKey = client.ObjectKey{
	Name: "extraworker-0.ostest.test.metalkube.org",
}

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
		waitForBaremetalHostState(cl, bmhKey, bmov1alpha1.StateAvailable)

		baremetalMachineTemplate = createBaremetalMachineTemplate(cl, mapiMachineSpec)

		machineSet = framework.CreateMachineSet(cl, framework.NewMachineSetParams(
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

		waitForBaremetalHostState(cl, bmhKey, bmov1alpha1.StateProvisioned)
		approveCertificates(cl)
		waitForNode(cl)
		labelNode(cl)

		framework.WaitForMachineSet(cl, machineSet.Name)
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

		By(fmt.Sprintf("  baremetalhost: %s %s", bmh.Name, bmh.Status.Provisioning.State))

		if bmh.Status.Provisioning.State != state {
			return fmt.Errorf("baremetalhost is not %s", state)
		}

		return nil
	}, framework.WaitBaremetal, framework.RetryLong).Should(Succeed())

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

// After the baremetalhost is provisioned and boots, it will try to register
// itself with the control plane as a Node.  Before that can succeed, we need
// to approve 2 certificate signing requests.
func approveCertificates(cl client.Client) {
	By("Approving certificates")
	approvalsLeft := 2

	bootstrapperCsr := "system:serviceaccount:openshift-machine-config-operator:node-bootstrapper"
	nodeCsr := "system:node:extraworker-0.ostest.test.metalkube.org"

	Eventually(func() error {
		csrs := &certificatesv1.CertificateSigningRequestList{}
		Expect(cl.List(ctx, csrs)).To(Succeed())

		cfg, err := config.GetConfig()
		Expect(err).To(Succeed())

		client, err := clientset.NewForConfig(cfg)
		Expect(err).To(Succeed())

		for _, csr := range csrs.Items {
			pending := true

			for _, condition := range csr.Status.Conditions {
				if condition.Type == certificatesv1.CertificateApproved {
					pending = false
				}
				if condition.Type == certificatesv1.CertificateDenied {
					pending = false
				}
			}

			if pending {
				if csr.Spec.Username == bootstrapperCsr || csr.Spec.Username == nodeCsr {
					csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{
						Type:           certificatesv1.CertificateApproved,
						Status:         corev1.ConditionTrue,
						LastUpdateTime: metav1.Now(),
					})

					_, err := client.CertificatesV1().CertificateSigningRequests().UpdateApproval(ctx, csr.Name, &csr, metav1.UpdateOptions{})
					Expect(err).To(Succeed())

					approvalsLeft--

					if approvalsLeft == 0 {
						return nil
					}
				}
			}
		}

		return fmt.Errorf("Not enought approvals yet")

	}, framework.WaitLong, framework.RetryMedium).Should(Succeed())
}

// Wait for the Node to be Ready
func waitForNode(cl client.Client) {
	Eventually(func() error {
		node := &corev1.Node{}
		err := cl.Get(ctx, nodeKey, node)

		Expect(err).To(Succeed())

		isReady := false

		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				isReady = true
			}
		}

		if !isReady {
			return fmt.Errorf("node is not ready yet")
		}

		return nil

	}, framework.WaitLong, framework.RetryMedium).Should(Succeed())
}

// Label the Node with the baremetalhost's uuid
func labelNode(cl client.Client) {
	node := &corev1.Node{}
	err := cl.Get(ctx, nodeKey, node)
	Expect(err).To(Succeed())

	bmh := bmov1alpha1.BareMetalHost{}
	err = cl.Get(ctx, bmhKey, &bmh)
	Expect(err).To(Succeed())

	if node.Labels == nil {
		node.Labels = map[string]string{}
	}

	node.Labels["metal3.io/uuid"] = fmt.Sprint(bmh.ObjectMeta.UID)

	err = cl.Update(ctx, node)
	Expect(err).To(Succeed())
}
