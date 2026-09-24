/*
Copyright 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package infracluster

import (
	"context"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	configv1 "github.com/openshift/api/config/v1"

	mapiv1beta1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"

	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	openstackclientsmock "sigs.k8s.io/cluster-api-provider-openstack/pkg/clients/mock"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func isSupportedOpenStackLoadBalancerType(lbType configv1.PlatformLoadBalancerType) bool {
	switch lbType {
	case configv1.LoadBalancerTypeOpenShiftManagedDefault, configv1.LoadBalancerTypeUserManaged:
		return true
	default:
		return false
	}
}

var _ = Describe("isSupportedOpenStackLoadBalancerType", func() {
	DescribeTable(
		"should determine whether the load balancer type is supported",
		func(lbType configv1.PlatformLoadBalancerType, expected bool) {
			Expect(isSupportedOpenStackLoadBalancerType(lbType)).To(Equal(expected))
		},
		Entry("OpenShiftManagedDefault is supported", configv1.LoadBalancerTypeOpenShiftManagedDefault, true),
		Entry("UserManaged is supported", configv1.LoadBalancerTypeUserManaged, true),
		Entry("empty is not supported", configv1.PlatformLoadBalancerType(""), false),
		Entry("unknown types are not supported", configv1.PlatformLoadBalancerType("SomethingElse"), false),
	)
})

var _ = Describe("getDefaultSubnetFromMachines", func() {
	const (
		controlPlaneMachineName = "master-0"
		instanceID              = "11111111-1111-1111-1111-111111111111"
		portID                  = "22222222-2222-2222-2222-222222222222"
		subnetID                = "33333333-3333-3333-3333-333333333333"
		networkID               = "44444444-4444-4444-4444-444444444444"
	)

	var (
		mockCtrl      *gomock.Controller
		networkClient *openstackclientsmock.MockNetworkClient
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		networkClient = openstackclientsmock.NewMockNetworkClient(mockCtrl)
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	newControlPlaneMachine := func(name, providerID string) runtime.Object {
		return mapiv1beta1resourcebuilder.Machine().
			WithNamespace(defaultMAPINamespace).
			WithName(name).
			WithProviderID(ptr.To(providerID)).
			AsMaster().
			Build()
	}

	newPlatformStatus := func(vips ...string) *configv1.OpenStackPlatformStatus {
		return &configv1.OpenStackPlatformStatus{
			APIServerInternalIPs: vips,
		}
	}

	It("returns an error when there are no control plane machines", func() {
		kubeClient := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).Build()

		_, err := getDefaultSubnetFromMachines(context.Background(), logr.Discard(), kubeClient, networkClient, newPlatformStatus("10.0.0.5"))
		Expect(err).To(MatchError(errOpenStackNoControlPlaneMachines))
	})

	It("finds the subnet whose CIDR contains the (single) API server internal IP", func() {
		kubeClient := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).
			WithRuntimeObjects(newControlPlaneMachine(controlPlaneMachineName, "openstack:///"+instanceID)).
			Build()

		networkClient.EXPECT().ListPort(ports.ListOpts{DeviceID: instanceID}).Return([]ports.Port{
			{
				ID:        portID,
				FixedIPs:  []ports.IP{{SubnetID: subnetID, IPAddress: "10.0.0.10"}},
				NetworkID: networkID,
			},
		}, nil)
		networkClient.EXPECT().GetSubnet(subnetID).Return(&subnets.Subnet{
			ID:        subnetID,
			NetworkID: networkID,
			CIDR:      "10.0.0.0/24",
		}, nil)

		subnet, err := getDefaultSubnetFromMachines(context.Background(), logr.Discard(), kubeClient, networkClient, newPlatformStatus("10.0.0.5"))
		Expect(err).NotTo(HaveOccurred())
		Expect(subnet.ID).To(Equal(subnetID))
		Expect(subnet.NetworkID).To(Equal(networkID))
	})

	It("returns an error when no subnet matches any APIServerInternalIPs entry", func() {
		kubeClient := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).
			WithRuntimeObjects(newControlPlaneMachine(controlPlaneMachineName, "openstack:///"+instanceID)).
			Build()

		networkClient.EXPECT().ListPort(ports.ListOpts{DeviceID: instanceID}).Return([]ports.Port{
			{
				ID:        portID,
				FixedIPs:  []ports.IP{{SubnetID: subnetID, IPAddress: "10.0.0.10"}},
				NetworkID: networkID,
			},
		}, nil)
		networkClient.EXPECT().GetSubnet(subnetID).Return(&subnets.Subnet{
			ID:        subnetID,
			NetworkID: networkID,
			CIDR:      "10.0.0.0/24",
		}, nil)

		_, err := getDefaultSubnetFromMachines(context.Background(), logr.Discard(), kubeClient, networkClient, newPlatformStatus("192.168.0.5"))
		Expect(err).To(MatchError(ContainSubstring("no matching subnets found")))
	})

	It("skips machines without a providerID", func() {
		kubeClient := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).
			WithRuntimeObjects(
				mapiv1beta1resourcebuilder.Machine().
					WithNamespace(defaultMAPINamespace).
					WithName(controlPlaneMachineName).
					AsMaster().
					Build(),
			).
			Build()

		_, err := getDefaultSubnetFromMachines(context.Background(), logr.Discard(), kubeClient, networkClient, newPlatformStatus("10.0.0.5"))
		Expect(err).To(MatchError(ContainSubstring("no matching subnets found")))
	})
})
