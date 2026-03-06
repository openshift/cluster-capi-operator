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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gcpv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
)

const (
	gcpMachineTemplateName = "gcp-machine-template"
)

var _ = Describe("[sig-cluster-lifecycle][Feature:ClusterAPI][platform:gcp][Disruptive] Cluster API GCP MachineSet", Ordered, Label("Conformance"), Label("Serial"), func() {
	var gcpMachineTemplate *gcpv1.GCPMachineTemplate
	var machineSet *clusterv1.MachineSet
	var mapiMachineSpec *mapiv1beta1.GCPMachineProviderSpec

	BeforeAll(func() {
		InitCommonVariables()
		if platform != configv1.GCPPlatformType {
			Skip("Skipping GCP E2E tests")
		}
		mapiMachineSpec = framework.GetMAPIProviderSpec[mapiv1beta1.GCPMachineProviderSpec](ctx, cl)
	})

	AfterEach(func() {
		if platform != configv1.GCPPlatformType {
			// Because AfterEach always runs, even when tests are skipped, we have to
			// explicitly skip it here for other platforms.
			Skip("Skipping GCP E2E tests")
		}
		framework.DeleteMachineSets(ctx, cl, machineSet)
		framework.WaitForMachineSetsDeleted(machineSet)
		framework.DeleteObjects(ctx, cl, gcpMachineTemplate)
	})

	It("should be able to run a machine", func() {
		gcpMachineTemplate = createGCPMachineTemplate(ctx, cl, mapiMachineSpec)

		machineSet = framework.CreateMachineSet(ctx, cl, framework.NewMachineSetParams(
			"gcp-machineset",
			clusterName,
			mapiMachineSpec.Zone,
			1,
			clusterv1.ContractVersionedObjectReference{
				Kind:     "GCPMachineTemplate",
				APIGroup: infraAPIGroup,
				Name:     gcpMachineTemplateName,
			},
			"worker-user-data",
		))

		framework.WaitForMachineSet(ctx, cl, machineSet.Name, machineSet.Namespace, framework.WaitLong)
	})
})

func createGCPMachineTemplate(ctx context.Context, cl client.Client, mapiProviderSpec *mapiv1beta1.GCPMachineProviderSpec) *gcpv1.GCPMachineTemplate {
	GinkgoHelper()
	By("Creating GCP machine template")

	Expect(mapiProviderSpec).ToNot(BeNil(), "expected MAPI ProviderSpec to not be nil")
	Expect(mapiProviderSpec.Disks).ToNot(BeNil(), "expected MAPI ProviderSpec Disks to not be nil")
	Expect(len(mapiProviderSpec.Disks)).To(BeNumerically(">", 0), "expected at least one disk")
	Expect(mapiProviderSpec.Disks[0].Type).ToNot(BeEmpty(), "expected disk type to not be empty")
	Expect(mapiProviderSpec.MachineType).ToNot(BeEmpty(), "expected MachineType to not be empty")
	Expect(mapiProviderSpec.NetworkInterfaces).ToNot(BeNil(), "expected NetworkInterfaces to not be nil")
	Expect(len(mapiProviderSpec.NetworkInterfaces)).To(BeNumerically(">", 0), "expected at least one network interface")
	Expect(mapiProviderSpec.NetworkInterfaces[0].Subnetwork).ToNot(BeEmpty(), "expected Subnetwork to not be empty")
	Expect(mapiProviderSpec.ServiceAccounts).ToNot(BeNil(), "expected ServiceAccounts to not be nil")
	Expect(mapiProviderSpec.ServiceAccounts[0].Email).ToNot(BeEmpty(), "expected ServiceAccount email to not be empty")
	Expect(mapiProviderSpec.ServiceAccounts[0].Scopes).ToNot(BeNil(), "expected ServiceAccount scopes to not be nil")
	Expect(len(mapiProviderSpec.ServiceAccounts)).To(BeNumerically(">", 0), "expected at least one ServiceAccount")
	Expect(mapiProviderSpec.Tags).ToNot(BeNil(), "expected Tags to not be nil")
	Expect(len(mapiProviderSpec.Tags)).To(BeNumerically(">", 0), "expected at least one tag")

	var rootDeviceType gcpv1.DiskType

	switch mapiProviderSpec.Disks[0].Type {
	case "pd-standard":
		rootDeviceType = gcpv1.PdStandardDiskType
	case "pd-ssd":
		rootDeviceType = gcpv1.PdSsdDiskType
	case "local-ssd":
		rootDeviceType = gcpv1.LocalSsdDiskType
	}

	ipForwardingDisabled := gcpv1.IPForwardingDisabled
	gcpMachineSpec := gcpv1.GCPMachineSpec{
		RootDeviceType: &rootDeviceType,
		RootDeviceSize: mapiProviderSpec.Disks[0].SizeGB,
		InstanceType:   mapiProviderSpec.MachineType,
		Image:          &mapiProviderSpec.Disks[0].Image,
		Subnet:         &mapiProviderSpec.NetworkInterfaces[0].Subnetwork,
		ServiceAccount: &gcpv1.ServiceAccount{
			Email:  mapiProviderSpec.ServiceAccounts[0].Email,
			Scopes: mapiProviderSpec.ServiceAccounts[0].Scopes,
		},
		AdditionalNetworkTags: mapiProviderSpec.Tags,
		AdditionalLabels:      gcpv1.Labels{fmt.Sprintf("kubernetes-io-cluster-%s", clusterName): "owned"},
		IPForwarding:          &ipForwardingDisabled,
	}

	gcpMachineTemplate := &gcpv1.GCPMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gcpMachineTemplateName,
			Namespace: framework.CAPINamespace,
		},
		Spec: gcpv1.GCPMachineTemplateSpec{
			Template: gcpv1.GCPMachineTemplateResource{
				Spec: gcpMachineSpec,
			},
		},
	}

	if err := cl.Create(ctx, gcpMachineTemplate); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred(), "should not fail creating GCP machine template")
	}

	return gcpMachineTemplate
}
