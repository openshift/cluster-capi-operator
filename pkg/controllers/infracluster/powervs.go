/*
Copyright 2024 Red Hat, Inc.

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
	"net/url"
	"strconv"

	"github.com/go-logr/logr"

	cerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mapiv1 "github.com/openshift/api/machine/v1"
)

var (
	errInvalidPlatformStatus      = errors.New("infrastructure PlatformStatus should not be nil")
	errUnKnownServiceInstanceType = errors.New("unknown service instance type")
	errUnKnownNetworkType         = errors.New("unknown network type")
)

// ensureIBMPowerVSCluster ensures the IBMPowerVSCluster object exists.
//
//nolint:funlen
func (r *InfraClusterController) ensureIBMPowerVSCluster(ctx context.Context, log logr.Logger) (client.Object, error) {
	target := &ibmpowervsv1.IBMPowerVSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Infra.Status.InfrastructureName,
			Namespace: r.CAPINamespace,
		}}

	// Checking whether InfraCluster object exists. If it doesn't, create it.

	if err := r.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil && !cerrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get InfraCluster: %w", err)
	} else if err == nil {
		return target, nil
	}

	log.Info(fmt.Sprintf("IBMPowerVSCluster %s does not exist, creating it", klog.KObj(target)))

	apiURL, err := url.Parse(r.Infra.Status.APIServerInternalURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse apiURL: %w", err)
	}

	port, err := strconv.ParseInt(apiURL.Port(), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse apiURL port: %w", err)
	}

	if r.Infra.Status.PlatformStatus == nil {
		return nil, errInvalidPlatformStatus
	}

	// Derive service instance and network from machine spec

	machineSpec, err := r.getPowerVSMAPIProviderSpec(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("unable to get PowerVS MAPI ProviderSpec: %w", err)
	}

	serviceInstance, err := getPowerVSServiceInstance(machineSpec.ServiceInstance)
	if err != nil {
		return nil, fmt.Errorf("unable to get PowerVS service instance: %w", err)
	}

	network, err := getPowerVSNetwork(machineSpec.Network)
	if err != nil {
		return nil, fmt.Errorf("unable to get PowerVS network %w", err)
	}

	target = &ibmpowervsv1.IBMPowerVSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Infra.Status.InfrastructureName,
			Namespace: r.CAPINamespace,
			// The ManagedBy Annotation is set so CAPI infra providers ignore the InfraCluster object,
			// as that's managed externally, in this case by this controller.
			Annotations: map[string]string{
				clusterv1.ManagedByAnnotation: managedByAnnotationValueClusterCAPIOperatorInfraClusterController,
			},
		},
		Spec: ibmpowervsv1.IBMPowerVSClusterSpec{
			ControlPlaneEndpoint: clusterv1beta1.APIEndpoint{
				Host: apiURL.Hostname(),
				Port: int32(port),
			},
			ServiceInstance: serviceInstance,
			Network:         network,
		},
	}

	if err := r.Create(ctx, target); err != nil {
		return nil, fmt.Errorf("failed to create InfraCluster: %w", err)
	}

	log.Info(fmt.Sprintf("InfraCluster %s successfully created", klog.KObj(target)))

	return target, nil
}

// getPowerVSMAPIProviderSpec returns a PowerVS Machine ProviderSpec from the the cluster.
func (r *InfraClusterController) getPowerVSMAPIProviderSpec(ctx context.Context, cl client.Client) (*mapiv1.PowerVSMachineProviderConfig, error) {
	return getMAPIProviderSpec[mapiv1.PowerVSMachineProviderConfig](ctx, cl, r.getRawMAPIProviderSpec)
}

func getPowerVSServiceInstance(serviceInstance mapiv1.PowerVSResource) (*ibmpowervsv1.IBMPowerVSResourceReference, error) {
	switch serviceInstance.Type {
	case mapiv1.PowerVSResourceTypeID:
		return &ibmpowervsv1.IBMPowerVSResourceReference{ID: serviceInstance.ID}, nil
	case mapiv1.PowerVSResourceTypeName:
		return &ibmpowervsv1.IBMPowerVSResourceReference{Name: serviceInstance.Name}, nil
	case mapiv1.PowerVSResourceTypeRegEx:
		return &ibmpowervsv1.IBMPowerVSResourceReference{RegEx: serviceInstance.RegEx}, nil
	default:
		return nil, fmt.Errorf("%w: %s", errUnKnownServiceInstanceType, serviceInstance.Type)
	}
}

func getPowerVSNetwork(network mapiv1.PowerVSResource) (ibmpowervsv1.IBMPowerVSResourceReference, error) {
	switch network.Type {
	case mapiv1.PowerVSResourceTypeID:
		return ibmpowervsv1.IBMPowerVSResourceReference{ID: network.ID}, nil
	case mapiv1.PowerVSResourceTypeName:
		return ibmpowervsv1.IBMPowerVSResourceReference{Name: network.Name}, nil
	case mapiv1.PowerVSResourceTypeRegEx:
		return ibmpowervsv1.IBMPowerVSResourceReference{RegEx: network.RegEx}, nil
	default:
		return ibmpowervsv1.IBMPowerVSResourceReference{}, fmt.Errorf("%w: %s", errUnKnownNetworkType, network.Type)
	}
}
