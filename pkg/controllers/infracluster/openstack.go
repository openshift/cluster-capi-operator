/*
Copyright 2025 Red Hat, Inc.

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
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/layer3/routers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	openstackclients "sigs.k8s.io/cluster-api-provider-openstack/pkg/clients"
	openstackscope "sigs.k8s.io/cluster-api-provider-openstack/pkg/scope"
)

var (
	errUnsupportedOpenStackLoadBalancerType = errors.New("unsupported load balancer type for OpenStack")
	errOpenStackNoAPIServerInternalIPs      = errors.New("no APIServerInternalIPs available")
	errOpenStackNoDefaultRouter             = errors.New("unable to determine default router from control plane machines")
	errOpenStackNoDefaultSubnet             = errors.New("unable to determine default subnet from control plane machines")
	errOpenStackNoControlPlaneMachines      = errors.New("no control plane machines found")
)

// ensureOpenStackCluster ensures the OpenStackCluster object exists.
//
//nolint:funlen
func (r *InfraClusterController) ensureOpenStackCluster(ctx context.Context, log logr.Logger) (client.Object, error) {
	target := &openstackv1.OpenStackCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Infra.Status.InfrastructureName,
			Namespace: r.CAPINamespace,
		},
	}

	// Checking whether InfraCluster object exists. If it doesn't, create it.

	if err := r.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get InfraCluster: %w", err)
	} else if err == nil {
		log.V(4).Info("OpenStackCluster already exists")
		return target, nil
	}

	log.Info(fmt.Sprintf("OpenStackCluster %s does not exist, creating it", klog.KObj(target)))

	// FIXME(stephenfin): Other providers use the below but not us. Why?
	//
	// apiURL, err := url.Parse(r.Infra.Status.APIServerInternalURL)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to parse apiURL: %w", err)
	// }
	//
	// port, err := strconv.ParseInt(apiURL.Port(), 10, 32)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to parse apiURL port: %w", err)
	// }

	if r.Infra.Status.PlatformStatus == nil {
		return nil, errInvalidPlatformStatus
	}

	// Perform some platform-specific validation

	platformStatus := r.Infra.Status.PlatformStatus.OpenStack

	if platformStatus.LoadBalancer.Type != configv1.LoadBalancerTypeOpenShiftManagedDefault {
		return nil, fmt.Errorf("%w: load balancer type %s not supported",
			errUnsupportedOpenStackLoadBalancerType, platformStatus.LoadBalancer.Type)
	}

	if len(platformStatus.APIServerInternalIPs) == 0 {
		return nil, errOpenStackNoAPIServerInternalIPs
	}

	target = &openstackv1.OpenStackCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Infra.Status.InfrastructureName,
			Namespace: r.CAPINamespace,
			// NOTE(stephenfin): Other providers set the ManagedBy annotation here so that CAPI
			// infra providers (CAPO here) ignore the InfraCluster object. However, we need CAPO
			// to populate the .Status field for us so we *do not* set the annotation here.
		},
		Spec: openstackv1.OpenStackClusterSpec{
			ControlPlaneEndpoint: &clusterv1beta1.APIEndpoint{
				// FIXME(stephenfin): As above.
				// Host: apiURL.Hostname(),
				// Port: int32(port),
				Host: platformStatus.APIServerInternalIPs[0],
				Port: 6443,
			},
			DisableAPIServerFloatingIP: ptr.To(true),
			IdentityRef: openstackv1.OpenStackIdentityReference{
				Name:      "openstack-cloud-credentials",
				CloudName: "openstack",
			},
			// NOTE(stephenfin): We deliberately don't add subnet here: CAPO will use all subnets in network,
			// which should also cover dual stack deployments. Everything else is populated below.
			Network:         nil,
			Router:          nil,
			ExternalNetwork: nil,
			Subnets:         nil,
			Tags:            []string{"openshiftClusterID=" + r.Infra.Status.InfrastructureName},
		},
	}

	// FIXME(stephenfin): Where can I source caCertificates from? The legacy infracluster controller
	// had the same issue.
	caCertificates := []byte{} // PEM encoded CA certificates
	scopeFactory := openstackscope.NewFactory(0)

	scope, err := scopeFactory.NewClientScopeFromObject(ctx, r.Client, caCertificates, log, target)
	if err != nil {
		return nil, fmt.Errorf("creating OpenStack client: %w", err)
	}

	networkClient, err := scope.NewNetworkClient()
	if err != nil {
		return nil, fmt.Errorf("creating OpenStack Networking client: %w", err)
	}

	defaultSubnet, err := getDefaultSubnetFromMachines(ctx, log, r.Client, networkClient, platformStatus)
	if err != nil {
		return nil, err
	}
	// NOTE(stephenfin): As noted previously, we deliberately *do not* add subnet here: CAPO will
	// use all subnets in network, which should also cover dual-stack deployments
	target.Spec.Network = &openstackv1.NetworkParam{ID: ptr.To(defaultSubnet.NetworkID)}

	router, err := getDefaultRouterFromSubnet(ctx, networkClient, defaultSubnet)
	if err != nil {
		return nil, err
	}

	target.Spec.Router = &openstackv1.RouterParam{ID: ptr.To(router.ID)}
	// NOTE(stephenfin): The only reason we set ExternalNetworkID in the cluster spec is to avoid
	// an error reconciling the external network if it isn't set. If CAPO ever no longer requires
	// this we can just not set it and remove much of the code above. We don't actually use it.
	target.Spec.ExternalNetwork = &openstackv1.NetworkParam{ID: ptr.To(router.GatewayInfo.NetworkID)}

	if err := r.Create(ctx, target); err != nil {
		return nil, fmt.Errorf("failed to create InfraCluster: %w", err)
	}

	log.Info(fmt.Sprintf("InfraCluster %s successfully created", klog.KObj(target)))

	return target, nil
}

// getDefaultRouterFromSubnet attempts to infer the default router used for
// the network by looking for ports with the given subnet and gateway IP
// associated with them.
func getDefaultRouterFromSubnet(_ context.Context, networkClient openstackclients.NetworkClient, subnet *subnets.Subnet) (*routers.Router, error) {
	// Find the port which owns the subnet's gateway IP
	ports, err := networkClient.ListPort(ports.ListOpts{
		NetworkID: subnet.NetworkID,
		FixedIPs: []ports.FixedIPOpts{
			{
				IPAddress: subnet.GatewayIP,
				// XXX: We should search on both subnet and IP
				// address here, but can't because of
				// https://github.com/gophercloud/gophercloud/issues/2807
				// SubnetID:  subnet.ID,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("listing ports: %w", err)
	}

	if len(ports) == 0 {
		return nil, fmt.Errorf("%w: no ports found for subnet %s", errOpenStackNoDefaultRouter, subnet.ID)
	}

	if len(ports) > 1 {
		return nil, fmt.Errorf("%w: multiple ports found for subnet %s", errOpenStackNoDefaultRouter, subnet.ID)
	}

	routerID := ports[0].DeviceID

	router, err := networkClient.GetRouter(routerID)
	if err != nil {
		return nil, fmt.Errorf("getting router %s: %w", routerID, err)
	}

	if router.GatewayInfo.NetworkID == "" {
		return nil, fmt.Errorf("%w: router %s does not have an external gateway", errOpenStackNoDefaultRouter, routerID)
	}

	return router, nil
}

// getDefaultSubnetFromMachines attempts to infer the default cluster subnet by
// directly examining the control plane machines. Specifically it looks for a
// subnet attached to a control plane machine whose CIDR contains the API
// loadbalancer internal VIP.
//
// This heuristic is only valid when the API loadbalancer type is
// LoadBalancerTypeOpenShiftManagedDefault.
//
//nolint:gocognit,funlen
func getDefaultSubnetFromMachines(ctx context.Context, log logr.Logger, kubeclient client.Client, networkClient openstackclients.NetworkClient, platformStatus *configv1.OpenStackPlatformStatus) (*subnets.Subnet, error) {
	mapiMachines := mapiv1beta1.MachineList{}
	if err := kubeclient.List(
		ctx,
		&mapiMachines,
		client.InNamespace(defaultMAPINamespace),
		client.MatchingLabels{"machine.openshift.io/cluster-api-machine-role": "master"},
	); err != nil {
		return nil, fmt.Errorf("listing control plane machines: %w", err)
	}

	if len(mapiMachines.Items) == 0 {
		return nil, errOpenStackNoControlPlaneMachines
	}

	apiServerInternalIPs := make([]net.IP, len(platformStatus.APIServerInternalIPs))
	for i, ipStr := range platformStatus.APIServerInternalIPs {
		apiServerInternalIPs[i] = net.ParseIP(ipStr)
	}

	for _, mapiMachine := range mapiMachines.Items {
		log := log.WithValues("machine", mapiMachine.Name)

		providerID := mapiMachine.Spec.ProviderID
		if providerID == nil {
			log.V(3).Info("Skipping machine: providerID is not set")
			continue
		}

		if !strings.HasPrefix(*providerID, "openstack:///") {
			log.V(2).Info("Skipping machine: providerID has unexpected format", "providerID", *providerID)
			continue
		}

		instanceID := (*providerID)[len("openstack:///"):]

		portOpts := ports.ListOpts{
			DeviceID: instanceID,
		}

		ports, err := networkClient.ListPort(portOpts)
		if err != nil {
			return nil, fmt.Errorf("listing ports for instance %s: %w", instanceID, err)
		}

		if len(ports) == 0 {
			return nil, fmt.Errorf("%w: no ports found for instance %s", errOpenStackNoDefaultSubnet, instanceID)
		}

		for _, port := range ports {
			log := log.WithValues("port", port.ID)

			for _, fixedIP := range port.FixedIPs {
				if fixedIP.SubnetID == "" {
					continue
				}

				subnet, err := networkClient.GetSubnet(fixedIP.SubnetID)
				if err != nil {
					return nil, fmt.Errorf("getting subnet %s: %w", fixedIP.SubnetID, err)
				}

				_, cidr, err := net.ParseCIDR(subnet.CIDR)
				if err != nil {
					return nil, fmt.Errorf("parsing subnet CIDR %s: %w", subnet.CIDR, err)
				}

				if slices.ContainsFunc(
					apiServerInternalIPs, func(ip net.IP) bool { return cidr.Contains(ip) },
				) {
					return subnet, nil
				}

				log.V(6).Info("subnet does not match any APIServerInternalIPs", "subnet", subnet.CIDR)
			}

			log.V(6).Info("port does not match any APIServerInternalIPs")
		}

		log.V(6).Info("machine does not match any APIServerInternalIPs")
	}

	return nil, fmt.Errorf("%w: no matching subnets found", errOpenStackNoDefaultSubnet)
}
