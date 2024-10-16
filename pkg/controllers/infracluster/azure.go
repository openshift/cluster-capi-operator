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
	corev1 "k8s.io/api/core/v1"
	cerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ptr "k8s.io/utils/ptr"
	azurev1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

var (
	errUnableToGetAzureMAPIProviderSpec = errors.New("unable to get Azure MAPI ProviderSpec")
	errUnableToGetAzureClientID         = errors.New("unable to get Azure Client ID")
	errUnableToGetAzureTenantID         = errors.New("unable to get Azure Tenant ID")
	errPlatformStatusNil                = errors.New("platform status should not be nil")
)

const (
	clusterSecretName               = "capz-manager-cluster-credential"    // #nosec G101
	capzManagerBootstrapCredentials = "capz-manager-bootstrap-credentials" // #nosec G101
)

// ensureAzureCluster ensures the AzureCluster cluster object exists.
func (r *InfraClusterController) ensureAzureCluster(ctx context.Context, log logr.Logger) (client.Object, error) {
	// Get the Azure Bootstrap Credentials Secret.
	// This is created by the Cluster Credential Operator and should always exist. We expect to always find it, if not, we error.
	capzManagerBootstrapSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: defaultCAPINamespace, Name: capzManagerBootstrapCredentials}, capzManagerBootstrapSecret); err != nil && !cerrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get Azure Boostrap Credentials Secret: %w", err)
	}

	if err := r.ensureClusterSecret(ctx, *capzManagerBootstrapSecret); err != nil {
		return nil, fmt.Errorf("error obtaining Azure Cluster Secret: %w", err)
	}

	if err := r.ensureClusterIdentity(ctx, *capzManagerBootstrapSecret); err != nil {
		return nil, fmt.Errorf("error ensuring Azure Cluster Identity: %w", err)
	}

	target := &azurev1.AzureCluster{ObjectMeta: metav1.ObjectMeta{
		Name:      r.Infra.Status.InfrastructureName,
		Namespace: defaultCAPINamespace,
	}}

	if err := r.ensureAzureInfraCluster(ctx, target, log); err != nil {
		return nil, fmt.Errorf("error ensuring Azure Infra Cluster: %w", err)
	}

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

// getAzureLocation obtains the Azure Location.
func (r *InfraClusterController) getAzureLocation(ctx context.Context) (string, error) {
	// Devise Azure location via MAPI providerSpec.
	machineSpec, err := getAzureMAPIProviderSpec(ctx, r.Client)
	if err != nil {
		return "", fmt.Errorf("error getting azure providerSpec: %w", err)
	}

	if machineSpec == nil {
		return "", errUnableToGetAzureMAPIProviderSpec
	}

	return machineSpec.Location, nil
}

// ensureClusterSecret ensures the AzureClusterSecret exists, and if it doesn't, creates it.
func (r *InfraClusterController) ensureClusterSecret(ctx context.Context, capzManagerBootstrapSecret corev1.Secret) error {
	// Get the Azure Cluster Secret.
	// CAPZ controllers expect a Secret Ref with a different data structure to what the secret we get from the Cluster Credential Operator above provides.
	// That is why we are creating a new secret below to copy the values to.
	clusterSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: defaultCAPINamespace, Name: clusterSecretName}, clusterSecret); err != nil && !cerrors.IsNotFound(err) {
		return fmt.Errorf("failed to get Azure Cluster Secret: %w", err)
	} else if err == nil {
		// When the object already exists, there's nothing to do.
		return nil
	}

	if err := createNewAzureSecret(ctx, r.Client, capzManagerBootstrapSecret.Data["azure_client_secret"]); err != nil {
		return fmt.Errorf("failed to create Azure Cluster secret: %w", err)
	}

	return nil
}

// createNewAzureSecret creates a new Azure Cluster Secret.
func createNewAzureSecret(ctx context.Context, cl client.Client, azureClientSecret []byte) error {
	azureSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterSecretName,
			Namespace: defaultCAPINamespace,
		},
		Immutable: ptr.To(true),
		Data: map[string][]byte{
			"clientSecret": azureClientSecret,
		},
	}

	if err := cl.Create(ctx, azureSecret); err != nil && !cerrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create new Azure Cluster Secret: %w", err)
	}

	return nil
}

// ensureClusterIdentity ensures the AzureClusterSecret exists, and if it doesn't, creates it.
func (r *InfraClusterController) ensureClusterIdentity(ctx context.Context, capzManagerBootstrapSecret corev1.Secret) error {
	azureClusterIdentity := &azurev1.AzureClusterIdentity{}
	// Get the Azure Cluster Identity.
	if err := r.Get(ctx, client.ObjectKey{Namespace: defaultCAPINamespace, Name: r.Infra.Status.InfrastructureName}, azureClusterIdentity); err != nil && !cerrors.IsNotFound(err) {
		return fmt.Errorf("failed to get Azure Cluster Identity: %w", err)
	} else if err == nil {
		// When the object already exists, there's nothing to do.
		return nil
	}

	if err := r.createAzureClusterIdentity(ctx, capzManagerBootstrapSecret); err != nil {
		return fmt.Errorf("failed to create new Azure Cluster Identity: %w", err)
	}

	return nil
}

// createNewAzureClusterIdenity creates a new AzureClusterIdentity.
func (r *InfraClusterController) createAzureClusterIdentity(ctx context.Context, capzManagerBootstrapSecret corev1.Secret) error {
	azureClientID, ok := capzManagerBootstrapSecret.Data["azure_client_id"]
	if !ok {
		return errUnableToGetAzureClientID
	}

	azureTenantID, ok := capzManagerBootstrapSecret.Data["azure_tenant_id"]
	if !ok {
		return errUnableToGetAzureTenantID
	}

	azureClusterIdentity := &azurev1.AzureClusterIdentity{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Infra.Status.InfrastructureName,
			Namespace: defaultCAPINamespace,
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
			ClientSecret:      corev1.SecretReference{Name: clusterSecretName, Namespace: defaultCAPINamespace},
		},
	}

	// The Azure Cluster Identtiy does not exist, so it needs to be created.
	if err := r.Create(ctx, azureClusterIdentity); err != nil && !cerrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create Azure Cluster Identity: %w", err)
	}

	return nil
}

// ensureAzureInfraCluster ensures the InfraCluster exists, and if it doesn't, creates it.
func (r *InfraClusterController) ensureAzureInfraCluster(ctx context.Context, target *azurev1.AzureCluster, log logr.Logger) error {
	if r.Infra.Status.PlatformStatus == nil {
		return errPlatformStatusNil
	}

	// Checking whether InfraCluster object exists. If it doesn't, create it.
	if err := r.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil && !cerrors.IsNotFound(err) {
		return fmt.Errorf("failed to get InfraCluster: %w", err)
	} else if err == nil {
		// When the object already exists, there's nothing to do.
		return nil
	}

	log.Info(fmt.Sprintf("AzureCluster %s/%s does not exist, creating it", target.Namespace, target.Name))

	apiURL, err := url.Parse(r.Infra.Status.APIServerInternalURL)
	if err != nil {
		return fmt.Errorf("failed to parse apiUrl: %w", err)
	}

	port, err := strconv.ParseInt(apiURL.Port(), 10, 32)
	if err != nil {
		return fmt.Errorf("failed to parse apiUrl port: %w", err)
	}

	providerSpec, err := getAzureMAPIProviderSpec(ctx, r.Client)
	if err != nil {
		return fmt.Errorf("error obtaining Azure Provider Spec: %w", err)
	}

	location, err := r.getAzureLocation(ctx)
	if err != nil {
		return fmt.Errorf("error obtaining Azure Cluster location: %w", err)
	}

	azureCluster := r.newAzureCluster(providerSpec, apiURL, port, location)
	if err := r.Create(ctx, azureCluster); err != nil {
		return fmt.Errorf("error creating New Azure Cluster: %w", err)
	}

	log.Info(fmt.Sprintf("InfraCluster '%s/%s' successfully created", defaultCAPINamespace, r.Infra.Status.InfrastructureName))

	return nil
}

// createNewAzureCluster creates a new Azure Infra Cluster.
func (r *InfraClusterController) newAzureCluster(providerSpec *mapiv1beta1.AzureMachineProviderSpec, apiURL *url.URL, port int64, location string) *azurev1.AzureCluster {
	return &azurev1.AzureCluster{
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
				AzureEnvironment: "AzurePublicCloud",
				IdentityRef: &corev1.ObjectReference{
					Name:      r.Infra.Status.InfrastructureName,
					Namespace: defaultCAPINamespace,
					Kind:      "AzureClusterIdentity",
				},
			},
			NetworkSpec: azurev1.NetworkSpec{
				NodeOutboundLB: &azurev1.LoadBalancerSpec{
					Name: r.Infra.Status.InfrastructureName,
					BackendPool: azurev1.BackendPool{
						Name: r.Infra.Status.InfrastructureName,
					},
				},
				Vnet: azurev1.VnetSpec{
					Name:          providerSpec.Vnet,
					ResourceGroup: providerSpec.NetworkResourceGroup,
				},
			},
			ResourceGroup: providerSpec.ResourceGroup,
			ControlPlaneEndpoint: clusterv1.APIEndpoint{
				Host: apiURL.Hostname(),
				Port: int32(port), //nolint:gosec
			},
		},
	}
}
