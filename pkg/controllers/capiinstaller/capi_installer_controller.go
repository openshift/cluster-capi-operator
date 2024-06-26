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

	"github.com/klauspost/compress/zstd"
)

const (
	// Controller conditions for the Cluster Operator resource
	capiInstallerControllerAvailableCondition = "CapiInstallerControllerAvailable"
	capiInstallerControllerDegradedCondition  = "CapiInstallerControllerDegraded"

	defaultCAPINamespace              = "openshift-cluster-api"
	providerConfigMapLabelVersionKey  = "provider.cluster.x-k8s.io/version"
	providerConfigMapLabelTypeKey     = "provider.cluster.x-k8s.io/type"
	providerConfigMapLabelNameKey     = "provider.cluster.x-k8s.io/name"
	ownedProviderComponentName        = "cluster.x-k8s.io/provider"
	imagePlaceholder                  = "to.be/replaced:v99"
	openshiftInfrastructureObjectName = "cluster"
	notNamespaced                     = ""
	clusterOperatorName               = "cluster-api"
	defaultCoreProviderComponentName  = "cluster-api"
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

// Reconcile reconciles the cluster-api ClusterOperator object.
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

// reconcile performs the main business logic for installing Cluster API components in the cluster.
// Notably it fetches CAPI providers "transport" ConfigMap(s) matching the required labels,
// it extracts from those ConfigMaps the embedded CAPI providers manifests for the components
// and it applies them to the cluster.
//
//nolint:unparam
func (r *CapiInstallerController) reconcile(ctx context.Context, log logr.Logger) (ctrl.Result, error) {
	// Define the desired providers to be installed for this cluster.
	// We always want to install the core provider, which in our case is the default cluster-api core provider.
	// We also want to install the infrastructure provider that matches the currently detected platform the cluster is running on.
	providerConfigMapLabels := map[string]string{
		"core":           defaultCoreProviderComponentName,
		"infrastructure": platformToProviderConfigMapLabelNameValue(r.Platform),
	}

	// Process each one of the desired providers.
	for providerConfigMapLabelTypeVal, providerConfigMapLabelNameVal := range providerConfigMapLabels {
		log.Info("reconciling CAPI provider", "name", providerConfigMapLabelNameVal)

		// Get a List all the ConfigMaps matching the desired provider labels.
		configMapList := &corev1.ConfigMapList{}
		if err := r.List(ctx, configMapList, client.InNamespace(defaultCAPINamespace),
			client.MatchingLabels{
				providerConfigMapLabelNameKey: providerConfigMapLabelNameVal,
				providerConfigMapLabelTypeKey: providerConfigMapLabelTypeVal,
			},
		); err != nil {
			if err := r.setDegradedCondition(ctx, log); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set conditions for CAPI Installer controller: %w", err)
			}
			return ctrl.Result{}, fmt.Errorf("unable to list CAPI provider %q ConfigMaps: %w", providerConfigMapLabelNameVal, err)
		}

		// Extract the provider manifests stored each of the matching ConfigMaps.
		var providerComponents []string
		for _, cm := range configMapList.Items {
			log.Info("processing CAPI provider ConfigMap", "configmapName", cm.Name, "providerType", cm.Labels[providerConfigMapLabelTypeKey],
				"providerName", cm.Labels[providerConfigMapLabelNameKey], "providerVersion", cm.Labels[providerConfigMapLabelVersionKey])

			partialComponents, err := r.extractProviderComponents(cm)
			if err != nil {
				if err := r.setDegradedCondition(ctx, log); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to set conditions for CAPI Installer controller: %w", err)
				}
				return ctrl.Result{}, fmt.Errorf("error extracting CAPI provider components from ConfigMap %q/%q: %w", cm.Namespace, cm.Name, err)
			}

			providerComponents = append(providerComponents, partialComponents...)
		}

		// Apply all the collected provider components manifests.
		if err := r.applyProviderComponents(ctx, providerComponents); err != nil {
			if err := r.setDegradedCondition(ctx, log); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set conditions for CAPI Installer controller: %w", err)
			}
			return ctrl.Result{}, fmt.Errorf("error applying CAPI provider %q components: %w", providerConfigMapLabelNameVal, err)
		}

		log.Info("finished reconciling CAPI provider", "name", providerConfigMapLabelNameVal)
	}

	return ctrl.Result{}, nil
}

// applyProviderComponents applies the provider components to the cluster.
// It does so by differentiating between static components and dynamic components (i.e. Deployments).
func (r *CapiInstallerController) applyProviderComponents(ctx context.Context, components []string) error {
	componentsFilenames := []string{}
	componentsAssets := make(map[string]string, 0)

	deploymentsFilenames := []string{}
	deploymentsAssets := make(map[string]string, 0)

	for i, m := range components {
		// Parse the YAML manifests into unstructure objects.
		u, err := yamlToUnstructured(r.Scheme, m)
		if err != nil {
			return fmt.Errorf("error parsing provider component at position %d to unstructured: %w", i, err)
		}

		name := fmt.Sprintf("%s/%s/%s - %s",
			u.GroupVersionKind().Group,
			u.GroupVersionKind().Version,
			u.GroupVersionKind().Kind,
			getResourceName(u.GetNamespace(), u.GetName()),
		)

		// Divide manifests into static vs deployment components.
		if u.GroupVersionKind().Kind == "Deployment" {
			deploymentsFilenames = append(deploymentsFilenames, name)
			deploymentsAssets[name] = m
		} else {
			componentsFilenames = append(componentsFilenames, name)
			componentsAssets[name] = m
		}
	}

	// Perform a Direct apply of the static components.
	res := resourceapply.ApplyDirectly(
		ctx,
		resourceapply.NewKubeClientHolder(r.ApplyClient).WithAPIExtensionsClient(r.APIExtensionsClient),
		events.NewInMemoryRecorder("cluster-capi-operator-capi-installer-apply-client"),
		resourceapply.NewResourceCache(),
		assetFn(componentsAssets),
		componentsFilenames...,
	)

	// For each of the Deployment components perform a Deployment-specific apply.
	for _, d := range deploymentsFilenames {
		deploymentManifest, ok := deploymentsAssets[d]
		if !ok {
			panic("error finding deployment manifest")
		}
		obj, err := yamlToRuntimeObject(r.Scheme, deploymentManifest)
		if err != nil {
			return fmt.Errorf("error parsing CAPI provider deployment manifets %q: %w", d, err)
		}

		// TODO: Deployments State/Conditions should influence the overall ClusterOperator Status.
		deployment := obj.(*v1.Deployment)
		if _, _, err := resourceapply.ApplyDeployment(
			ctx,
			r.ApplyClient.AppsV1(),
			events.NewInMemoryRecorder("cluster-capi-operator-capi-installer-apply-client"),
			deployment,
			resourcemerge.ExpectedDeploymentGeneration(deployment, nil),
		); err != nil {
			return fmt.Errorf("error applying CAPI provider deployment %q: %w", deployment.Name, err)
		}
	}

	var errs error
	for i, r := range res {
		if r.Error != nil {
			errs = errors.Join(errs, fmt.Errorf("error applying CAPI provider component %q at position %d: %w", r.File, i, r.Error))
		}
	}

	return errs
}

// setAvailableCondition sets the ClusterOperator status condition to Available.
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

// setAvailableCondition sets the ClusterOperator status condition to Degraded.
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

// extractProviderComponents extracts CAPI components manifests from a transport ConfigMap.
// The format of the ConfigMap is well known and follows the upstream CAPI's
// clusterctl Provider Contract - Components YAML file contract defined at:
// https://github.com/kubernetes-sigs/cluster-api/blob/a36712e28bf5d54e398ea84cb3e20102c0499426/docs/book/src/clusterctl/provider-contract.md?plain=1#L157-L162
func (r *CapiInstallerController) extractProviderComponents(cm corev1.ConfigMap) ([]string, error) {
	yamlManifests, err := extractManifests(cm)
	if err != nil {
		return nil, fmt.Errorf("failed to extract manifests from configMap: %w", err)
	}

	replacedYamlManifests := []string{}
	providerName := cm.Labels[providerConfigMapLabelNameKey]

	for _, m := range yamlManifests {
		newM := strings.Replace(m, imagePlaceholder, r.Images[providerNameToImageKey(providerName)], 1)
		newM = strings.Replace(newM, "registry.ci.openshift.org/openshift:kube-rbac-proxy", r.Images["kube-rbac-proxy"], 1)
		// TODO: change this to manager in the forked providers openshift/Dockerfile.rhel.
		newM = strings.Replace(newM, "/manager", providerNameToCommand(providerName), 1)

		replacedYamlManifests = append(replacedYamlManifests, newM)
	}

	return replacedYamlManifests, nil
}

// extractManifests extracts and processes component manifests from given ConfiMap.
// If the data is in compressed binary form, it decompresses them.
func extractManifests(cm corev1.ConfigMap) ([]string, error) {
	data, hasData := cm.Data["components"]
	binaryData, hasBinary := cm.BinaryData["components-zstd"]

	if !(hasBinary || hasData) {
		return nil, errors.New("provider configmap has no components data")
	}

	if hasBinary {
		decoder, err := zstd.NewReader(nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd reader: %w", err)
		}

		decoded, err := decoder.DecodeAll(binaryData, []byte{})
		if err != nil {
			return nil, fmt.Errorf("failed to decompress components: %w", err)
		}
		data = string(decoded)
	}

	// Certain provider components have drone/envsubst environment variables interpolated within the manifest.
	// Substitute them with the value defined in the environment variable (see setFeatureGatesEnvVars()).
	// If that's not set, fallback to the default value defined in the template.
	components, err := envsubst.EvalEnv(data)
	if err != nil {
		return nil, fmt.Errorf("failed to substitute environment variables in component manifests: %w", err)
	}

	// Split multi-document YAML into single manifests.
	yamlManifests := regexp.MustCompile("(?m)^---$").Split(components, -1)

	return yamlManifests, nil
}

// platformToProviderConfigMapLabelNameValue maps an OpenShift configv1.PlatformType
// to a matching CAPI provider ConfigMap `name` Label value.
func platformToProviderConfigMapLabelNameValue(platform configv1.PlatformType) string {
	if platform == configv1.PowerVSPlatformType {
		platform = "ibmcloud"
	}
	return strings.ToLower(fmt.Sprintf("%s", platform))
}

// platformToInfraProviderComponentName maps an OpenShift configv1.PlatformType
// to a matching CAPI ownedProviderComponentName (see consts) Label value.
func platformToInfraProviderComponentName(platform configv1.PlatformType) string {
	if platform == configv1.PowerVSPlatformType {
		platform = "ibmcloud"
	}
	return strings.ToLower(fmt.Sprintf("infrastructure-%s", platform))
}

// getResourceName returns a "namespace/name" string or a "name" string if namespace is empty.
func getResourceName(namespace, name string) string {
	resourceName := fmt.Sprintf("%s/%s", namespace, name)
	if namespace == "" {
		resourceName = name
	}

	return resourceName
}

// assetsFn is a resourceapply.AssetFunc.
func assetFn(assetsMap map[string]string) resourceapply.AssetFunc {
	return func(name string) ([]byte, error) {
		o, ok := assetsMap[name]
		if !ok {
			return nil, fmt.Errorf("error resource not found with name %s", name)
		}

		return []byte(o), nil
	}
}

// yamlToRuntimeObject parses a YAML manifest into a runtime.Object.
func yamlToRuntimeObject(sch *runtime.Scheme, m string) (runtime.Object, error) {
	decode := serializer.NewCodecFactory(sch).UniversalDeserializer().Decode
	obj, _, err := decode([]byte(m), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("error while decoding YAML object: %w", err)
	}

	return obj, nil
}

// yamlToRuntimeObject parses a YAML manifest into an *unstructured.Unstructured object.
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
