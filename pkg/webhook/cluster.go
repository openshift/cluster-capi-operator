package webhook

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

type ClusterWebhook struct {
}

func (r *ClusterWebhook) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		WithValidator(r).
		For(&v1beta1.Cluster{}).
		Complete()
}

var _ webhook.CustomValidator = &ClusterWebhook{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *ClusterWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) error {
	cluster, ok := obj.(*v1beta1.Cluster)
	if !ok {
		panic("expected to get an of object of type v1beta1.Cluster")
	}
	switch cluster.Spec.InfrastructureRef.Kind {
	case "AWSCluster", "AzureCluster", "GCPCluster":
	default:
		return fmt.Errorf("unsupported cluster infra provider kind: %s", cluster.Spec.InfrastructureRef.Kind)
	}

	return nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *ClusterWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) error {
	_, ok := oldObj.(*v1beta1.Cluster)
	if !ok {
		panic("expected to get an of object of type v1beta1.Cluster")
	}
	newCluster, ok := newObj.(*v1beta1.Cluster)
	if !ok {
		panic("expected to get an of object of type v1beta1.Cluster")
	}

	switch newCluster.Spec.InfrastructureRef.Kind {
	case "AWSCluster", "AzureCluster", "GCPCluster":
	default:
		return fmt.Errorf("unsupported cluster infra provider kind: %s", newCluster.Spec.InfrastructureRef.Kind)
	}

	return nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *ClusterWebhook) ValidateDelete(_ context.Context, obj runtime.Object) error {
	return errors.New("deletion of cluster is not allowed")
}
