/*
Copyright 2026 Red Hat, Inc.

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

package proxy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	"pkg.package-operator.run/boxcutter/managedcache"
)

const (
	controllerName = "ProxyController"

	// ProxyInjectAnnotation is the annotation placed on a pod template to opt
	// into cluster-wide proxy injection. The value is a comma-separated list of
	// container names that should receive HTTP_PROXY, HTTPS_PROXY, and NO_PROXY
	// environment variables. The annotation is added by manifests-gen and can be
	// overridden by provider manifests.
	ProxyInjectAnnotation = operatorstatus.CAPIOperatorIdentifierDomain + "/inject-proxy"

	// proxyVarNames are the only env var names this controller ever owns.
	// Used for the skip-if-unchanged optimisation.

	proxyClusterName = "cluster"
)

// proxyVarNames returns the env var names this controller manages. These are
// the only names that can appear in the SSA patch from this field manager, so
// comparing them against the current state is sufficient to skip redundant
// applies.
func proxyVarNames() []string { return []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} }

// Controller watches managed workloads that opt in to cluster-wide proxy
// injection via the ProxyInjectAnnotation annotation and applies the current
// proxy environment variables (HTTP_PROXY, HTTPS_PROXY, NO_PROXY) to the
// named containers via SSA. It reconciles whenever the Proxy CR changes or
// when a managed workload is created or updated.
type Controller struct {
	client        client.Client
	trackingCache managedcache.TrackingCache
}

// SetupWithManager registers the ProxyController with the Manager. It shares
// the trackingCache returned by installer.SetupWithManager so it can list
// managed objects without creating a second cache.
func SetupWithManager(mgr ctrl.Manager, trackingCache managedcache.TrackingCache) error {
	c := &Controller{
		client:        mgr.GetClient(),
		trackingCache: trackingCache,
	}

	toProxy := func(_ context.Context, _ client.Object) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: proxyClusterName}}}
	}

	// Watch only managed workloads (those labelled by the installer).
	// Using standard controller-runtime Watches instead of trackingCache.Source()
	// avoids registering a handler on the TrackingCache, which prevents it from
	// stopping cleanly and can leave orphaned envtest processes.
	managedReq, err := labels.NewRequirement(revisiongenerator.ManagedLabelKey, selection.Exists, nil)
	if err != nil {
		return fmt.Errorf("building managed label requirement: %w", err)
	}

	isManagedWorkload := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return labels.NewSelector().Add(*managedReq).Matches(labels.Set(obj.GetLabels()))
	})

	err = ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		Watches(
			&configv1.Proxy{},
			handler.EnqueueRequestsFromMapFunc(toProxy),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				return obj.GetName() == proxyClusterName
			})),
		).
		Watches(&appsv1.Deployment{}, handler.EnqueueRequestsFromMapFunc(toProxy),
			builder.WithPredicates(isManagedWorkload)).
		Watches(&appsv1.DaemonSet{}, handler.EnqueueRequestsFromMapFunc(toProxy),
			builder.WithPredicates(isManagedWorkload)).
		Watches(&appsv1.StatefulSet{}, handler.EnqueueRequestsFromMapFunc(toProxy),
			builder.WithPredicates(isManagedWorkload)).
		Complete(c)
	if err != nil {
		return fmt.Errorf("failed to create proxy controller: %w", err)
	}

	return nil
}

// workloadItem collects the fields needed to reconcile proxy env vars on a
// single Deployment, DaemonSet, or StatefulSet without storing a reference to
// the concrete typed object.
type workloadItem struct {
	apiVersion     string
	kind           string
	name           string
	namespace      string
	podAnnotations map[string]string
	containers     []corev1.Container
}

// Reconcile reads the cluster-wide proxy configuration and applies the proxy
// environment variables to all managed workloads that carry the inject-proxy
// annotation.
func (c *Controller) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName(controllerName)
	log.Info("Reconciling proxy environment variables")

	proxyVars, err := util.GetProxyEnvVars(ctx, c.client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching cluster-wide proxy: %w", err)
	}

	if err := c.applyToAllWorkloads(ctx, proxyVars); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Proxy environment variables reconciled")

	return ctrl.Result{}, nil
}

// containerNamesFromAnnotation parses the comma-separated inject-proxy annotation value.
func containerNamesFromAnnotation(annotation string) []string {
	var names []string

	for _, name := range strings.Split(annotation, ",") {
		if n := strings.TrimSpace(name); n != "" {
			names = append(names, n)
		}
	}

	return names
}

// applyToAllWorkloads collects all managed Deployments, DaemonSets, and
// StatefulSets from the tracking cache and reconciles their proxy env vars.
// It continues past per-object errors and returns all failures combined.
func (c *Controller) applyToAllWorkloads(ctx context.Context, proxyVars []corev1.EnvVar) error {
	items, err := c.collectWorkloads(ctx)
	if err != nil {
		return err
	}

	var errs []error

	for _, item := range items {
		if err := c.applyProxyVars(ctx, item, proxyVars); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// collectWorkloads lists all managed Deployments, DaemonSets, and StatefulSets
// from the tracking cache and returns them as workloadItems.
func (c *Controller) collectWorkloads(ctx context.Context) ([]workloadItem, error) {
	var items []workloadItem

	deployList := &appsv1.DeploymentList{}
	if err := c.trackingCache.List(ctx, deployList); err != nil {
		return nil, fmt.Errorf("listing managed deployments: %w", err)
	}

	for i := range deployList.Items {
		d := &deployList.Items[i]
		items = append(items, workloadItem{
			apiVersion:     "apps/v1",
			kind:           "Deployment",
			name:           d.Name,
			namespace:      d.Namespace,
			podAnnotations: d.Spec.Template.Annotations,
			containers:     d.Spec.Template.Spec.Containers,
		})
	}

	dsList := &appsv1.DaemonSetList{}
	if err := c.trackingCache.List(ctx, dsList); err != nil {
		return nil, fmt.Errorf("listing managed daemonsets: %w", err)
	}

	for i := range dsList.Items {
		ds := &dsList.Items[i]
		items = append(items, workloadItem{
			apiVersion:     "apps/v1",
			kind:           "DaemonSet",
			name:           ds.Name,
			namespace:      ds.Namespace,
			podAnnotations: ds.Spec.Template.Annotations,
			containers:     ds.Spec.Template.Spec.Containers,
		})
	}

	ssList := &appsv1.StatefulSetList{}
	if err := c.trackingCache.List(ctx, ssList); err != nil {
		return nil, fmt.Errorf("listing managed statefulsets: %w", err)
	}

	for i := range ssList.Items {
		ss := &ssList.Items[i]
		items = append(items, workloadItem{
			apiVersion:     "apps/v1",
			kind:           "StatefulSet",
			name:           ss.Name,
			namespace:      ss.Namespace,
			podAnnotations: ss.Spec.Template.Annotations,
			containers:     ss.Spec.Template.Spec.Containers,
		})
	}

	return items, nil
}

// applyProxyVars submits a single SSA apply for the workload covering ALL
// containers in the pod spec. This is required because SSA field manager
// semantics treat each apply as the complete desired state: sending two
// separate applies from the same field manager would cause the second to
// cancel the ownership established by the first.
//
// For each container:
//   - containers listed in the inject-proxy annotation receive proxy env vars
//   - all other containers receive an empty env list, removing any proxy env
//     var entries previously owned by this controller's SSA field manager
//
// The apply is skipped if the containers already reflect the desired proxy
// state, to avoid unnecessary KAS round-trips during installation (when the
// controller reconciles for every newly-applied managed object).
func (c *Controller) applyProxyVars(ctx context.Context, item workloadItem, proxyVars []corev1.EnvVar) error {
	log := ctrl.LoggerFrom(ctx).WithName(controllerName)

	if len(item.containers) == 0 {
		return nil
	}

	// Build the set of containers that should receive proxy env vars.
	annotatedSet := make(map[string]struct{})

	if annotation, ok := item.podAnnotations[ProxyInjectAnnotation]; ok {
		for _, name := range containerNamesFromAnnotation(annotation) {
			annotatedSet[name] = struct{}{}
		}
	}

	// Skip the apply if the containers already have the desired proxy state.
	// This avoids a KAS round-trip when the controller reconciles for every
	// newly-applied managed object during installation.
	if proxyVarsAlreadyApplied(item.containers, annotatedSet, proxyVars) {
		log.Info("Proxy env vars already up to date, skipping apply",
			"kind", item.kind, "name", item.name, "namespace", item.namespace)

		return nil
	}

	patch := buildPatch(item, annotatedSet, proxyVars)

	if err := c.client.Patch(ctx, patch, util.ApplyConfigPatch(patch.Object), operatorstatus.CAPIFieldOwner("proxy"), client.ForceOwnership); err != nil {
		return fmt.Errorf("applying proxy env vars to %s %s/%s: %w", item.kind, item.namespace, item.name, err)
	}

	log.Info("Applied proxy env vars", "kind", item.kind, "name", item.name, "namespace", item.namespace)

	return nil
}

// buildPatch constructs the unstructured SSA patch for the workload covering all
// containers: annotated containers get proxy env vars, others get an empty list.
func buildPatch(item workloadItem, annotatedSet map[string]struct{}, proxyVars []corev1.EnvVar) *unstructured.Unstructured {
	containerPatches := make([]map[string]interface{}, 0, len(item.containers))

	for _, container := range item.containers {
		envEntries := make([]map[string]interface{}, 0)

		if _, ok := annotatedSet[container.Name]; ok {
			for _, ev := range proxyVars {
				envEntries = append(envEntries, map[string]interface{}{
					"name":  ev.Name,
					"value": ev.Value,
				})
			}
		}

		containerPatches = append(containerPatches, map[string]interface{}{
			"name": container.Name,
			"env":  envEntries,
		})
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": item.apiVersion,
			"kind":       item.kind,
			"metadata": map[string]interface{}{
				"name":      item.name,
				"namespace": item.namespace,
			},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": containerPatches,
					},
				},
			},
		},
	}
}

// proxyVarsAlreadyApplied returns true when every container in the pod spec
// already carries exactly the desired proxy state:
//   - annotated containers have all proxy env vars with the correct values
//   - unannotated containers have none of the proxy env var names
//
// The check uses the current container state from the workloadItem (sourced
// from the tracking cache), which reflects the actual state on the cluster
// including any values previously applied by this controller.
func proxyVarsAlreadyApplied(
	containers []corev1.Container,
	annotatedSet map[string]struct{},
	proxyVars []corev1.EnvVar,
) bool {
	for _, container := range containers {
		_, isAnnotated := annotatedSet[container.Name]

		// Index this container's env by name for quick lookup.
		envByName := make(map[string]string, len(container.Env))
		for _, ev := range container.Env {
			envByName[ev.Name] = ev.Value
		}

		if isAnnotated && len(proxyVars) > 0 {
			// Annotated with proxy configured: all desired vars must be present with correct values.
			for _, desired := range proxyVars {
				if got, ok := envByName[desired.Name]; !ok || got != desired.Value {
					return false
				}
			}
		} else {
			// Not annotated, or annotated but proxy was cleared: none of our var
			// names may be present (covers both "never had proxy" and "proxy removed").
			for _, name := range proxyVarNames() {
				if _, ok := envByName[name]; ok {
					return false
				}
			}
		}
	}

	return true
}
