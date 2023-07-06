package capiinstaller

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/drone/envsubst/v2"
	"github.com/go-logr/logr"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"

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
	openshiftInfrastructureObjectName         = "cluster"
	notNamespaced                             = ""
	clusterOperatorName                       = "cluster-capi-operator"
	defaultCoreProviderComponentName          = "cluster-api"
)

type CapiInstallerController struct {
	operatorstatus.ClusterOperatorStatusClient
	Scheme   *runtime.Scheme
	Images   map[string]string
	RestCfg  *rest.Config
	Platform configv1.PlatformType
}

type provider struct {
	Name      string
	Namespace string
	Version   string
	Type      string
}

func (r *CapiInstallerController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName("CapiInstallerController")

	res, err := r.reconcile(ctx, log)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error during reconcile: %w", err)
	}

	if err := r.setAvailableCondition(ctx, log); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set conditions for CAPI Installer Controller: %w", err)
	}

	return res, nil
}

//nolint:unparam
func (r *CapiInstallerController) reconcile(ctx context.Context, log logr.Logger) (ctrl.Result, error) {
	// Build desired providers list.
	coreProviderConfigMapName := defaultCoreProviderComponentName
	infrastructureProviderConfigMapName := platformToProviderConfigMapName(r.Platform)
	providerConfigMapNames := []string{coreProviderConfigMapName, infrastructureProviderConfigMapName}

	// Reconcile desired providers list.
	for _, cmName := range providerConfigMapNames {
		cm := &corev1.ConfigMap{}
		if err := r.Get(ctx, client.ObjectKey{Name: cmName, Namespace: defaultCAPINamespace}, cm); err != nil {
			if err := r.setDegradedCondition(ctx, log); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set conditions for CAPI Installer controller: %w", err)
			}
			return ctrl.Result{}, fmt.Errorf("unable to get CAPI Provider ConfigMap %q: %w", getResourceName(defaultCAPINamespace, cm.Name), err)
		}

		pr, err := r.newProviderFromConfigMap(cm)
		if err != nil {
			if err := r.setDegradedCondition(ctx, log); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set conditions for CAPI Installer controller: %w", err)
			}
			return ctrl.Result{}, fmt.Errorf("error creating provider from ConfigMap %q: %w", getResourceName(defaultCAPINamespace, cmName), err)
		}

		log.Info("reconciling CAPI provider", "type", pr.Type, "name", pr.Name, "version", pr.Version)

		objs, err := r.extractProviderComponents(pr, cm)
		if err != nil {
			if err := r.setDegradedCondition(ctx, log); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set conditions for CAPI Installer controller: %w", err)
			}
			return ctrl.Result{}, fmt.Errorf("error extracting provider components from ConfigMap %q: %w", getResourceName(defaultCAPINamespace, cmName), err)
		}

		for _, o := range objs {
			cO := o.(client.Object)

			log.V(2).Info("reconciling resource", "gvk", cO.GetObjectKind().GroupVersionKind(), "resourceName", getResourceName(cO.GetNamespace(), cO.GetName()))

			if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, cO, func() error {
				// TODO: implement update / diffing strategy.
				return nil
			}); err != nil {
				return ctrl.Result{}, fmt.Errorf("error creating/updating CAPI provider resource %q: %w", getResourceName(cO.GetNamespace(), cO.GetName()), err)
			}
		}

		log.Info("finished reconciling CAPI provider", "type", pr.Type, "name", pr.Name, "version", pr.Version)
	}

	return ctrl.Result{}, nil
}

func (r *CapiInstallerController) setAvailableCondition(ctx context.Context, log logr.Logger) error {
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
	log.V(2).Info("CAPI Installer Controller is Available")
	return r.SyncStatus(ctx, co, conds)
}

func (r *CapiInstallerController) setDegradedCondition(ctx context.Context, log logr.Logger) error {
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
		For(&configv1.ClusterOperator{}, builder.WithPredicates(clusterOperatorPredicates())).
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(configMapPredicate(r.ManagedNamespace, r.Platform)),
		).
		Watches(
			&appsv1.Deployment{},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(ownedPlatformLabelPredicate(r.ManagedNamespace, r.Platform)),
		).
		Watches(
			&admissionregistrationv1.ValidatingWebhookConfiguration{},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(ownedPlatformLabelPredicate(notNamespaced, r.Platform)),
		).
		Watches(
			&admissionregistrationv1.MutatingWebhookConfiguration{},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(ownedPlatformLabelPredicate(notNamespaced, r.Platform)),
		).
		Watches(
			&corev1.Service{},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(ownedPlatformLabelPredicate(r.ManagedNamespace, r.Platform)),
		).
		Watches(
			&apiextensionsv1.CustomResourceDefinition{},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(ownedPlatformLabelPredicate(notNamespaced, r.Platform)),
		).
		Watches(
			&corev1.ServiceAccount{},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(ownedPlatformLabelPredicate(r.ManagedNamespace, r.Platform)),
		).
		Watches(
			&rbacv1.ClusterRoleBinding{},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(ownedPlatformLabelPredicate(notNamespaced, r.Platform)),
		).
		Watches(
			&rbacv1.ClusterRole{},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(ownedPlatformLabelPredicate(notNamespaced, r.Platform)),
		).
		Watches(
			&rbacv1.Role{},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(ownedPlatformLabelPredicate(r.ManagedNamespace, r.Platform)),
		).
		Watches(
			&rbacv1.RoleBinding{},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(ownedPlatformLabelPredicate(r.ManagedNamespace, r.Platform)),
		)

	return build.Complete(r)
}

func (r *CapiInstallerController) extractProviderComponents(pr provider, cm *corev1.ConfigMap) ([]runtime.Object, error) {
	components, err := envsubst.EvalEnv(cm.Data["components"])
	if err != nil {
		return nil, fmt.Errorf("failed to substitute env vars in CAPI manifests: %w", err)
	}

	// Split multi-document YAML into single manifests.
	yamlManifests := regexp.MustCompile("(?m)^---$").Split(components, -1)
	replacedYamlManifests := []string{}

	for _, m := range yamlManifests {
		newM := strings.Replace(m, imagePlaceholder, r.Images[providerNameToImageKey(pr.Name)], 1)
		// TODO: change this to manager in the forked providers openshift/Dockerfile.rhel.
		newM = strings.Replace(newM, "/manager", providerNameToCommand(pr.Name), 1)

		replacedYamlManifests = append(replacedYamlManifests, newM)
	}

	objs, err := parseK8sYaml(r.Scheme, replacedYamlManifests)
	if err != nil {
		return nil, fmt.Errorf("error parsing CAPI provider manifests: %w", err)
	}

	return objs, nil
}

func (r *CapiInstallerController) newProviderFromConfigMap(cm *corev1.ConfigMap) (provider, error) {
	providerVersion, ok := cm.GetLabels()[configMapVersionLabelName]
	if !ok {
		return provider{}, fmt.Errorf("unable to read provider version from ConfigMap %q", cm.Name)
	}

	providerType, ok := cm.GetLabels()[configMapVersionTypeName]
	if !ok {
		return provider{}, fmt.Errorf("unable to read provider type from ConfigMap %q", cm.Name)
	}

	providerName, ok := cm.GetLabels()[configMapVersionNameName]
	if !ok {
		return provider{}, fmt.Errorf("unable to read provider name from ConfigMap %q", cm.Name)
	}

	return provider{
		Name:      providerName,
		Namespace: cm.Namespace,
		Type:      providerType,
		Version:   providerVersion,
	}, nil
}

func platformToProviderConfigMapName(platform configv1.PlatformType) string {
	return strings.ToLower(fmt.Sprintf("%s", platform))
}

func platformToInfraProviderComponentName(platform configv1.PlatformType) string {
	return strings.ToLower(fmt.Sprintf("infrastructure-%s", platform))
}

func parseK8sYaml(sch *runtime.Scheme, manifests []string) ([]runtime.Object, error) {
	retVal := make([]runtime.Object, 0, len(manifests))
	for _, f := range manifests {
		if f == "\n" || f == "" {
			// Ignore empty cases.
			continue
		}

		decode := serializer.NewCodecFactory(sch).UniversalDeserializer().Decode
		obj, _, err := decode([]byte(f), nil, nil)
		if err != nil {
			return nil, fmt.Errorf("error while decoding YAML object: %w", err)
		}

		retVal = append(retVal, obj)
	}

	return retVal, nil
}

func getResourceName(namespace, name string) string {
	resourceName := fmt.Sprintf("%s/%s", namespace, name)
	if namespace == "" {
		resourceName = name
	}

	return resourceName
}
