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
	"fmt"

	bmov1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	metal3v1 "github.com/metal3-io/cluster-api-provider-metal3/api/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
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
		mapiMachineSpec = framework.GetMAPIProviderSpec[bmv1alpha1.BareMetalMachineProviderSpec](ctx, cl)
	})

	AfterEach(func() {
		if platform != configv1.BareMetalPlatformType {
			// Because AfterEach always runs, even when tests are skipped, we have to
			// explicitly skip it here for other platforms.
			Skip("Skipping Baremetal E2E tests")
		}
		framework.DeleteMachineSets(ctx, cl, machineSet)
		framework.WaitForMachineSetsDeleted(machineSet)
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
			clusterv1.ContractVersionedObjectReference{
				Kind:     "Metal3MachineTemplate",
				APIGroup: infraAPIGroup,
				Name:     baremetalMachineTemplateName,
			},
			"worker-user-data-managed",
		))

		framework.WaitForMachineSet(ctx, cl, machineSet.Name, machineSet.Namespace)
	})
})

func waitForBaremetalHostState(cl client.Client, key client.ObjectKey, state bmov1alpha1.ProvisioningState) {
	GinkgoHelper()
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

func createBaremetalMachineTemplate(cl client.Client, mapiProviderSpec *bmv1alpha1.BareMetalMachineProviderSpec) *metal3v1.Metal3MachineTemplate {
	GinkgoHelper()
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
		Expect(err).ToNot(HaveOccurred(), "should not fail creating baremetal machine template")
	}

	return baremetalMachineTemplate
}
