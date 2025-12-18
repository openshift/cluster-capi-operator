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
	"strings"

	"github.com/go-logr/logr"
	cerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
)

var (
	// ErrNoControlPlaneLoadBalancerConfigured indicates there is no control plane load balancer configuration present.
	ErrNoControlPlaneLoadBalancerConfigured = errors.New("no control plane load balancer configured")
	// ErrNilProviderSpec indicates a nil provider spec raw extension.
	ErrNilProviderSpec = errors.New("provider spec is nil")
	// ErrInvalidNumberOfControlPlaneLoadBalancers indicates an invalid number of control plane load balancers has been configured.
	ErrInvalidNumberOfControlPlaneLoadBalancers = errors.New("invalid number of control plane load balancers")
	// ErrUnsupportedLoadBalancerType indicates an unsupported load balancer type.
	ErrUnsupportedLoadBalancerType = errors.New("unsupported load balancer type")
)

// ensureAWSCluster ensures the AWSCluster cluster object exists.
func (r *InfraClusterController) ensureAWSCluster(ctx context.Context, log logr.Logger) (client.Object, error) {
	awsCluster := &awsv1.AWSCluster{ObjectMeta: metav1.ObjectMeta{
		Name:      r.Infra.Status.InfrastructureName,
		Namespace: r.CAPINamespace,
	}}

	// Checking whether InfraCluster object exists. If it doesn't, create it.
	if err := r.Get(ctx, client.ObjectKeyFromObject(awsCluster), awsCluster); err != nil && !cerrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get InfraCluster: %w", err)
	} else if err == nil {
		return awsCluster, nil
	}

	log = log.WithValues("AWSCluster", klog.KObj(awsCluster))
	log.Info("AWSCluster does not exist, creating it")

	apiURL, err := url.Parse(r.Infra.Status.APIServerInternalURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse apiURL: %w", err)
	}

	port, err := strconv.ParseInt(apiURL.Port(), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse apiURL port: %w", err)
	}

	if r.Infra.Status.PlatformStatus == nil {
		return nil, fmt.Errorf("infrastructure PlatformStatus should not be nil: %w", err)
	}

	providerSpec, err := r.getAWSMAPIProviderSpec(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("unable to obtain MAPI ProviderSpec: %w", err)
	}

	awsCluster, err = r.newAWSCluster(providerSpec, apiURL, int32(port))
	if err != nil {
		return nil, fmt.Errorf("failed to get AWSCluster: %w", err)
	}

	if err := r.Create(ctx, awsCluster); err != nil {
		return nil, fmt.Errorf("failed to create AWSCluster: %w", err)
	}

	log.Info("AWSCluster successfully created")

	return awsCluster, nil
}

func (r *InfraClusterController) newAWSCluster(providerSpec *mapiv1beta1.AWSMachineProviderConfig, apiURL *url.URL, port int32) (*awsv1.AWSCluster, error) {
	controlPlaneLoadBalancer, secondaryControlPlaneLoadBalancer, err := extractLoadBalancerConfigFromMAPIAWSProviderSpec(providerSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to extract control plane load balancer configuration: %w", err)
	}

	target := &awsv1.AWSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Infra.Status.InfrastructureName,
			Namespace: r.CAPINamespace,
			// The ManagedBy Annotation is set so CAPI infra providers ignore the InfraCluster object,
			// as that's managed externally, in this case by this controller.
			Annotations: map[string]string{
				clusterv1.ManagedByAnnotation: managedByAnnotationValueClusterCAPIOperatorInfraClusterController,
			},
		},
		Spec: awsv1.AWSClusterSpec{
			Region: r.Infra.Status.PlatformStatus.AWS.Region,
			ControlPlaneEndpoint: clusterv1beta1.APIEndpoint{
				Host: apiURL.Hostname(),
				Port: port,
			},
			// This default IdentityRef will be created by the controlleridentitycreator controller.
			// Leaving it as nil works the same as this value, but fills the log with log messages
			// about the field being nil. Setting it also makes it more obvious how CAPA's configured.
			// We also hard-code this to be the ControllerIdentity since that's the only Identity method we support right now.
			IdentityRef: &awsv1.AWSIdentityReference{
				Kind: awsv1.ControllerIdentityKind,
				Name: "default",
			},
			// Set control plane load balancer configuration extracted from MAPI machines
			ControlPlaneLoadBalancer:          controlPlaneLoadBalancer,
			SecondaryControlPlaneLoadBalancer: secondaryControlPlaneLoadBalancer,
		},
	}

	return target, nil
}

func (r *InfraClusterController) getAWSMAPIProviderSpec(ctx context.Context, cl client.Client) (*mapiv1beta1.AWSMachineProviderConfig, error) {
	return getMAPIProviderSpec[mapiv1beta1.AWSMachineProviderConfig](ctx, cl, r.getRawMAPIProviderSpec)
}

// extractLoadBalancerConfigFromMAPIAWSProviderSpec extracts one or two control plane load balancers from a MAPI machine's provider spec.
// When two load balancers are present, the one whose name ends with "-int" is preferred as the primary (internal).
// The primary load balancer scheme is set to internal, secondary to internet-facing.
// Returns an error if zero or more than two load balancers are defined.
func extractLoadBalancerConfigFromMAPIAWSProviderSpec(providerSpec *mapiv1beta1.AWSMachineProviderConfig) (*awsv1.AWSLoadBalancerSpec, *awsv1.AWSLoadBalancerSpec, error) {
	if providerSpec == nil {
		return nil, nil, ErrNilProviderSpec
	}

	switch len(providerSpec.LoadBalancers) {
	case 0:
		return nil, nil, ErrNoControlPlaneLoadBalancerConfigured
	case 1:
		lbPrimary := providerSpec.LoadBalancers[0]

		lbType, err := convertMAPILoadBalancerTypeToCAPI(lbPrimary.Type)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to convert load balancer type: %w", err)
		}

		return &awsv1.AWSLoadBalancerSpec{
			Name:             &lbPrimary.Name,
			LoadBalancerType: lbType,
			Scheme:           &awsv1.ELBSchemeInternal,
		}, nil, nil
	case 2:
		lbFirst := providerSpec.LoadBalancers[0]
		lbSecond := providerSpec.LoadBalancers[1]
		// Prefer the load balancer with "-int" suffix as primary when two are present.
		if strings.HasSuffix(lbSecond.Name, "-int") && !strings.HasSuffix(lbFirst.Name, "-int") {
			lbFirst, lbSecond = lbSecond, lbFirst
		}

		lbTypeFirst, err := convertMAPILoadBalancerTypeToCAPI(lbFirst.Type)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to convert load balancer type: %w", err)
		}

		lbTypeSecond, err := convertMAPILoadBalancerTypeToCAPI(lbSecond.Type)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to convert load balancer type: %w", err)
		}

		return &awsv1.AWSLoadBalancerSpec{
				Name:             &lbFirst.Name,
				LoadBalancerType: lbTypeFirst,
				Scheme:           &awsv1.ELBSchemeInternal,
			}, &awsv1.AWSLoadBalancerSpec{
				Name:             &lbSecond.Name,
				LoadBalancerType: lbTypeSecond,
				Scheme:           &awsv1.ELBSchemeInternetFacing,
			}, nil
	default:
		return nil, nil, fmt.Errorf("%w: expected 1 or 2, got %d", ErrInvalidNumberOfControlPlaneLoadBalancers, len(providerSpec.LoadBalancers))
	}
}

// convertMAPILoadBalancerTypeToCAPI converts MAPI AWSLoadBalancerType to CAPI LoadBalancerType.
func convertMAPILoadBalancerTypeToCAPI(mapiType mapiv1beta1.AWSLoadBalancerType) (awsv1.LoadBalancerType, error) {
	switch mapiType {
	case mapiv1beta1.ClassicLoadBalancerType:
		return awsv1.LoadBalancerTypeClassic, nil
	case mapiv1beta1.NetworkLoadBalancerType:
		return awsv1.LoadBalancerTypeNLB, nil
	default:
		return "", fmt.Errorf("%w: unknown load balancer type: %s", ErrUnsupportedLoadBalancerType, mapiType)
	}
}
