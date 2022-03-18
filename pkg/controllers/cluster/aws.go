package cluster

import (
	"context"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
)

func (r *ClusterReconciler) reconcileAWSCluster(ctx context.Context, awsPlatformStatus *configv1.AWSPlatformStatus) error {
	if awsPlatformStatus == nil {
		return errors.New("AWSPlatformStatus can't be nil")
	}

	awsCluster := &awsv1.AWSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.clusterName,
			Namespace: controllers.DefaultManagedNamespace,
			Annotations: map[string]string{
				clusterv1.ManagedByAnnotation: "",
			},
		},
		Spec: awsv1.AWSClusterSpec{
			Region: awsPlatformStatus.Region,
		},
	}

	awsClusterCopy := awsCluster.DeepCopy()
	if _, err := controllerutil.CreateOrPatch(ctx, r.Client, awsCluster, func() error {
		awsCluster.Annotations = awsClusterCopy.Annotations
		awsCluster.Spec = awsClusterCopy.Spec
		return nil
	}); err != nil {
		return fmt.Errorf("unable to create or patch core cluster: %v", err)
	}

	awsCluster.Status.Ready = true
	if err := r.Status().Patch(ctx, awsCluster, client.MergeFrom(awsClusterCopy)); err != nil {
		return fmt.Errorf("unable to update aws cluster status: %v", err)
	}

	return nil
}
