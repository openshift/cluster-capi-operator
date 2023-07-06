package capiinstaller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

const (
	// Controller conditions for the Cluster Operator resource
	capiInstallerControllerAvailableCondition = "CapiInstallerControllerAvailable"
	capiInstallerControllerDegradedCondition  = "CapiInstallerControllerDegraded"
	defaultCAPINamespace                      = "openshift-cluster-api"
	configMapVersionLabelName                 = "provider.cluster.x-k8s.io/version"
)

type CapiInstallerController struct {
	operatorstatus.ClusterOperatorStatusClient
	Scheme  *runtime.Scheme
	Images  map[string]string
	RestCfg *rest.Config
}

func (r *CapiInstallerController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName("CapiInstallerController")

	log.Info("reconciling CAPI Provider ConfigMap", req.Name)

	defaultObjectKey := client.ObjectKey{
		Name: req.Name, Namespace: req.Namespace,
	}

	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, defaultObjectKey, cm); err != nil {
		log.Error(err, "unable to get CAPI Provider ConfigMap", req.Name)
		if err := r.setDegradedCondition(ctx); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set conditions for capi installer controller: %v", err)
		}
		return ctrl.Result{}, err
	}

	// log.Info(fmt.Sprintf("obtained ConfigMap: %#v", cm))

	return r.reconcile(ctx, log, cm)
}

func (r *CapiInstallerController) reconcile(ctx context.Context, log logr.Logger, cm *corev1.ConfigMap) (ctrl.Result, error) {
	log.Info("starting reconciliation",
		"Generation", cm.GetGeneration())

	version, ok := cm.GetLabels()[configMapVersionLabelName]
	if !ok {
		return ctrl.Result{}, fmt.Errorf("unable to read provider version from ConfigMap %q", cm.Name)
	}

	pr := provider{
		Name:      cm.Name,
		Namespace: cm.Namespace,
		Version:   version,
	}

	reconciler := newPhaseReconciler(*r, pr)
	phases := []reconcilePhaseFn{
		reconciler.preflightChecks,
		reconciler.load,
		reconciler.fetch,
		reconciler.preInstall,
		reconciler.install,
	}

	res := reconcile.Result{}
	var err error
	for _, phase := range phases {
		res, err = phase(ctx)
		if err != nil {
			se, ok := err.(*PhaseError)
			if ok {
				// TODO: fix
				// conditions.Set(provider, conditions.FalseCondition(se.Type, se.Reason, se.Severity, err.Error()))
				log.Error(err, se.Reason)
			}
		}
		if !res.IsZero() || err != nil {
			// the steps are sequencial, so we must be complete before progressing.
			return res, err
		}
	}
	return res, nil
}

func (r *CapiInstallerController) setAvailableCondition(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)

	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		return err
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		operatorstatus.NewClusterOperatorStatusCondition(capiInstallerControllerAvailableCondition, configv1.ConditionTrue, operatorstatus.ReasonAsExpected,
			"User Data Secret Controller works as expected"),
		operatorstatus.NewClusterOperatorStatusCondition(capiInstallerControllerDegradedCondition, configv1.ConditionFalse, operatorstatus.ReasonAsExpected,
			"User Data Secret Controller works as expected"),
	}

	co.Status.Versions = []configv1.OperandVersion{{Name: controllers.OperatorVersionKey, Version: r.ReleaseVersion}}
	log.Info("CAPI Installer Controller is Available")
	return r.SyncStatus(ctx, co, conds)
}

func (r *CapiInstallerController) setDegradedCondition(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)

	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		return err
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		operatorstatus.NewClusterOperatorStatusCondition(capiInstallerControllerAvailableCondition, configv1.ConditionFalse, operatorstatus.ReasonSyncFailed,
			"User Data Secret Controller failed to sync secret"),
		operatorstatus.NewClusterOperatorStatusCondition(capiInstallerControllerDegradedCondition, configv1.ConditionTrue, operatorstatus.ReasonSyncFailed,
			"User Data Secret Controller failed to sync secret"),
	}

	co.Status.Versions = []configv1.OperandVersion{{Name: controllers.OperatorVersionKey, Version: r.ReleaseVersion}}
	log.Info("CAPI Installer Controller is Degraded")
	return r.SyncStatus(ctx, co, conds)
}

// SetupWithManager sets up the controller with the Manager.
func (r *CapiInstallerController) SetupWithManager(mgr ctrl.Manager) error {
	build := ctrl.NewControllerManagedBy(mgr).
		For(
			&corev1.ConfigMap{},
			builder.WithPredicates(configMapPredicate(r.ManagedNamespace)),
		)
		// Watches(
		// 	&source.Kind{Type: &corev1.ConfigMap{}},
		// 	handler.EnqueueRequestsFromMapFunc(toConfigMap),
		// 	builder.WithPredicates(configMapPredicate(r.ManagedNamespace)),
		// )

	return build.Complete(r)
}
