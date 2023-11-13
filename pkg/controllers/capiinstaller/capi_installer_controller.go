package capiinstaller

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/drone/envsubst/v2"
	"github.com/go-logr/logr"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
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
	Scheme              *runtime.Scheme
	Images              map[string]string
	RestCfg             *rest.Config
	Platform            configv1.PlatformType
	ApplyClient         *kubernetes.Clientset
	APIExtensionsClient *apiextensionsclient.Clientset
}

type provider struct {
	Name      string
	Namespace string
	Version   string
	Type      string
}

func (r *CapiInstallerController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName("CapiInstallerController")

	cO := &configv1.ClusterOperator{}
	if err := r.Get(ctx, req.NamespacedName, cO); err != nil {
		return ctrl.Result{}, fmt.Errorf("error getting cluster operator: %w", err)
	}

	res, err := r.reconcile(ctx, log, cO)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error during reconcile: %w", err)
	}

	if err := r.setAvailableCondition(ctx, log); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set conditions for CAPI Installer Controller: %w", err)
	}

	return res, nil
}

func (r *CapiInstallerController) reconcile(ctx context.Context, log logr.Logger, cO *configv1.ClusterOperator) (ctrl.Result, error) {
	// Build desired providers list.
	coreProviderConfigMapName := defaultCoreProviderComponentName
	infrastructureProviderConfigMapName := platformToProviderConfigMapName(r.Platform)
	providerConfigMapNames := []string{coreProviderConfigMapName, infrastructureProviderConfigMapName}

	// Reconcile desired providers list.
	// TODO: change this to a List to get labels, for future proofing with shards support?
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

		components, err := r.extractProviderComponents(pr, cm)
		if err != nil {
			if err := r.setDegradedCondition(ctx, log); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set conditions for CAPI Installer controller: %w", err)
			}
			return ctrl.Result{}, fmt.Errorf("error extracting provider components from ConfigMap %q: %w", getResourceName(defaultCAPINamespace, cmName), err)
		}

		if err := r.applyProviderComponents(ctx, log, components); err != nil {
			return ctrl.Result{}, fmt.Errorf("error applying provider components: %w", err)
		}

		log.Info("finished reconciling CAPI provider", "type", pr.Type, "name", pr.Name, "version", pr.Version)
	}

	return ctrl.Result{}, nil
}

func (r *CapiInstallerController) applyProviderComponents(ctx context.Context, log logr.Logger, components []string) error {
	componentsFilenames := []string{}
	componentsAssets := make(map[string]string, 0)

	deploymentsFilenames := []string{}
	deploymentsAssets := make(map[string]string, 0)

	for i, m := range components {
		u, err := yamlToUnstructured(r.Scheme, m)
		if err != nil {
			return fmt.Errorf("error parsing provider component at position %d to unstructured: %w", i, err)
		}

		name := u.GroupVersionKind().Group + "/" + u.GroupVersionKind().Version + "/" + u.GroupVersionKind().Kind +
			" - " + getResourceName(u.GetNamespace(), u.GetName())

		if u.GroupVersionKind().Kind == "Deployment" {
			deploymentsFilenames = append(deploymentsFilenames, name)
			deploymentsAssets[name] = m
		} else {
			componentsFilenames = append(componentsFilenames, name)
			componentsAssets[name] = m
		}
	}

	res := resourceapply.ApplyDirectly(
		ctx,
		resourceapply.NewKubeClientHolder(r.ApplyClient).WithAPIExtensionsClient(r.APIExtensionsClient),
		events.NewInMemoryRecorder("cluster-capi-operator-capi-installer-apply-client"),
		resourceapply.NewResourceCache(),
		assetFn(componentsAssets),
		componentsFilenames...,
	)

	for _, d := range deploymentsFilenames {
		deploymentManifest, ok := deploymentsAssets[d]
		if !ok {
			panic("error finding deployment manifest")
		}
		obj, err := yamlToRuntimeObject(r.Scheme, deploymentManifest)
		if err != nil {
			return fmt.Errorf("error parsing provider deployment manifets %q: %w", d, err)
		}

		deployment := obj.(*v1.Deployment)

		if _, _, err := resourceapply.ApplyDeployment(
			ctx,
			r.ApplyClient.AppsV1(),
			events.NewInMemoryRecorder("cluster-capi-operator-capi-installer-apply-client"),
			deployment,
			resourcemerge.ExpectedDeploymentGeneration(deployment, nil),
		); err != nil {
			return fmt.Errorf("error applying provider deployment %q: %w", deployment.Name, err)
		}
	}

	log.Info("CAPI provider components apply result")

	var errs error
	for i, r := range res {
		fmt.Printf("name: %s, changed: %v, error: %v\n", r.File, r.Changed, r.Error)

		if r.Error != nil {
			errs = errors.Join(errs, fmt.Errorf("error applying provider component %q at position %d: %w", r.File, i, r.Error))
		}
	}

	return errs
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

func (r *CapiInstallerController) extractProviderComponents(pr provider, cm *corev1.ConfigMap) ([]string, error) {
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

	// objs, err := parseK8sYaml(r.Scheme, replacedYamlManifests)
	// if err != nil {
	// 	return nil, fmt.Errorf("error parsing CAPI provider manifests: %w", err)
	// }

	return replacedYamlManifests, nil
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

func getResourceName(namespace, name string) string {
	resourceName := fmt.Sprintf("%s/%s", namespace, name)
	if namespace == "" {
		resourceName = name
	}

	return resourceName
}

func assetFn(assetsMap map[string]string) resourceapply.AssetFunc {
	return func(name string) ([]byte, error) {
		o, ok := assetsMap[name]
		if !ok {
			return nil, fmt.Errorf("error resource not found with name %s", name)
		}

		return []byte(o), nil
	}
}

func yamlToRuntimeObject(sch *runtime.Scheme, m string) (runtime.Object, error) {
	decode := serializer.NewCodecFactory(sch).UniversalDeserializer().Decode
	obj, _, err := decode([]byte(m), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("error while decoding YAML object: %w", err)
	}

	return obj, nil
}

func yamlToUnstructured(sch *runtime.Scheme, m string) (*unstructured.Unstructured, error) {
	obj, err := yamlToRuntimeObject(sch, m)
	if err != nil {
		return nil, fmt.Errorf("error while decoding YAML to runtime object: %w", err)
	}

	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("error converting runtime.Object to unstructured: %w", err)
	}

	u := &unstructured.Unstructured{}
	u.Object = unstructuredObj

	return u, nil
}
