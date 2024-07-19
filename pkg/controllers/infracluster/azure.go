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
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	azurev1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	errUnableToGetAzureMAPIProviderSpec = errors.New("unable to get Azure MAPI ProviderSpec")
)

// ensureAzureCluster ensures the AzureCluster cluster object exists.
//
//nolint:funlen
func (r *InfraClusterController) ensureAzureCluster(ctx context.Context, log logr.Logger) (client.Object, error) {
	target := &azurev1.AzureCluster{ObjectMeta: metav1.ObjectMeta{
		Name:      r.Infra.Status.InfrastructureName,
		Namespace: defaultCAPINamespace,
	}}

	capzManagerBootstrapCredentialsKey := client.ObjectKey{Namespace: defaultCAPINamespace, Name: capzManagerBootstrapCredentials}
	capzManagerBootstrapCredentials := &corev1.Secret{}

	if err := r.Get(ctx, capzManagerBootstrapCredentialsKey, capzManagerBootstrapCredentials); err != nil {
		return nil, fmt.Errorf("failed to create Azure Cluster Secret: %w", err)
	}

	// Checking whether InfraCluster object exists. If it doesn't, create it.
	if err := r.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get InfraCluster: %w", err)
	} else if err == nil {
		return target, nil
	}

	log.Info(fmt.Sprintf("AzureCluster %s/%s does not exist, creating it", target.Namespace, target.Name))

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

	azureClientID, ok := capzManagerBootstrapCredentials.Data["azure_client_id"]
	if !ok {
		return nil, fmt.Errorf("failed to get azureClientID: %w", err)
	}

	azureTenantID, ok := capzManagerBootstrapCredentials.Data["azure_tenant_id"]
	if !ok {
		return nil, fmt.Errorf("failed to get azureTenantID: %w", err)
	}

	azureClusterIdentity := &azurev1.AzureClusterIdentity{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Infra.Status.InfrastructureName,
			Namespace: framework.CAPINamespace,
			Annotations: map[string]string{
				// The ManagedBy Annotation is set so CAPI infra providers ignore the InfraCluster object,
				// as that's managed externally, in this case by the cluster-capi-operator's infracluster controller.
				clusterv1.ManagedByAnnotation: managedByAnnotationValueClusterCAPIOperatorInfraClusterController,
			},
		},
		Spec: azurev1.AzureClusterIdentitySpec{
			Type:              azurev1.ServicePrincipal,
			AllowedNamespaces: &azurev1.AllowedNamespaces{NamespaceList: []string{defaultCAPINamespace}},
			ClientID:          string(azureClientID),
			TenantID:          string(azureTenantID),
			ClientSecret:      corev1.SecretReference{Name: r.Infra.Status.InfrastructureName, Namespace: defaultCAPINamespace},
		},
	}

	if err := r.Create(ctx, azureClusterIdentity); err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("failed to create Azure Cluster Identity: %w", err)
	}

	location, err := r.getAzureLocation(ctx)
	if err != nil {
		return nil, fmt.Errorf("error obtaining Azure Cluster location: %w", err)
	}

	target = &azurev1.AzureCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Infra.Status.InfrastructureName,
			Namespace: defaultCAPINamespace,
			// The ManagedBy Annotation is set so CAPI infra providers ignore the InfraCluster object,
			// as that's managed externally, in this case by this controller.
			Annotations: map[string]string{
				clusterv1.ManagedByAnnotation: managedByAnnotationValueClusterCAPIOperatorInfraClusterController,
			},
		},

		Spec: azurev1.AzureClusterSpec{
			AzureClusterClassSpec: azurev1.AzureClusterClassSpec{
				Location:         location,
				AzureEnvironment: "AzurePubliCloud",
				IdentityRef: &corev1.ObjectReference{
					Name:      r.Infra.Status.InfrastructureName,
					Namespace: defaultCAPINamespace,
					Kind:      "AzureClusterIdentity",
				},
			},
			ControlPlaneEndpoint: clusterv1.APIEndpoint{
				Host: apiURL.Hostname(),
				Port: int32(port),
			},
		},
	}

	if err := r.Create(ctx, target); err != nil {
		return nil, fmt.Errorf("failed to create InfraCluster: %w", err)
	}

	log.Info(fmt.Sprintf("InfraCluster '%s/%s' successfully created", defaultCAPINamespace, r.Infra.Status.InfrastructureName))

	return target, nil
}

// getAzureMAPIProviderSpec returns a Azure Machine ProviderSpec from the the cluster.
func getAzureMAPIProviderSpec(ctx context.Context, cl client.Client) (*mapiv1beta1.AzureMachineProviderSpec, error) {
	rawProviderSpec, err := getRawMAPIProviderSpec(ctx, cl)
	if err != nil {
		return nil, fmt.Errorf("unable to obtain MAPI ProviderSpec: %w", err)
	}

	providerSpec := &mapiv1beta1.AzureMachineProviderSpec{}
	if err := yaml.Unmarshal(rawProviderSpec, providerSpec); err != nil {
		return nil, fmt.Errorf("unable to unmarshal MAPI ProviderSpec: %w", err)
	}

	return providerSpec, nil
}

// getAzureLocation obtains the Azure Server address.
func (r *InfraClusterController) getAzureLocation(ctx context.Context) (string, error) {
	if r.Infra.Spec.PlatformSpec.Azure == nil {
		// Devise Azure location via MAPI providerSpec.
		machineSpec, err := getAzureMAPIProviderSpec(ctx, r.Client)
		if err != nil || machineSpec == nil {
			return "", errUnableToGetAzureMAPIProviderSpec
		}

		return machineSpec.Location, nil
	}

	return "", nil
}
