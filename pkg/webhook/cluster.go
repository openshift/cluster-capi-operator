package webhook

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	openshiftCAPINamespace = "openshift-cluster-api"
)

type ClusterWebhook struct {
	client client.Client
}

func (r *ClusterWebhook) SetupWebhookWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		WithValidator(r).
		For(&v1beta1.Cluster{}).
		Complete()
}

var _ webhook.CustomValidator = &ClusterWebhook{}

// fetchInfrastructureObject fetches the Infrastructure object from the cluster.
func (r *ClusterWebhook) fetchInfrastructureObject(ctx context.Context) (*configv1.Infrastructure, error) {
	infrastructureObjectKey := client.ObjectKey{Name: "cluster", Namespace: "default"}

	infrastructureObject := configv1.Infrastructure{}
	err := r.client.Get(ctx, infrastructureObjectKey, &infrastructureObject)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Infrastructure object: %w", err)
	}

	return &infrastructureObject, nil
}

// In openshift-cluster-api allow only one Cluster object to be created. This Cluster manages the cluster we are running on.
func (r *ClusterWebhook) validateClusterName(ctx context.Context, cluster *v1beta1.Cluster) error {
	if cluster.Namespace != openshiftCAPINamespace {
		return nil
	}

	infrastructureObject, err := r.fetchInfrastructureObject(ctx)
	if err != nil {
		return fmt.Errorf("cluster in %s namespace must be named <infrastructure_id>. Failed to obtain name from Infrastructure object for validation: %w", openshiftCAPINamespace, err)
	}

	infrastructureName := infrastructureObject.Status.InfrastructureName
	if cluster.ObjectMeta.Name != infrastructureName {
		return fmt.Errorf("cluster name must be %s in %s namespace", infrastructureName, openshiftCAPINamespace)
	}

	return nil
}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *ClusterWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) error {
	cluster, ok := obj.(*v1beta1.Cluster)
	if !ok {
		panic("expected to get an of object of type v1beta1.Cluster")
	}

	errs := []error{}
	infrastructureRefPath := field.NewPath("spec", "infrastructureRef")
	if cluster.Spec.InfrastructureRef == nil {
		return field.Required(infrastructureRefPath, "infrastructureRef is required")
	}

	errs = append(errs, r.validateClusterName(ctx, cluster))

	if len(errs) > 0 {
		return utilerrors.NewAggregate(errs)
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

	infrastructureRefPath := field.NewPath("spec", "infrastructureRef")
	if newCluster.Spec.InfrastructureRef == nil {
		return field.Required(infrastructureRefPath, "infrastructureRef is required")
	}

	return nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *ClusterWebhook) ValidateDelete(_ context.Context, obj runtime.Object) error {
	return errors.New("deletion of cluster is not allowed")
}
