// Copyright 2024 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	yaml "sigs.k8s.io/yaml"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
)

const (
	vSphereMachineTemplateName = "vsphere-machine-template"
	kubeSystemnamespace        = "kube-system"
	//nolint:gosec // This is just the resource name, not the actual credentials
	vSphereCredentialsName                                            = "vsphere-creds"
	managedByAnnotationValueClusterCAPIOperatorInfraClusterController = "cluster-capi-operator-infracluster-controller"
)

var _ = Describe("Cluster API vSphere MachineSet", Ordered, func() {
	var (
		cl                     client.Client
		ctx                    = context.Background()
		vSphereMachineTemplate *vspherev1.VSphereMachineTemplate
		machineSet             *clusterv1.MachineSet
		mapiMachineSpec        *mapiv1.VSphereMachineProviderSpec
		platform               configv1.PlatformType
		clusterName            string
	)

	BeforeAll(func() {
		cfg, err := config.GetConfig()
		Expect(err).ToNot(HaveOccurred(), "Failed to GetConfig")

		cl, err = client.New(cfg, client.Options{})
		Expect(err).ToNot(HaveOccurred(), "Failed to create Kubernetes client for test")

		infra := &configv1.Infrastructure{}
		infraName := client.ObjectKey{
			Name: infrastructureName,
		}
		Expect(cl.Get(ctx, infraName, infra)).To(Succeed(), "Failed to get cluster infrastructure object")
		Expect(infra.Status.PlatformStatus).ToNot(BeNil(), "expected the infrastructure Status.PlatformStatus to not be nil")
		clusterName = infra.Status.InfrastructureName
		platform = infra.Status.PlatformStatus.Type
		if platform != configv1.VSpherePlatformType {
			Skip("Skipping vSphere E2E tests")
		}
		mapiMachineSpec = getVSphereMAPIProviderSpec(cl)
		createVSphereSecret(cl, mapiMachineSpec, clusterName)
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
	Expect(cl.List(framework.GetContext(), machineSetList, client.InNamespace(framework.MAPINamespace))).To(Succeed(),
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

func createVSphereSecret(cl client.Client, mapiProviderSpec *mapiv1.VSphereMachineProviderSpec, clusterName string) {
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

	if err := cl.Create(framework.GetContext(), vSphereSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred(), "should not fail creating a VSphere credentials secret")
	}
}

func getVSphereCredentials(cl client.Client, mapiProviderSpec *mapiv1.VSphereMachineProviderSpec) (string, string) {
	vSphereCredentialsSecret := &corev1.Secret{}
	err := cl.Get(framework.GetContext(), types.NamespacedName{
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

	if err := cl.Create(framework.GetContext(), vSphereMachineTemplate); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred(), "should not error creating the VSphere Cluster object")
	}

	return vSphereMachineTemplate
}
