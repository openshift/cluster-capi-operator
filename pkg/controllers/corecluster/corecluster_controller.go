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
package corecluster

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const (
	controllerName                    = "CoreClusterController"
	capiInfraClusterAPIVersionV1Beta1 = "infrastructure.cluster.x-k8s.io/v1beta1"
	capiInfraClusterAPIVersionV1Beta2 = "infrastructure.cluster.x-k8s.io/v1beta2"
	clusterOperatorName               = "cluster-api"
)

var (
	errPlatformStatusShouldNotBeNil                = errors.New("infrastructure platformStatus should not be nil")
	errUnsupportedPlatformType                     = errors.New("unsupported platform type")
	errOpenshiftInfraShouldNotBeNil                = errors.New("infrastructure object should not be nil")
	errOpenshiftInfrastructureNameShouldNotBeEmpty = errors.New("infrastructure object's infrastructureName should not be empty")
)

// CoreClusterReconciler reconciles a Cluster object.
type CoreClusterReconciler struct {
	operatorstatus.ClusterOperatorStatusClient
	Cluster  *clusterv1.Cluster
	Infra    *configv1.Infrastructure
	Platform configv1.PlatformType
}

// SetupWithManager sets the CoreClusterReconciler controller up with the given manager.
func (r *CoreClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(&configv1.ClusterOperator{}, builder.WithPredicates(clusterOperatorPredicates())).
		Watches(
			&clusterv1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(coreClusterPredicate(r.ManagedNamespace)),
		).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

// Reconcile reconciles the core cluster object for the openshift-cluster-api namespace.
func (r *CoreClusterReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName(controllerName)
	logger.Info("Reconciling core cluster")
	defer logger.Info("Finished reconciling core cluster")

	ocpInfrastructureName, err := getOCPInfrastructureName(r.Infra)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to obtain infrastructure name: %w", err)
	}

	cluster, err := r.ensureCoreCluster(ctx, client.ObjectKey{Namespace: r.ManagedNamespace, Name: ocpInfrastructureName}, logger)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure core cluster: %w", err)
	}

	if !cluster.DeletionTimestamp.IsZero() {
		if err := r.SetStatusAvailable(ctx, ""); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set status available: %w", err)
		}

		return ctrl.Result{}, nil
	}

	if err := r.ensureCoreClusterControlPlaneInitializedCondition(ctx, cluster); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure core cluster has the ControlPlaneInitializedCondition: %w", err)
	}

	if err := r.SetStatusAvailable(ctx, ""); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set status available: %w", err)
	}

	return ctrl.Result{}, nil
}

// ensureCoreCluster creates a cluster with the given name and returns the cluster object.
func (r *CoreClusterReconciler) ensureCoreCluster(ctx context.Context, clusterObjectKey client.ObjectKey, logger logr.Logger) (*clusterv1.Cluster, error) {
	cluster := &clusterv1.Cluster{}
	if err := r.Client.Get(ctx, clusterObjectKey, cluster); err != nil && !kerrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get core cluster %s/%s: %w", clusterObjectKey.Namespace, clusterObjectKey.Name, err)
	} else if err == nil {
		return cluster, nil
	}

	if r.Infra.Status.PlatformStatus == nil {
		return nil, errPlatformStatusShouldNotBeNil
	}

	infraClusterKind, infraClusterAPIVersion, err := mapOCPPlatformToInfraClusterKindAndVersion(r.Platform)
	if err != nil {
		return nil, fmt.Errorf("unable to map infrastucture resource platform type to infrastructure cluster kind: %w", err)
	}

	infraCluster := &unstructured.Unstructured{}
	infraCluster.SetKind(infraClusterKind)
	infraCluster.SetAPIVersion(infraClusterAPIVersion)

	if err := r.Client.Get(ctx, clusterObjectKey, infraCluster); err != nil {
		return nil, fmt.Errorf("failed to get infra cluster %s/%s: %w", clusterObjectKey.Namespace, clusterObjectKey.Name, err)
	}

	logger.Info(fmt.Sprintf("Core cluster %s/%s does not exist, creating it", clusterObjectKey.Namespace, clusterObjectKey.Name))

	cluster, err = r.generateCoreClusterObject(ctx, clusterObjectKey, infraClusterAPIVersion, infraClusterKind)
	if err != nil {
		return nil, fmt.Errorf("failed to generate core cluster object: %w", err)
	}

	if err := r.Create(ctx, cluster); err != nil {
		return nil, fmt.Errorf("failed to create core cluster: %w", err)
	}

	logger.Info(fmt.Sprintf("Successfully created core cluster '%s/%s'", clusterObjectKey.Namespace, r.Infra.Status.InfrastructureName))

	return cluster, nil
}

// generateCoreClusterObject generates a new core cluster object to be created.
func (r *CoreClusterReconciler) generateCoreClusterObject(_ context.Context, clusterObjectKey client.ObjectKey, infraClusterAPIVersion, infraClusterKind string) (*clusterv1.Cluster, error) {
	apiURL, err := url.Parse(r.Infra.Status.APIServerInternalURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse apiURL: %w", err)
	}

	port, err := strconv.ParseInt(apiURL.Port(), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse apiURL port: %w", err)
	}

	return &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterObjectKey.Name,
			Namespace: clusterObjectKey.Namespace,
		},
		Spec: clusterv1.ClusterSpec{
			InfrastructureRef: &corev1.ObjectReference{
				APIVersion: infraClusterAPIVersion,
				Kind:       infraClusterKind,
				Name:       clusterObjectKey.Name,
				Namespace:  clusterObjectKey.Namespace,
			},
			ControlPlaneEndpoint: clusterv1.APIEndpoint{
				Host: apiURL.Hostname(),
				Port: int32(port),
			},
		},
	}, nil
}

// ensureCoreClusterControlPlaneInitializedCondition makes sure the ControlPlaneInitializedCondition condition on the cluster.
func (r *CoreClusterReconciler) ensureCoreClusterControlPlaneInitializedCondition(ctx context.Context, cluster *clusterv1.Cluster) error {
	if conditions.Get(cluster, clusterv1.ControlPlaneInitializedCondition) != nil {
		return nil
	}

	clusterCopy := cluster.DeepCopy()

	conditions.MarkTrue(cluster, clusterv1.ControlPlaneInitializedCondition)

	patch := client.MergeFrom(clusterCopy)

	isRequired, err := util.IsPatchRequired(cluster, patch)
	if err != nil {
		return fmt.Errorf("failed to check if patch required: %w", err)
	}

	if isRequired {
		if err := r.Status().Patch(ctx, cluster, patch); err != nil {
			return fmt.Errorf("unable to update core cluster status: %w", err)
		}
	}

	return nil
}

// mapOCPPlatformToInfraClusterKindAndVersion maps an OCP Infrastructure PlatformType to a CAPI InfraCluster Kind and APIVersion.
func mapOCPPlatformToInfraClusterKindAndVersion(platform configv1.PlatformType) (string, string, error) {
	switch platform {
	case configv1.AWSPlatformType:
		return fmt.Sprintf("%sCluster", platform), capiInfraClusterAPIVersionV1Beta2, nil
	case configv1.AzurePlatformType, configv1.GCPPlatformType, configv1.PowerVSPlatformType,
		configv1.VSpherePlatformType, configv1.OpenStackPlatformType, configv1.BareMetalPlatformType:
		return fmt.Sprintf("%sCluster", platform), capiInfraClusterAPIVersionV1Beta1, nil
	default:
		return "", "", fmt.Errorf("%w: %q", errUnsupportedPlatformType, platform)
	}
}

// getOCPInfrastructureName returns the infrastructureName of the OCP infrastructure and errors if it can't find it.
func getOCPInfrastructureName(infra *configv1.Infrastructure) (string, error) {
	if infra == nil {
		return "", errOpenshiftInfraShouldNotBeNil
	}

	if infra.Status.InfrastructureName == "" {
		return "", errOpenshiftInfrastructureNameShouldNotBeEmpty
	}

	return infra.Status.InfrastructureName, nil
}
