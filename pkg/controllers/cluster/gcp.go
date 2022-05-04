package cluster

import (
	"context"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gcpv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
)

func (r *ClusterReconciler) reconcileGCPCluster(ctx context.Context, gcpPlatformStatus *configv1.GCPPlatformStatus) error {
	if gcpPlatformStatus == nil {
		return errors.New("GCPPlatformStatus can't be nil")
	}

	gcpCluster := &gcpv1.GCPCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.clusterName,
			Namespace: controllers.DefaultManagedNamespace,
			Annotations: map[string]string{
				clusterv1.ManagedByAnnotation: "",
			},
		},
		Spec: gcpv1.GCPClusterSpec{
			Project: gcpPlatformStatus.ProjectID,
			Region:  gcpPlatformStatus.Region,
		},
	}

	gcpClusterCopy := gcpCluster.DeepCopy()
	if _, err := controllerutil.CreateOrPatch(ctx, r.Client, gcpCluster, func() error {
		gcpCluster.Annotations = gcpClusterCopy.Annotations
		gcpCluster.Spec.Project = gcpClusterCopy.Spec.Project
		gcpCluster.Spec.Region = gcpClusterCopy.Spec.Region
		return nil
	}); err != nil {
		return fmt.Errorf("unable to create or patch aws cluster: %v", err)
	}

	gcpCluster.Status.Ready = true
	if err := r.Status().Patch(ctx, gcpCluster, client.MergeFrom(gcpClusterCopy)); err != nil {
		return fmt.Errorf("unable to update gcp cluster status: %v", err)
	}

	return nil
}
