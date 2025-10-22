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
	corev1 "k8s.io/api/core/v1"
	cerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	errInfraPlatformStatusNil = errors.New("infrastructure PlatformStatus should not be nil")
)

// ensureNutanixCluster ensures the NutanixCluster cluster object exists.
func (r *InfraClusterController) ensureNutanixCluster(ctx context.Context, log logr.Logger) (client.Object, error) {
	target := &nutanixv1.NutanixCluster{ObjectMeta: metav1.ObjectMeta{
		Name:      r.Infra.Status.InfrastructureName,
		Namespace: defaultCAPINamespace,
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
		return nil, fmt.Errorf("failed to parse apiURL: %w", err)
	}

	port, err := strconv.ParseInt(apiURL.Port(), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse apiURL port: %w", err)
	}

	if r.Infra.Status.PlatformStatus == nil {
		return nil, errInfraPlatformStatusNil
	}

	// Build the NutanixCluster spec
	clusterSpec := r.buildNutanixClusterSpec(apiURL.Hostname(), int32(port))

	target = &nutanixv1.NutanixCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Infra.Status.InfrastructureName,
			Namespace: defaultCAPINamespace,
			// The ManagedBy Annotation is set so CAPI infra providers ignore the InfraCluster object,
			// as that's managed externally, in this case by this controller.
			Annotations: map[string]string{
				clusterv1.ManagedByAnnotation: managedByAnnotationValueClusterCAPIOperatorInfraClusterController,
			},
		},
		Spec: clusterSpec,
	}

	if err := r.Create(ctx, target); err != nil {
		return nil, fmt.Errorf("failed to create InfraCluster: %w", err)
	}

	log.Info(fmt.Sprintf("InfraCluster '%s/%s' successfully created", defaultCAPINamespace, r.Infra.Status.InfrastructureName))

	return target, nil
}

// buildNutanixClusterSpec builds the NutanixClusterSpec from the Infrastructure object.
func (r *InfraClusterController) buildNutanixClusterSpec(host string, port int32) nutanixv1.NutanixClusterSpec {
	clusterSpec := nutanixv1.NutanixClusterSpec{
		ControlPlaneEndpoint: clusterv1.APIEndpoint{
			Host: host,
			Port: port,
		},
	}

	// Add PrismCentral configuration if available in the Infrastructure spec
	if r.Infra.Spec.PlatformSpec.Nutanix != nil && r.Infra.Spec.PlatformSpec.Nutanix.PrismCentral.Address != "" {
		clusterSpec.PrismCentral = &credentialTypes.NutanixPrismEndpoint{
			// Address holds the IP address or FQDN of the Nutanix Prism Central
			Address: r.Infra.Spec.PlatformSpec.Nutanix.PrismCentral.Address,
			// Port holds the port number of the Nutanix Prism Central
			Port: r.Infra.Spec.PlatformSpec.Nutanix.PrismCentral.Port,
		}
	}

	// Add failure domains if available in the Infrastructure spec
	if r.Infra.Spec.PlatformSpec.Nutanix != nil && len(r.Infra.Spec.PlatformSpec.Nutanix.FailureDomains) > 0 {
		failureDomains := make([]corev1.LocalObjectReference, 0, len(r.Infra.Spec.PlatformSpec.Nutanix.FailureDomains))
		for _, fd := range r.Infra.Spec.PlatformSpec.Nutanix.FailureDomains {
			failureDomains = append(failureDomains, corev1.LocalObjectReference{
				Name: fd.Name,
			})
		}

		clusterSpec.ControlPlaneFailureDomains = failureDomains
	}

	return clusterSpec
}
