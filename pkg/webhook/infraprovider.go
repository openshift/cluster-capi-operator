package webhook

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cluster-api-operator/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	configv1 "github.com/openshift/api/config/v1"
)

type InfrastructureProviderWebhook struct {
	Platform configv1.PlatformType
}

func (r *InfrastructureProviderWebhook) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		WithValidator(r).
		For(&v1alpha1.InfrastructureProvider{}).
		Complete()
}

var _ webhook.CustomValidator = &InfrastructureProviderWebhook{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *InfrastructureProviderWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) error {
	infraProvider, ok := obj.(*v1alpha1.InfrastructureProvider)
	if !ok {
		panic("expected to get an of object of type v1alpha1.InfrastructureProvider")
	}

	switch r.Platform {
	case configv1.AWSPlatformType:
		if infraProvider.Name != "aws" {
			return fmt.Errorf("incorrect infra provider name for AWS platform: %s", infraProvider.Name)
		}
	case configv1.AzurePlatformType:
		if infraProvider.Name != "azure" {
			return fmt.Errorf("incorrect infra provider name for Azure platform: %s", infraProvider.Name)
		}
	case configv1.GCPPlatformType:
		if infraProvider.Name != "gcp" {
			return fmt.Errorf("incorrect infra provider name for GCP platform: %s", infraProvider.Name)
		}
	case configv1.PowerVSPlatformType:
		// for Power VS the upstream cluster api provider name is ibmcloud
		// https://github.com/kubernetes-sigs/cluster-api/blob/main/cmd/clusterctl/client/config/providers_client.go#L218-L222
		if infraProvider.Name != "ibmcloud" {
			return fmt.Errorf("incorrect infra provider name for PowerVS platform: %s", infraProvider.Name)
		}
	default:
		return errors.New("platform not supported, skipping infra cluster controller setup")
	}

	return nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *InfrastructureProviderWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) error {
	_, ok := oldObj.(*v1alpha1.InfrastructureProvider)
	if !ok {
		panic("expected to get an of object of type v1alpha1.InfrastructureProvider")
	}
	newInfraProvider, ok := newObj.(*v1alpha1.InfrastructureProvider)
	if !ok {
		panic("expected to get an of object of type v1alpha1.InfrastructureProvider")
	}

	switch r.Platform {
	case configv1.AWSPlatformType:
		if newInfraProvider.Name != "aws" {
			return fmt.Errorf("incorrect infra provider name for AWS platform: %s", newInfraProvider.Name)
		}
	case configv1.AzurePlatformType:
		if newInfraProvider.Name != "azure" {
			return fmt.Errorf("incorrect infra provider name for Azure platform: %s", newInfraProvider.Name)
		}
	case configv1.GCPPlatformType:
		if newInfraProvider.Name != "gcp" {
			return fmt.Errorf("incorrect infra provider name for GCP platform: %s", newInfraProvider.Name)
		}
	case configv1.PowerVSPlatformType:
		// for Power VS the upstream cluster api provider name is ibmcloud
		// https://github.com/kubernetes-sigs/cluster-api/blob/main/cmd/clusterctl/client/config/providers_client.go#L218-L222
		if newInfraProvider.Name != "ibmcloud" {
			return fmt.Errorf("incorrect infra provider name for PowerVS platform: %s", newInfraProvider.Name)
		}
	default:
		return errors.New("platform not supported, skipping infra cluster controller setup")
	}

	return nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *InfrastructureProviderWebhook) ValidateDelete(_ context.Context, obj runtime.Object) error {
	return errors.New("deletion of infrastructure provider is not allowed")
}
