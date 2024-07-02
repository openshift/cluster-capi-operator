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
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	cerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	errUnableToFindPasswordVSphereCredsSecret = errors.New("unable to find password in the VSphere credentials secret")
	errUnableToFindUsernameVSphereCredsSecret = errors.New("unable to find username in the VSphere credentials secret")
)

// ensureVSphereCluster ensures the VSphereCluster cluster object exists.
//
//nolint:funlen
func (r *InfraClusterController) ensureVSphereCluster(ctx context.Context, log logr.Logger) (client.Object, error) {
	vsphereServerAddr, err := r.getVSphereServerAddr(ctx)
	if err != nil {
		return nil, fmt.Errorf("error obtaining VSphere server address: %w", err)
	}

	// First make sure the CAPI VSphere credentials secret exists.
	if err := r.ensureVSphereSecret(ctx, vsphereServerAddr); err != nil {
		return nil, fmt.Errorf("unable to ensure CAPI VSphere credentials secret: %w", err)
	}

	target := &vspherev1.VSphereCluster{ObjectMeta: metav1.ObjectMeta{
		Name:      r.Infra.Status.InfrastructureName,
		Namespace: defaultCAPINamespace,
	}}

	// Checking whether InfraCluster object exists. If it doesn't, create it.
	if err := r.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil && !cerrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get InfraCluster: %w", err)
	} else if err == nil {
		return target, nil
	}

	log.Info(fmt.Sprintf("VSphereCluster %s/%s does not exist, creating it", target.Namespace, target.Name))

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

	target = &vspherev1.VSphereCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Infra.Status.InfrastructureName,
			Namespace: defaultCAPINamespace,
			// The ManagedBy Annotation is set so CAPI infra providers ignore the InfraCluster object,
			// as that's managed externally, in this case by this controller.
			Annotations: map[string]string{
				clusterv1.ManagedByAnnotation: managedByAnnotationValueClusterCAPIOperatorInfraClusterController,
			},
		},
		Spec: vspherev1.VSphereClusterSpec{
			Server: vsphereServerAddr,
			IdentityRef: &vspherev1.VSphereIdentityReference{
				Kind: "Secret",
				Name: r.Infra.Status.InfrastructureName,
			},
			ControlPlaneEndpoint: vspherev1.APIEndpoint{
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

// getVSphereMAPIProviderSpec returns a VSphere Machine ProviderSpec from the the cluster.
func getVSphereMAPIProviderSpec(ctx context.Context, cl client.Client) (*mapiv1beta1.VSphereMachineProviderSpec, error) {
	rawProviderSpec, err := getRawMAPIProviderSpec(ctx, cl)
	if err != nil {
		return nil, fmt.Errorf("unable to obtain MAPI ProviderSpec: %w", err)
	}

	providerSpec := &mapiv1beta1.VSphereMachineProviderSpec{}
	if err := yaml.Unmarshal(rawProviderSpec, providerSpec); err != nil {
		return nil, fmt.Errorf("unable to unmarshal MAPI ProviderSpec: %w", err)
	}

	return providerSpec, nil
}

// ensureVSphereSecret ensures the CAPI VSphere credentials secret exists.
func (r *InfraClusterController) ensureVSphereSecret(ctx context.Context, vsphereServerAddr string) error {
	vSphereSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Infra.Status.InfrastructureName,
			Namespace: defaultCAPINamespace,
		},
	}

	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(vSphereSecret), vSphereSecret); err != nil && !cerrors.IsNotFound(err) {
		return fmt.Errorf("failed to get CAPI VSphere credentials secret: %w", err)
	}

	username, password, err := r.getVSphereCredentials(ctx, vsphereServerAddr)
	if err != nil {
		return fmt.Errorf("unable to get VSphere credentials: %w", err)
	}

	vSphereSecret.StringData = map[string]string{
		"username": username,
		"password": password,
	}

	if err := r.Client.Create(ctx, vSphereSecret); err != nil && !cerrors.IsAlreadyExists(err) {
		return fmt.Errorf("unable to create CAPI VSphere credentials secret: %w", err)
	}

	return nil
}

// getVSphereCredentials obtains the VSphere credentials from the well-known credentials secret.
func (r *InfraClusterController) getVSphereCredentials(ctx context.Context, vsphereServerAddr string) (string, string, error) {
	vSphereCredentialsSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{
		Namespace: kubeSystemNamespace,
		Name:      vSphereCredentialsName,
	}, vSphereCredentialsSecret); err != nil {
		return "", "", fmt.Errorf("unable to get the VSphere credentials secret %s/%s: %w", kubeSystemNamespace, vSphereCredentialsName, err)
	}

	username, ok := vSphereCredentialsSecret.Data[fmt.Sprintf("%s.username", vsphereServerAddr)]
	if !ok {
		return "", "", fmt.Errorf("%w %s/%s", errUnableToFindUsernameVSphereCredsSecret, kubeSystemNamespace, vSphereCredentialsName)
	}

	password, ok := vSphereCredentialsSecret.Data[fmt.Sprintf("%s.password", vsphereServerAddr)]
	if !ok {
		return "", "", fmt.Errorf("%w %s/%s", errUnableToFindPasswordVSphereCredsSecret, kubeSystemNamespace, vSphereCredentialsName)
	}

	return string(username), string(password), nil
}

// getVSphereServerAddr obtains the VSphere Server address.
func (r *InfraClusterController) getVSphereServerAddr(ctx context.Context) (string, error) {
	if r.Infra.Spec.PlatformSpec.VSphere == nil || len(r.Infra.Spec.PlatformSpec.VSphere.VCenters) == 0 {
		// Devise VSphere server addr via MAPI providerSpec.
		machineSpec, err := getVSphereMAPIProviderSpec(ctx, r.Client)
		if err != nil {
			return "", fmt.Errorf("unable to get VSphere MAPI ProviderSpec: %w", err)
		}

		return machineSpec.Workspace.Server, nil
	}

	// Here we just take the first VCenter as the one we want CAPV to interact with.
	// Once CAPV supports multiple VCenters we can extend this.
	vCenter := r.Infra.Spec.PlatformSpec.VSphere.VCenters[0]

	return vCenter.Server, nil
}
