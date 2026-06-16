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

package installer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	"pkg.package-operator.run/boxcutter/managedcache"
)

const (
	proxyControllerName = "ProxyController"

	// ProxyInjectAnnotation is the annotation placed on a pod template to opt
	// into cluster-wide proxy injection. The value is a comma-separated list of
	// container names that should receive HTTP_PROXY, HTTPS_PROXY, and NO_PROXY
	// environment variables. The annotation is added by manifests-gen and can be
	// overridden by provider manifests.
	ProxyInjectAnnotation = operatorstatus.CAPIOperatorIdentifierDomain + "/inject-proxy"

	proxyClusterName = "cluster"
)

// ProxyController watches managed workloads that opt in to cluster-wide proxy
// injection via the ProxyInjectAnnotation annotation and applies the current
// proxy environment variables (HTTP_PROXY, HTTPS_PROXY, NO_PROXY) to the
// named containers via SSA. It reconciles whenever the Proxy CR changes or
// when a managed workload is created or updated.
type ProxyController struct {
	client        client.Client
	trackingCache managedcache.TrackingCache
}

// SetupProxyController registers the ProxyController with the Manager. It
// shares the trackingCache returned by SetupWithManager so it can watch the
// same set of managed objects without creating a second cache.
func SetupProxyController(mgr ctrl.Manager, trackingCache managedcache.TrackingCache) error {
	c := &ProxyController{
		client:        mgr.GetClient(),
		trackingCache: trackingCache,
	}

	toProxy := func(_ context.Context, _ client.Object) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: proxyClusterName}}}
	}

	err := ctrl.NewControllerManagedBy(mgr).
		Named(proxyControllerName).
		Watches(
			&configv1.Proxy{},
			handler.EnqueueRequestsFromMapFunc(toProxy),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				return obj.GetName() == proxyClusterName
			})),
		).
		WatchesRawSource(
			c.trackingCache.Source(
				handler.EnqueueRequestsFromMapFunc(toProxy),
				predicate.Or(
					predicate.GenerationChangedPredicate{},
					noGenerationPredicate(),
					predicate.AnnotationChangedPredicate{},
				),
			),
		).
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
func (c *ProxyController) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName(proxyControllerName)
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
func (c *ProxyController) applyToAllWorkloads(ctx context.Context, proxyVars []corev1.EnvVar) error {
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
func (c *ProxyController) collectWorkloads(ctx context.Context) ([]workloadItem, error) {
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
// A single apply is submitted even if no containers carry the annotation, so
// that previously-owned env vars are always cleared correctly on opt-out.
func (c *ProxyController) applyProxyVars(ctx context.Context, item workloadItem, proxyVars []corev1.EnvVar) error {
	log := ctrl.LoggerFrom(ctx).WithName(proxyControllerName)

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

	// Build a single patch covering ALL containers. Annotated containers get
	// the proxy env vars; all others get an empty list to clear stale ownership.
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

	patch := &unstructured.Unstructured{
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

	if err := c.client.Patch(ctx, patch, util.ApplyConfigPatch(patch.Object), operatorstatus.CAPIFieldOwner("proxy"), client.ForceOwnership); err != nil {
		return fmt.Errorf("applying proxy env vars to %s %s/%s: %w", item.kind, item.namespace, item.name, err)
	}

	log.Info("Applied proxy env vars", "kind", item.kind, "name", item.name, "namespace", item.namespace)

	return nil
}
