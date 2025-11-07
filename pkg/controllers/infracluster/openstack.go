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

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
)

var (
	errUnsupportedOpenStackLoadBalancerType = errors.New("unsupported load balancer type for OpenStack")
	errOpenStackNoAPIServerInternalIPs      = errors.New("no APIServerInternalIPs available")
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
			// The ManagedBy Annotation is set so CAPI infra providers ignore the InfraCluster object,
			// as that's managed externally, in this case by this controller.
			Annotations: map[string]string{
				clusterv1.ManagedByAnnotation: managedByAnnotationValueClusterCAPIOperatorInfraClusterController,
			},
		},
		Spec: openstackv1.OpenStackClusterSpec{
			ControlPlaneEndpoint: &clusterv1.APIEndpoint{
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
			// FIXME(stephenfin): Populate these.
			Network:         nil,
			Router:          nil,
			ExternalNetwork: nil,
			Subnets:         nil,
			Tags:            []string{"openshiftClusterID=" + r.Infra.Status.InfrastructureName},
		},
	}

	if err := r.Create(ctx, target); err != nil {
		return nil, fmt.Errorf("failed to create InfraCluster: %w", err)
	}

	log.Info(fmt.Sprintf("InfraCluster %s successfully created", klog.KObj(target)))

	return target, nil
}
