package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

const (
	infraGVK = "infrastructure.cluster.x-k8s.io/v1beta1"
)

// ClusterReconciler reconciles a ClusterOperator object
type ClusterReconciler struct {
	operatorstatus.ClusterOperatorStatusClient
	Scheme             *runtime.Scheme
	SupportedPlatforms map[string]bool
	clusterName        string
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&configv1.Infrastructure{}, builder.WithPredicates(infrastructurePredicates())).
		Complete(r)
}

func (r *ClusterReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	infra := &configv1.Infrastructure{}
	if err := r.Get(ctx, client.ObjectKey{Name: controllers.InfrastructureResourceName}, infra); err != nil {
		klog.Errorf("Unable to retrive Infrastructure object: %v", err)
		if err := r.SetStatusDegraded(ctx, err); err != nil {
			return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %v", err)
		}
		return ctrl.Result{}, err
	}

	if infra.Status.PlatformStatus == nil {
		klog.Infof("No platform status exists in infrastructure object. Skipping cluster reconciliation...")
		if err := r.SetStatusAvailable(ctx); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	r.clusterName = infra.Status.InfrastructureName

	platformType := infra.Status.PlatformStatus.Type

	// If the platform type is not supported, we should skip cluster reconciliation.
	if _, ok := r.SupportedPlatforms[strings.ToLower(string(platformType))]; !ok {
		klog.Infof("Platform type %v is not supported. Skipping cluster reconciliation...", platformType)
		if err := r.SetStatusAvailable(ctx); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	klog.Infof("Reconciling %v infrastucture cluster", platformType)
	// Reconcile infrastructure cluster based on platform type.
	var infraClusterKind string
	var err error
	switch platformType {
	case configv1.AWSPlatformType:
		infraClusterKind = "AWSCluster"
		err = r.reconcileAWSCluster(ctx, infra.Status.PlatformStatus.AWS)
	default:
		// skipping unsupported platform should be handled earlier
		err = fmt.Errorf("unsupported platform type %v", platformType)
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile infra cluster: %v", err)
	}

	// Reconcile generic cluster
	klog.Infof("Reconciling core cluster")
	if err := r.reconcileCluster(ctx, infraClusterKind); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile cluster: %v", err)
	}

	return ctrl.Result{}, r.SetStatusAvailable(ctx)
}

func (r *ClusterReconciler) reconcileCluster(ctx context.Context, clusterKind string) error {
	if clusterKind == "" {
		return errors.New("cluster kind can't be empty")
	}

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.clusterName,
			Namespace: controllers.DefaultManagedNamespace,
		},
		Spec: clusterv1.ClusterSpec{
			InfrastructureRef: &corev1.ObjectReference{
				APIVersion: infraGVK,
				Kind:       clusterKind,
				Namespace:  controllers.DefaultManagedNamespace,
				Name:       r.clusterName,
			},
		},
	}

	clusterCopy := cluster.DeepCopy()
	if _, err := controllerutil.CreateOrPatch(ctx, r.Client, cluster, func() error {
		cluster.Spec = clusterCopy.Spec
		return nil
	}); err != nil {
		return fmt.Errorf("unable to create or patch core cluster: %v", err)
	}

	return nil
}
