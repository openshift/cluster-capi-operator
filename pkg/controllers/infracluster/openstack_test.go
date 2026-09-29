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
	"errors"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	configv1 "github.com/openshift/api/config/v1"

	mapiv1beta1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"

	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/external"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/layer3/routers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
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

var _ = Describe("resolveRouterAndExternalNetwork", func() {
	const (
		subnetID    = "33333333-3333-3333-3333-333333333333"
		networkID   = "44444444-4444-4444-4444-444444444444"
		gatewayIP   = "10.0.0.1"
		portID      = "66666666-6666-6666-6666-666666666666"
		routerID    = "77777777-7777-7777-7777-777777777777"
		extNetID    = "88888888-8888-8888-8888-888888888888"
		deviceOwner = "network:router_interface"
	)

	var (
		mockCtrl      *gomock.Controller
		networkClient *openstackclientsmock.MockNetworkClient
		subnet        *subnets.Subnet
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		networkClient = openstackclientsmock.NewMockNetworkClient(mockCtrl)
		subnet = &subnets.Subnet{
			ID:        subnetID,
			NetworkID: networkID,
			GatewayIP: gatewayIP,
		}
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	gatewayPortListOpts := func() ports.ListOpts {
		return ports.ListOpts{
			NetworkID: networkID,
			FixedIPs:  []ports.FixedIPOpts{{IPAddress: gatewayIP}},
		}
	}

	externalNetworkListOpts := func() external.ListOptsExt {
		return external.ListOptsExt{
			ListOptsBuilder: networks.ListOpts{},
			External:        ptr.To(true),
		}
	}

	It("resolves the router and external network when a router owns the subnet's gateway IP", func() {
		networkClient.EXPECT().ListPort(gatewayPortListOpts()).Return([]ports.Port{
			{ID: portID, DeviceID: routerID, DeviceOwner: deviceOwner},
		}, nil)
		networkClient.EXPECT().GetRouter(routerID).Return(&routers.Router{
			ID:          routerID,
			GatewayInfo: routers.GatewayInfo{NetworkID: extNetID},
		}, nil)

		router, externalNetwork, err := resolveRouterAndExternalNetwork(context.Background(), logr.Discard(), networkClient, subnet)
		Expect(err).NotTo(HaveOccurred())
		Expect(router).NotTo(BeNil())
		Expect(*router.ID).To(Equal(routerID))
		Expect(externalNetwork).NotTo(BeNil())
		Expect(*externalNetwork.ID).To(Equal(extNetID))
	})

	It("falls back to resolving the external network directly when the subnet has no gateway port", func() {
		networkClient.EXPECT().ListPort(gatewayPortListOpts()).Return([]ports.Port{}, nil)
		networkClient.EXPECT().ListNetwork(externalNetworkListOpts()).Return([]networks.Network{
			{ID: extNetID},
		}, nil)

		router, externalNetwork, err := resolveRouterAndExternalNetwork(context.Background(), logr.Discard(), networkClient, subnet)
		Expect(err).NotTo(HaveOccurred())
		Expect(router).To(BeNil())
		Expect(externalNetwork).NotTo(BeNil())
		Expect(*externalNetwork.ID).To(Equal(extNetID))
	})

	It("leaves external network unset too when there is no router and no external network in the project", func() {
		networkClient.EXPECT().ListPort(gatewayPortListOpts()).Return([]ports.Port{}, nil)
		networkClient.EXPECT().ListNetwork(externalNetworkListOpts()).Return([]networks.Network{}, nil)

		router, externalNetwork, err := resolveRouterAndExternalNetwork(context.Background(), logr.Discard(), networkClient, subnet)
		Expect(err).NotTo(HaveOccurred())
		Expect(router).To(BeNil())
		Expect(externalNetwork).To(BeNil())
	})

	It("returns an explicit error when there is no router and multiple external networks exist (ambiguous)", func() {
		networkClient.EXPECT().ListPort(gatewayPortListOpts()).Return([]ports.Port{
			{ID: portID, DeviceID: routerID},
			{ID: "99999999-9999-9999-9999-999999999999", DeviceID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
		}, nil)
		networkClient.EXPECT().ListNetwork(externalNetworkListOpts()).Return([]networks.Network{
			{ID: extNetID},
			{ID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"},
		}, nil)

		_, _, err := resolveRouterAndExternalNetwork(context.Background(), logr.Discard(), networkClient, subnet)
		Expect(err).To(MatchError(errOpenStackAmbiguousExternalNetworks))
	})

	It("falls back to resolving the external network directly when the router has no external gateway", func() {
		networkClient.EXPECT().ListPort(gatewayPortListOpts()).Return([]ports.Port{
			{ID: portID, DeviceID: routerID},
		}, nil)
		networkClient.EXPECT().GetRouter(routerID).Return(&routers.Router{ID: routerID}, nil)
		networkClient.EXPECT().ListNetwork(externalNetworkListOpts()).Return([]networks.Network{}, nil)

		router, externalNetwork, err := resolveRouterAndExternalNetwork(context.Background(), logr.Discard(), networkClient, subnet)
		Expect(err).NotTo(HaveOccurred())
		Expect(router).To(BeNil())
		Expect(externalNetwork).To(BeNil())
	})

	It("propagates real OpenStack API errors from router discovery instead of silently ignoring them", func() {
		networkClient.EXPECT().ListPort(gatewayPortListOpts()).Return(nil, errors.New("boom"))

		_, _, err := resolveRouterAndExternalNetwork(context.Background(), logr.Discard(), networkClient, subnet)
		Expect(err).To(MatchError(ContainSubstring("boom")))
	})

	It("propagates real OpenStack API errors from external network discovery instead of silently ignoring them", func() {
		networkClient.EXPECT().ListPort(gatewayPortListOpts()).Return([]ports.Port{}, nil)
		networkClient.EXPECT().ListNetwork(externalNetworkListOpts()).Return(nil, errors.New("kaboom"))

		_, _, err := resolveRouterAndExternalNetwork(context.Background(), logr.Discard(), networkClient, subnet)
		Expect(err).To(MatchError(ContainSubstring("kaboom")))
	})
})

var _ = Describe("resolveExternalNetworkWithoutRouter", func() {
	const extNetID = "88888888-8888-8888-8888-888888888888"

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

	externalNetworkListOpts := external.ListOptsExt{
		ListOptsBuilder: networks.ListOpts{},
		External:        ptr.To(true),
	}

	It("returns nil when the project has no external network", func() {
		networkClient.EXPECT().ListNetwork(externalNetworkListOpts).Return([]networks.Network{}, nil)

		externalNetwork, err := resolveExternalNetworkWithoutRouter(networkClient)
		Expect(err).NotTo(HaveOccurred())
		Expect(externalNetwork).To(BeNil())
	})

	It("returns the network when the project has exactly one external network", func() {
		networkClient.EXPECT().ListNetwork(externalNetworkListOpts).Return([]networks.Network{{ID: extNetID}}, nil)

		externalNetwork, err := resolveExternalNetworkWithoutRouter(networkClient)
		Expect(err).NotTo(HaveOccurred())
		Expect(externalNetwork).NotTo(BeNil())
		Expect(*externalNetwork.ID).To(Equal(extNetID))
	})

	It("returns an explicit, attributable error when the project has multiple external networks", func() {
		networkClient.EXPECT().ListNetwork(externalNetworkListOpts).Return([]networks.Network{
			{ID: extNetID},
			{ID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"},
		}, nil)

		_, err := resolveExternalNetworkWithoutRouter(networkClient)
		Expect(err).To(MatchError(errOpenStackAmbiguousExternalNetworks))
	})

	It("propagates real OpenStack API errors", func() {
		networkClient.EXPECT().ListNetwork(externalNetworkListOpts).Return(nil, errors.New("boom"))

		_, err := resolveExternalNetworkWithoutRouter(networkClient)
		Expect(err).To(MatchError(ContainSubstring("boom")))
	})
})
