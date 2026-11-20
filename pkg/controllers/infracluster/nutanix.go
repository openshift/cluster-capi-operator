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
	"net/url"
	"strconv"

	"github.com/go-logr/logr"
	nutanixv1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	credentialTypes "github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1"
	corev1 "k8s.io/api/core/v1"
	cerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	errNutanixPrismCentralNotSet      = errors.New("infrastructure PlatformSpec.Nutanix.PrismCentral is not set")
	errNutanixCredentialsSecretNotSet = errors.New("nutanix Machine API credentials secret is not set")
	errNutanixCredentialsDataMissing  = errors.New("nutanix credentials secret is missing credentials data")
)

// ensureNutanixCluster ensures the NutanixCluster cluster object exists.
func (r *InfraClusterController) ensureNutanixCluster(ctx context.Context, log logr.Logger) (client.Object, error) {
	if r.Infra.Spec.PlatformSpec.Nutanix == nil {
		return nil, errNutanixPrismCentralNotSet
	}

	// The provider looks up credentialRef in the NutanixCluster namespace as soon as the
	// cluster exists. Copy the Machine API credentials before creating or returning it.
	if err := r.ensureNutanixCredentialsSecret(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure Nutanix credentials secret: %w", err)
	}

	target := &nutanixv1.NutanixCluster{ObjectMeta: metav1.ObjectMeta{
		Name:      r.Infra.Status.InfrastructureName,
		Namespace: r.CAPINamespace,
	}}

	// Checking whether InfraCluster object exists. If it doesn't, create it.
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

	target = newNutanixCluster(r.Infra.Status.InfrastructureName, r.CAPINamespace, apiURL.Hostname(), int32(port), r.Infra.Spec.PlatformSpec.Nutanix.PrismCentral)

	if err := r.Create(ctx, target); err != nil {
		return nil, fmt.Errorf("failed to create InfraCluster: %w", err)
	}

	log.Info(fmt.Sprintf("InfraCluster '%s/%s' successfully created", r.CAPINamespace, r.Infra.Status.InfrastructureName))

	return target, nil
}

// newNutanixCluster builds the NutanixCluster managed by this controller.
func newNutanixCluster(name, namespace, host string, port int32, prismCentral configv1.NutanixPrismEndpoint) *nutanixv1.NutanixCluster {
	return &nutanixv1.NutanixCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			// The ManagedBy Annotation is set so CAPI infra providers ignore the InfraCluster object,
			// as that's managed externally, in this case by this controller.
			Annotations: map[string]string{
				clusterv1.ManagedByAnnotation: managedByAnnotationValueClusterCAPIOperatorInfraClusterController,
			},
		},
		Spec: nutanixv1.NutanixClusterSpec{
			ControlPlaneEndpoint: clusterv1beta1.APIEndpoint{
				Host: host,
				Port: port,
			},
			PrismCentral: &credentialTypes.NutanixPrismEndpoint{
				Address: prismCentral.Address,
				Port:    prismCentral.Port,
				// credentialRef is resolved in the NutanixCluster namespace by name.
				// ensureNutanixCredentialsSecret creates that Secret from the Machine API credentials.
				CredentialRef: &credentialTypes.NutanixCredentialReference{
					Kind:      credentialTypes.SecretKind,
					Name:      name,
					Namespace: namespace,
				},
			},
		},
	}
}

// ensureNutanixCredentialsSecret copies the Machine API Prism Central credentials into the
// Secret referenced by NutanixCluster.spec.prismCentral.credentialRef.
func (r *InfraClusterController) ensureNutanixCredentialsSecret(ctx context.Context) error {
	target := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      r.Infra.Status.InfrastructureName,
		Namespace: r.CAPINamespace,
	}}

	if err := r.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil && !cerrors.IsNotFound(err) {
		return fmt.Errorf("failed to get Cluster API Nutanix credentials secret: %w", err)
	} else if err == nil {
		if len(target.Data[credentialTypes.KeyName]) == 0 {
			return fmt.Errorf("%w: %s/%s", errNutanixCredentialsDataMissing, target.Namespace, target.Name)
		}

		return nil
	}

	source, err := r.getNutanixMAPICredentialsSecret(ctx)
	if err != nil {
		return err
	}

	credentialsData := source.Data[credentialTypes.KeyName]
	if len(credentialsData) == 0 {
		return fmt.Errorf("%w: %s/%s", errNutanixCredentialsDataMissing, source.Namespace, source.Name)
	}

	target = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Infra.Status.InfrastructureName,
			Namespace: r.CAPINamespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			credentialTypes.KeyName: credentialsData,
		},
	}

	if err := r.Create(ctx, target); err != nil && !cerrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create Cluster API Nutanix credentials secret: %w", err)
	}

	return nil
}

// getNutanixMAPICredentialsSecret returns the Machine API secret referenced by a control plane provider spec.
func (r *InfraClusterController) getNutanixMAPICredentialsSecret(ctx context.Context) (*corev1.Secret, error) {
	providerSpec, err := getMAPIProviderSpec[mapiv1.NutanixMachineProviderConfig](ctx, r.Client, r.getRawMAPIProviderSpec)
	if err != nil {
		return nil, fmt.Errorf("unable to get Nutanix Machine API provider spec: %w", err)
	}

	if providerSpec.CredentialsSecret == nil || providerSpec.CredentialsSecret.Name == "" {
		return nil, errNutanixCredentialsSecretNotSet
	}

	mapiNamespace := r.MAPINamespace
	if mapiNamespace == "" {
		mapiNamespace = defaultMAPINamespace
	}

	source := &corev1.Secret{}
	sourceKey := types.NamespacedName{Namespace: mapiNamespace, Name: providerSpec.CredentialsSecret.Name}

	if err := r.Get(ctx, sourceKey, source); err != nil {
		return nil, fmt.Errorf("failed to get Machine API Nutanix credentials secret %s: %w", sourceKey, err)
	}

	return source, nil
}
