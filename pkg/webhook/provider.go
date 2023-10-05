package webhook

import (
	"context"
	"errors"

	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/cmd/clusterctl/api/v1alpha3"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type ProviderWebhook struct {
}

func (r *ProviderWebhook) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		WithValidator(r).
		For(&clusterv1.Provider{}).
		Complete()
}

var _ webhook.CustomValidator = &ProviderWebhook{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *ProviderWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	_, ok := obj.(*clusterv1.Provider)
	if !ok {
		panic("expected to get an of object of type v1alpha3.Provider")
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *ProviderWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	_, ok := oldObj.(*clusterv1.Provider)
	if !ok {
		panic("expected to get an of object of type v1alpha3.Provider")
	}
	_, ok = newObj.(*clusterv1.Provider)
	if !ok {
		panic("expected to get an of object of type v1alpha3.Provider")
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *ProviderWebhook) ValidateDelete(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, errors.New("deletion of cluster API providers is not allowed")
}
