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
	"fmt"
	"net/url"
	"strconv"

	"github.com/go-logr/logr"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	cerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gcpv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ensureGCPCluster ensures the GCPCluster cluster object exists.
//
//nolint:funlen
func (r *InfraClusterController) ensureGCPCluster(ctx context.Context, log logr.Logger) (client.Object, error) {
	target := &gcpv1.GCPCluster{ObjectMeta: metav1.ObjectMeta{
		Name:      r.Infra.Status.InfrastructureName,
		Namespace: r.CAPINamespace,
	}}

	// Checking whether InfraCluster object exists. If it doesn't, create it.
	if err := r.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil && !cerrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get InfraCluster: %w", err)
	} else if err == nil {
		return target, nil
	}

	log.Info(fmt.Sprintf("GCPCluster %s/%s does not exist, creating it", target.Namespace, target.Name))

	apiURL, err := url.Parse(r.Infra.Status.APIServerInternalURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse apiUrl: %w", err)
	}

	port, err := strconv.ParseInt(apiURL.Port(), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse apiUrl port: %w", err)
	}

	if r.Infra.Status.PlatformStatus == nil {
		return nil, fmt.Errorf("infrastructure PlatformStatus should not be nil: %w", err)
	}

	gcpProjectID, err := r.getGCPProjectID(ctx)
	if err != nil {
		return nil, fmt.Errorf("error obtaining GCP Project ID: %w", err)
	}

	providerSpec, err := r.getGCPMAPIProviderSpec(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("error obtaining GCP Provider Spec: %w", err)
	}

	target = &gcpv1.GCPCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Infra.Status.InfrastructureName,
			Namespace: r.CAPINamespace,
			// The ManagedBy Annotation is set so CAPI infra providers ignore the InfraCluster object,
			// as that's managed externally, in this case by this controller.
			Annotations: map[string]string{
				clusterv1.ManagedByAnnotation: managedByAnnotationValueClusterCAPIOperatorInfraClusterController,
			},
		},
		Spec: gcpv1.GCPClusterSpec{
			Network: gcpv1.NetworkSpec{
				Name: &providerSpec.NetworkInterfaces[0].Network,
			},
			Region:  r.Infra.Status.PlatformStatus.GCP.Region,
			Project: gcpProjectID,
			ControlPlaneEndpoint: clusterv1beta1.APIEndpoint{
				Host: apiURL.Hostname(),
				Port: int32(port),
			},
		},
	}

	if err := r.Create(ctx, target); err != nil {
		return nil, fmt.Errorf("failed to create InfraCluster: %w", err)
	}

	log.Info(fmt.Sprintf("InfraCluster '%s/%s' successfully created", r.CAPINamespace, r.Infra.Status.InfrastructureName))

	return target, nil
}

// getGCPMAPIProviderSpec returns a GCP Machine ProviderSpec from the the cluster.
func (r *InfraClusterController) getGCPMAPIProviderSpec(ctx context.Context, cl client.Client) (*mapiv1beta1.GCPMachineProviderSpec, error) {
	return getMAPIProviderSpec[mapiv1beta1.GCPMachineProviderSpec](ctx, cl, r.getRawMAPIProviderSpec)
}

// getGCPProjectID obtains the GCP Project ID.
func (r *InfraClusterController) getGCPProjectID(ctx context.Context) (string, error) {
	if r.Infra.Spec.PlatformSpec.GCP == nil || len(r.Infra.Status.PlatformStatus.GCP.ProjectID) == 0 {
		// Devise GCP Project ID via MAPI providerSpec.
		machineSpec, err := r.getGCPMAPIProviderSpec(ctx, r.Client)
		if err != nil || machineSpec == nil {
			return "", fmt.Errorf("unable to get GCP MAPI ProviderSpec: %w", err)
		}

		return machineSpec.ProjectID, nil
	}

	projectID := r.Infra.Status.PlatformStatus.GCP.ProjectID

	return projectID, nil
}
