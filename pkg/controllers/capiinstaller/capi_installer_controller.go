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
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/utils/ptr"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"

	"github.com/klauspost/compress/zstd"
)

const (
	// Controller conditions for the Cluster Operator resource.
	capiInstallerControllerAvailableCondition = "CapiInstallerControllerAvailable"
	capiInstallerControllerDegradedCondition  = "CapiInstallerControllerDegraded"

	controllerName                    = "CapiInstallerController"
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
	powerVSIBMCloudProvider           = "ibmcloud"
)

var (
	errEmptyProviderConfigMap = errors.New("provider configmap has no components data")
)

// CapiInstallerController reconciles a ClusterOperator object.
// It is resopnsible for installing the Cluster API components in the cluster.
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
	log := ctrl.LoggerFrom(ctx).WithName(controllerName)

	clusterOperator := &configv1.ClusterOperator{}
	if err := r.Get(ctx, client.ObjectKey{Name: controllers.ClusterOperatorName}, clusterOperator); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Waiting for creation of cluster operator")
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to get cluster operator: %w", err)
	}

	res, err := r.reconcile(ctx, log, clusterOperator)
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
func (r *CapiInstallerController) reconcile(ctx context.Context, log logr.Logger, clusterOperator *configv1.ClusterOperator) (ctrl.Result, error) {
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
		if err := r.applyProviderComponents(ctx, providerComponents, clusterOperator); err != nil {
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
func (r *CapiInstallerController) applyProviderComponents(ctx context.Context, components []string, owner client.Object) error {
	providerObjects, err := getProviderObjects(r.Scheme, components)
	if err != nil {
		return fmt.Errorf("error getting provider components: %w", err)
	}

	var errs error

	for i, providerObject := range providerObjects {
		// All objects created by cluster-capi-operator are owned by its cluster operator
		providerObject.SetOwnerReferences([]metav1.OwnerReference{
			{
				APIVersion: owner.GetObjectKind().GroupVersionKind().Version,
				Kind:       owner.GetObjectKind().GroupVersionKind().Kind,
				Name:       owner.GetName(),
				UID:        owner.GetUID(),
				Controller: ptr.To(true),
			},
		})

		err := r.Patch(ctx, providerObject, client.Apply, client.ForceOwnership, client.FieldOwner("cluster-capi-operator.openshift.io/installer"))
		if err != nil {
			gvk := providerObject.GroupVersionKind()
			name := strings.Join([]string{gvk.Group, gvk.Version, gvk.Kind, providerObject.GetName()}, "/")
			errs = errors.Join(errs, fmt.Errorf("error applying CAPI provider component %q at position %d: %w", name, i, err))
		}
	}

	return errs
}

// getProviderComponents parses the provided list of components into a map of filenames and assets.
// Deployments are handled separately so are returned in a separate map.
func getProviderObjects(scheme *runtime.Scheme, components []string) ([]*unstructured.Unstructured, error) {
	objects := make([]*unstructured.Unstructured, len(components))

	for i, m := range components {
		// Parse the YAML manifests into unstructure objects.
		u, err := yamlToUnstructured(scheme, m)
		if err != nil {
			return nil, fmt.Errorf("error parsing provider component at position %d to unstructured: %w", i, err)
		}

		objects[i] = u
	}

	return objects, nil
}

// setAvailableCondition sets the ClusterOperator status condition to Available.
func (r *CapiInstallerController) setAvailableCondition(ctx context.Context, log logr.Logger) error {
	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		return fmt.Errorf("unable to get cluster operator: %w", err)
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		operatorstatus.NewClusterOperatorStatusCondition(capiInstallerControllerAvailableCondition, configv1.ConditionTrue, operatorstatus.ReasonAsExpected,
			"CAPI Installer Controller works as expected"),
		operatorstatus.NewClusterOperatorStatusCondition(capiInstallerControllerDegradedCondition, configv1.ConditionFalse, operatorstatus.ReasonAsExpected,
			"CAPI Installer Controller works as expected"),
	}

	co.Status.Versions = []configv1.OperandVersion{{Name: controllers.OperatorVersionKey, Version: r.ReleaseVersion}}

	log.V(2).Info("CAPI Installer Controller is Available")

	if err := r.SyncStatus(ctx, co, conds); err != nil {
		return fmt.Errorf("failed to sync status: %w", err)
	}

	return nil
}

// setAvailableCondition sets the ClusterOperator status condition to Degraded.
func (r *CapiInstallerController) setDegradedCondition(ctx context.Context, log logr.Logger) error {
	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		return fmt.Errorf("unable to get cluster operator: %w", err)
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		operatorstatus.NewClusterOperatorStatusCondition(capiInstallerControllerAvailableCondition, configv1.ConditionFalse, operatorstatus.ReasonSyncFailed,
			"CAPI Installer Controller failed install"),
		operatorstatus.NewClusterOperatorStatusCondition(capiInstallerControllerDegradedCondition, configv1.ConditionTrue, operatorstatus.ReasonSyncFailed,
			"CAPI Installer Controller failed install"),
	}

	co.Status.Versions = []configv1.OperandVersion{{Name: controllers.OperatorVersionKey, Version: r.ReleaseVersion}}

	log.Info("CAPI Installer Controller is Degraded")

	if err := r.SyncStatus(ctx, co, conds); err != nil {
		return fmt.Errorf("failed to sync status: %w", err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CapiInstallerController) SetupWithManager(mgr ctrl.Manager) error {
	namespacePredicate := []predicate.Predicate{predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == r.ManagedNamespace
	})}

	build := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(&configv1.ClusterOperator{}, builder.WithPredicates(clusterOperatorPredicates())).
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(transportConfigMapPredicate(r.ManagedNamespace, r.Platform)),
		).
		// We reconcile all Deployment changes because we intend to reflect the
		// status of any created Deployment in the ClusterOperator status.
		Owns(&appsv1.Deployment{}, builder.WithPredicates(namespacePredicate...))

	// All of the following ownsTypes share the ownedPlatformLabelPredicate.
	ownsTypes := []struct {
		obj        client.Object
		predicates []predicate.Predicate
	}{
		{&admissionregistrationv1.MutatingWebhookConfiguration{}, nil},
		{&admissionregistrationv1.ValidatingWebhookConfiguration{}, nil},
		{&admissionregistrationv1beta1.ValidatingAdmissionPolicy{}, nil},
		{&admissionregistrationv1beta1.ValidatingAdmissionPolicyBinding{}, nil},
		{&apiextensionsv1.CustomResourceDefinition{}, nil},
		// These are any ConfigMaps we own, as opposed to the transport ConfigMap, which we don't
		{&corev1.ConfigMap{}, namespacePredicate},
		{&corev1.Service{}, namespacePredicate},
		{&corev1.ServiceAccount{}, namespacePredicate},
		{&rbacv1.ClusterRole{}, nil},
		{&rbacv1.ClusterRoleBinding{}, nil},
		{&rbacv1.Role{}, namespacePredicate},
		{&rbacv1.RoleBinding{}, namespacePredicate},
	}

	for _, owned := range ownsTypes {
		// We're only interested in changes which affect an object's spec
		predicates := append(owned.predicates,
			predicate.Or(
				predicate.AnnotationChangedPredicate{},
				predicate.LabelChangedPredicate{},
				predicate.GenerationChangedPredicate{},
			),
		)

		build = build.Owns(owned.obj, builder.WithPredicates(predicates...))
	}

	if err := build.Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
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
		return nil, errEmptyProviderConfigMap
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
		platform = powerVSIBMCloudProvider
	}

	return strings.ToLower(string(platform))
}

// platformToInfraProviderComponentName maps an OpenShift configv1.PlatformType
// to a matching CAPI ownedProviderComponentName (see consts) Label value.
func platformToInfraProviderComponentName(platform configv1.PlatformType) string {
	if platform == configv1.PowerVSPlatformType {
		platform = powerVSIBMCloudProvider
	}

	return strings.ToLower(fmt.Sprintf("infrastructure-%s", platform))
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
