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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
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
	var mapiMachineSpec *mapiv1beta1.VSphereMachineProviderSpec

	BeforeAll(func() {
		if platform != configv1.VSpherePlatformType {
			Skip("Skipping vSphere E2E tests")
		}
		mapiMachineSpec = framework.GetMAPIProviderSpec[mapiv1beta1.VSphereMachineProviderSpec](ctx, cl)
		createVSphereSecret(cl, mapiMachineSpec)
	})

	AfterEach(func() {
		if platform != configv1.VSpherePlatformType {
			// Because AfterEach always runs, even when tests are skipped, we have to
			// explicitly skip it here for other platforms.
			Skip("Skipping vSphere E2E tests")
		}
		framework.DeleteMachineSets(ctx, cl, machineSet)
		framework.WaitForMachineSetsDeleted(machineSet)
		framework.DeleteObjects(ctx, cl, vSphereMachineTemplate)
	})

	It("should be able to run a machine", func() {
		vSphereMachineTemplate = createVSphereMachineTemplate(cl, mapiMachineSpec)

		machineSet = framework.CreateMachineSet(ctx, cl, framework.NewMachineSetParams(
			"vsphere-machineset",
			clusterName,
			"",
			1,
			clusterv1.ContractVersionedObjectReference{
				Kind:     "VSphereMachineTemplate",
				APIGroup: infraAPIGroup,
				Name:     vSphereMachineTemplateName,
			},
			"worker-user-data",
		))

		framework.WaitForMachineSet(ctx, cl, machineSet.Name, machineSet.Namespace, framework.WaitLong)
	})
})

func createVSphereSecret(cl client.Client, mapiProviderSpec *mapiv1beta1.VSphereMachineProviderSpec) {
	GinkgoHelper()
	By("Creating a vSphere credentials secret")

	username, password := getVSphereCredentials(ctx, cl, mapiProviderSpec)

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

func getVSphereCredentials(ctx context.Context, cl client.Client, mapiProviderSpec *mapiv1beta1.VSphereMachineProviderSpec) (string, string) {
	GinkgoHelper()

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

func createVSphereMachineTemplate(cl client.Client, mapiProviderSpec *mapiv1beta1.VSphereMachineProviderSpec) *vspherev1.VSphereMachineTemplate {
	GinkgoHelper()
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
