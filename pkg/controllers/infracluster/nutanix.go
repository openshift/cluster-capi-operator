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
	nutanixv1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	credentialTypes "github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	mapiv1beta1 "github.com/openshift/api/machine/v1"
	cerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	// Define the kube-system or central namespace for Nutanix credentials secret if used
	nutanixCredentialsNamespace = "openshift-machine-api"
)

// ensureNutanixCluster ensures the NutanixCluster cluster object exists.
//
//nolint:funlen
func (r *InfraClusterController) ensureNutanixCluster(ctx context.Context, log logr.Logger) (client.Object, error) {
	prismCentralAddr, prismCentralPort, err := r.getPrismCentralAddress()
	if err != nil {
		return nil, fmt.Errorf("error obtaining Prism Central address: %w", err)
	}

	target := &nutanixv1.NutanixCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Infra.Status.InfrastructureName,
			Namespace: defaultCAPINamespace,
		},
	}

	// Check if NutanixCluster exists; if so, return it
	if err := r.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil && !cerrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get InfraCluster: %w", err)
	} else if err == nil {
		return target, nil
	}

	log.Info(fmt.Sprintf("NutanixCluster %s/%s does not exist, creating it", target.Namespace, target.Name))

	apiURL, err := url.Parse(r.Infra.Status.APIServerInternalURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse apiUrl: %w", err)
	}

	port, err := strconv.ParseInt(apiURL.Port(), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse apiUrl port: %w", err)
	}

	if r.Infra.Status.PlatformStatus == nil {
		return nil, fmt.Errorf("infrastructure PlatformStatus should not be nil")
	}

	// Get Nutanix provider spec (to extract Prism Central credentials ref & other details)
	providerSpec, err := getNutanixMAPIProviderSpec(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("failed to get Nutanix provider spec: %w", err)
	}

	// Map CoreV1 LocalObjectReference to NutanixCredentialReference if needed
	var credRef *credentialTypes.NutanixCredentialReference
	if providerSpec.CredentialsSecret != nil {
		credRef = &credentialTypes.NutanixCredentialReference{
			Name:      providerSpec.CredentialsSecret.Name,
			Namespace: nutanixCredentialsNamespace,
			Kind:      credentialTypes.SecretKind,
		}
	}

	target = &nutanixv1.NutanixCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Infra.Status.InfrastructureName,
			Namespace: defaultCAPINamespace,
			Annotations: map[string]string{
				clusterv1.ManagedByAnnotation: managedByAnnotationValueClusterCAPIOperatorInfraClusterController,
			},
		},
		Spec: nutanixv1.NutanixClusterSpec{
			PrismCentral: &credentialTypes.NutanixPrismEndpoint{
				Address:       prismCentralAddr,
				Port:          prismCentralPort,
				Insecure:      false, // assume secure connection, adjust if needed
				CredentialRef: credRef,
			},
			// ControlPlaneEndpoint is where API server of the cluster listens
			ControlPlaneEndpoint: clusterv1.APIEndpoint{
				Host: apiURL.Hostname(),
				Port: int32(port),
			},
			FailureDomains: r.getPrismCentralFailureDomain(),
		},
	}

	if err := r.Create(ctx, target); err != nil {
		return nil, fmt.Errorf("failed to create Nutanix InfraCluster: %w", err)
	}

	log.Info(fmt.Sprintf("InfraCluster '%s/%s' successfully created", defaultCAPINamespace, r.Infra.Status.InfrastructureName))

	return target, nil
}

// getNutanixMAPIProviderSpec returns a Nutanix Machine ProviderConfig from the cluster.
func getNutanixMAPIProviderSpec(ctx context.Context, cl client.Client) (*mapiv1beta1.NutanixMachineProviderConfig, error) {
	rawProviderSpec, err := getRawMAPIProviderSpec(ctx, cl)
	if err != nil {
		return nil, fmt.Errorf("unable to obtain MAPI ProviderSpec: %w", err)
	}
	providerSpec := &mapiv1beta1.NutanixMachineProviderConfig{}
	if err := yaml.Unmarshal(rawProviderSpec, providerSpec); err != nil {
		return nil, fmt.Errorf("unable to unmarshal MAPI ProviderSpec: %w", err)
	}
	return providerSpec, nil
}

// getPrismCentralAddress obtains the Prism Central server address for Nutanix from Infra or ProviderSpec.
// Falls back to provider spec if no PlatformSpec provided.
func (r *InfraClusterController) getPrismCentralAddress() (string, int32, error) {
	if r.Infra.Spec.PlatformSpec.Nutanix == nil || r.Infra.Spec.PlatformSpec.Nutanix.PrismCentral.Address == "" {
		return "", int32(0), fmt.Errorf("prismCentral address not set in infrastructure PlatformSpec or ProviderSpec")
	}
	return r.Infra.Spec.PlatformSpec.Nutanix.PrismCentral.Address, r.Infra.Spec.PlatformSpec.Nutanix.PrismCentral.Port, nil
}

func (r *InfraClusterController) getPrismCentralFailureDomain() []nutanixv1.NutanixFailureDomain {
	if r.Infra.Spec.PlatformSpec.Nutanix.FailureDomains == nil {
		return nil
	}
	out := make([]nutanixv1.NutanixFailureDomain, len(r.Infra.Spec.PlatformSpec.Nutanix.FailureDomains))
	for i, domain := range r.Infra.Spec.PlatformSpec.Nutanix.FailureDomains {
		subnets := make([]nutanixv1.NutanixResourceIdentifier, len(domain.Subnets))
		for j, subnet := range domain.Subnets {
			subnets[j] = nutanixv1.NutanixResourceIdentifier{
				Type: nutanixv1.NutanixIdentifierType(subnet.Type),
				UUID: subnet.UUID,
				Name: subnet.Name,
			}
		}
		out[i] = nutanixv1.NutanixFailureDomain{
			Name: domain.Name,
			Cluster: nutanixv1.NutanixResourceIdentifier{
				Type: nutanixv1.NutanixIdentifierType(domain.Cluster.Type),
				UUID: domain.Cluster.UUID,
				Name: domain.Cluster.Name,
			},
			Subnets: subnets,
		}
	}
	return out
}
