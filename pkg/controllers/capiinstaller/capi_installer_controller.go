package capiinstaller

import (
	"context"
	"fmt"
	"strings"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/drone/envsubst/v2"
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
	configMapVersionTypeName                  = "provider.cluster.x-k8s.io/type"
	configMapVersionNameName                  = "provider.cluster.x-k8s.io/name"
	ownedProviderComponentName                = "cluster.x-k8s.io/provider"
	imagePlaceholder                          = "to.be/replaced:v99"
)

type CapiInstallerController struct {
	operatorstatus.ClusterOperatorStatusClient
	Scheme  *runtime.Scheme
	Images  map[string]string
	RestCfg *rest.Config
}

type provider struct {
	Name      string
	Namespace string
	Version   string
	Type      string
}

func (r *CapiInstallerController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName("CapiInstallerController")

	log.Info("reconciling CAPI Provider ConfigMap", "name", req.Namespace+"/"+req.Name)

	defaultObjectKey := client.ObjectKey{
		Name: req.Name, Namespace: req.Namespace,
	}

	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, defaultObjectKey, cm); err != nil {
		log.Error(err, "unable to get CAPI Provider ConfigMap", "name", req.Namespace+"/"+req.Name)
		if err := r.setDegradedCondition(ctx); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set conditions for CAPI Installer Controller: %w", err)
		}
		return ctrl.Result{}, err
	}

	res, err := r.reconcile(ctx, log, cm)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error during reconcile: %w", err)
	}

	if err := r.setAvailableCondition(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set conditions for CAPI Installer Controller: %w", err)
	}

	return res, nil
}

func (r *CapiInstallerController) reconcile(ctx context.Context, log logr.Logger, cm *corev1.ConfigMap) (ctrl.Result, error) {
	log.Info("starting reconciliation",
		"Generation", cm.GetGeneration())

	providerVersion, ok := cm.GetLabels()[configMapVersionLabelName]
	if !ok {
		return ctrl.Result{}, fmt.Errorf("unable to read provider version from ConfigMap %q", cm.Name)
	}

	providerType, ok := cm.GetLabels()[configMapVersionTypeName]
	if !ok {
		return ctrl.Result{}, fmt.Errorf("unable to read provider type from ConfigMap %q", cm.Name)
	}

	providerName, ok := cm.GetLabels()[configMapVersionNameName]
	if !ok {
		return ctrl.Result{}, fmt.Errorf("unable to read provider name from ConfigMap %q", cm.Name)
	}

	pr := provider{
		Name:      providerName,
		Namespace: cm.Namespace,
		Type:      providerType,
		Version:   providerVersion,
	}

	log.Info("reconciling CAPI provider", "type", pr.Type, "name", pr.Name, "version", pr.Version)

	componentsTmpl := cm.Data["components"]

	components, err := envsubst.EvalEnv(componentsTmpl)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to substitute env vars in CAPI manifests: %w", err)
	}

	yamlManifests := strings.Split(components, "---")
	replacedYamlManifests := []string{}

	for _, m := range yamlManifests {
		newM := strings.Replace(m, imagePlaceholder, r.Images[providerNameToImageKey(pr.Name)], 1)
		// TODO: change this to manager in the forked providers openshift/Dockerfile.rhel.
		newM = strings.Replace(newM, "/manager", providerNameToCommand(pr.Name), 1)

		replacedYamlManifests = append(replacedYamlManifests, newM)
	}

	objs, err := parseK8sYaml(replacedYamlManifests)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error parsing CAPI manifests: %w", err)
	}

	for _, o := range objs {
		cO := o.(client.Object)
		log.Info("creating object", "gvk", cO.GetObjectKind().GroupVersionKind(), "namespace/name", cO.GetNamespace()+"/"+cO.GetName())

		if err := r.Create(ctx, cO); err != nil {
			if !errors.IsAlreadyExists(err) {
				return ctrl.Result{}, fmt.Errorf("error creating CAPI manifest: %w", err)
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *CapiInstallerController) setAvailableCondition(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)

	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		return err
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		operatorstatus.NewClusterOperatorStatusCondition(capiInstallerControllerAvailableCondition, configv1.ConditionTrue, operatorstatus.ReasonAsExpected,
			"CAPI Installer Controller works as expected"),
		operatorstatus.NewClusterOperatorStatusCondition(capiInstallerControllerDegradedCondition, configv1.ConditionFalse, operatorstatus.ReasonAsExpected,
			"CAPI Installer Controller works as expected"),
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
			"CAPI Installer Controller failed install"),
		operatorstatus.NewClusterOperatorStatusCondition(capiInstallerControllerDegradedCondition, configv1.ConditionTrue, operatorstatus.ReasonSyncFailed,
			"CAPI Installer Controller failed install"),
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
		).
		Watches(
			&source.Kind{Type: &appsv1.Deployment{}},
			handler.EnqueueRequestsFromMapFunc(toProviderConfigMap),
			builder.WithPredicates(ownedLabelPredicate(r.ManagedNamespace)),
		).
		Watches(
			&source.Kind{Type: &admissionregistrationv1.ValidatingWebhookConfiguration{}},
			handler.EnqueueRequestsFromMapFunc(toProviderConfigMap),
			builder.WithPredicates(ownedLabelPredicate(r.ManagedNamespace)),
		).
		Watches(
			&source.Kind{Type: &admissionregistrationv1.MutatingWebhookConfiguration{}},
			handler.EnqueueRequestsFromMapFunc(toProviderConfigMap),
			builder.WithPredicates(ownedLabelPredicate(r.ManagedNamespace)),
		).
		Watches(
			&source.Kind{Type: &corev1.Service{}},
			handler.EnqueueRequestsFromMapFunc(toProviderConfigMap),
			builder.WithPredicates(ownedLabelPredicate(r.ManagedNamespace)),
		)

	return build.Complete(r)
}

func parseK8sYaml(manifests []string) ([]runtime.Object, error) {
	retVal := make([]runtime.Object, 0, len(manifests))
	for _, f := range manifests {
		if f == "\n" || f == "" {
			// Ignore empty cases.
			continue
		}

		decode := scheme.Codecs.UniversalDeserializer().Decode
		obj, _, err := decode([]byte(f), nil, nil)
		if err != nil {
			return nil, fmt.Errorf("error while decoding YAML object: %w", err)
		}

		retVal = append(retVal, obj)
	}

	return retVal, nil
}
