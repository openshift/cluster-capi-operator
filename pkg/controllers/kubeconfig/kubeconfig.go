package kubeconfig

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

const (
	tokenSecretName = "cluster-capi-operator-secret" // nolint
)

// ClusterReconciler reconciles a ClusterOperator object
type KubeconfigReconciler struct {
	operatorstatus.ClusterOperatorStatusClient
	Scheme             *runtime.Scheme
	RestCfg            *rest.Config
	SupportedPlatforms map[string]bool
	clusterName        string
}

// SetupWithManager sets up the controller with the Manager.
func (r *KubeconfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(
			&corev1.Secret{},
			builder.WithPredicates(tokenSecretPredicate()),
		).
		Watches(
			&source.Kind{Type: &corev1.Secret{}},
			handler.EnqueueRequestsFromMapFunc(toTokenSecret),
			builder.WithPredicates(kubeconfigSecretPredicate()),
		).
		Complete(r)
}

func (r *KubeconfigReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName("KubeconfigController")

	infra := &configv1.Infrastructure{}
	if err := r.Get(ctx, client.ObjectKey{Name: controllers.InfrastructureResourceName}, infra); err != nil {
		log.Error(err, "Unable to retrive Infrastructure object")
		if err := r.SetStatusDegraded(ctx, err); err != nil {
			return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %v", err)
		}
		return ctrl.Result{}, err
	}

	if infra.Status.PlatformStatus == nil {
		log.Info("No platform status exists in infrastructure object. Skipping kubeconfig reconciliation...")
		if err := r.SetStatusAvailable(ctx); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	r.clusterName = infra.Status.InfrastructureName

	// If the platform type is not supported, we should skip cluster reconciliation.
	if _, ok := r.SupportedPlatforms[strings.ToLower(string(infra.Status.PlatformStatus.Type))]; !ok {
		log.Info("Platform type is not supported. Skipping kubeconfig reconciliation...", "platformType", infra.Status.PlatformStatus.Type)
		if err := r.SetStatusAvailable(ctx); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	log.Info("Reconciling kubeconfig secret")

	res, err := r.reconcileKubeconfig(ctx)
	if err != nil {
		log.Error(err, "Error reconciling kubeconfig")
		if err := r.SetStatusDegraded(ctx, err); err != nil {
			return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %v", err)
		}
		return ctrl.Result{}, err
	}

	return res, r.SetStatusAvailable(ctx)
}

func (r *KubeconfigReconciler) reconcileKubeconfig(ctx context.Context) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	// Get the token secret
	tokenSecret := &corev1.Secret{}
	tokenSecretKey := client.ObjectKey{
		Name:      tokenSecretName,
		Namespace: controllers.DefaultManagedNamespace,
	}
	if err := r.Get(ctx, tokenSecretKey, tokenSecret); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Waiting for token secret to be created")
			return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
		}
		return ctrl.Result{}, fmt.Errorf("unable to retrieve Secret object: %v", err)
	}

	if time.Since(tokenSecret.CreationTimestamp.Time) >= 30*time.Minute {
		log.Info("Token secret is older than 30 minutes. Recreating it...")
		// The token secret is managed by the CVO, it should be recreated shortly after deletion.
		if err := r.Delete(ctx, tokenSecret); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to delete Secret object: %v", err)
		}
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}

	// Generate kubeconfig
	kubeconfig, err := generateKubeconfig(kubeconfigOptions{
		token:            tokenSecret.Data["token"],
		caCert:           tokenSecret.Data["ca.crt"],
		apiServerEnpoint: r.RestCfg.Host,
		clusterName:      r.clusterName,
	})

	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error generating kubeconfig: %v", err)
	}

	// Create a secret with generated kubeconfig
	out, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error writing kubeconfig: %v", err)
	}

	kubeconfigSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-kubeconfig", r.clusterName),
			Namespace: controllers.DefaultManagedNamespace,
			Labels: map[string]string{
				clusterv1.ClusterLabelName: r.clusterName,
			},
		},
		Data: map[string][]byte{
			"value": out,
		},
		Type: clusterv1.ClusterSecretType,
	}

	kubeconfigSecretCopy := kubeconfigSecret.DeepCopy()
	if _, err := controllerutil.CreateOrPatch(ctx, r.Client, kubeconfigSecret, func() error {
		kubeconfigSecret.ObjectMeta = kubeconfigSecretCopy.ObjectMeta
		kubeconfigSecret.Data = kubeconfigSecretCopy.Data
		kubeconfigSecret.Type = kubeconfigSecretCopy.Type
		return nil
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("error reconciling kubeconfig secret: %v", err)
	}

	return ctrl.Result{}, nil
}
