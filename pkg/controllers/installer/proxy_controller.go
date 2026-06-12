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
	ProxyInjectAnnotation = "capi-operator.openshift.io/inject-proxy"

	// proxyFieldManager is the SSA field manager name for proxy env vars. It
	// must be distinct from boxcutter's field manager so that the two controllers
	// can own disjoint sets of fields on the same Deployment/DaemonSet/StatefulSet.
	proxyFieldManager = "capi-operator-proxy"

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

	var errs []error

	for _, applyFn := range []func(context.Context, []corev1.EnvVar) error{
		c.applyToDeployments,
		c.applyToDaemonSets,
		c.applyToStatefulSets,
	} {
		if err := applyFn(ctx, proxyVars); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		combined := errs[0]
		for _, e := range errs[1:] {
			combined = fmt.Errorf("%w; %w", combined, e)
		}

		return ctrl.Result{}, combined
	}

	log.Info("Proxy environment variables reconciled")

	return ctrl.Result{}, nil
}

func (c *ProxyController) applyToDeployments(ctx context.Context, proxyVars []corev1.EnvVar) error {
	list := &appsv1.DeploymentList{}
	if err := c.trackingCache.List(ctx, list); err != nil {
		return fmt.Errorf("listing managed deployments: %w", err)
	}

	for i := range list.Items {
		deploy := &list.Items[i]
		annotation, ok := deploy.Spec.Template.Annotations[ProxyInjectAnnotation]

		if !ok {
			continue
		}

		if err := c.applyProxyVars(ctx, "apps/v1", "Deployment", deploy.Name, deploy.Namespace, annotation, proxyVars); err != nil {
			return err
		}
	}

	return nil
}

func (c *ProxyController) applyToDaemonSets(ctx context.Context, proxyVars []corev1.EnvVar) error {
	list := &appsv1.DaemonSetList{}
	if err := c.trackingCache.List(ctx, list); err != nil {
		return fmt.Errorf("listing managed daemonsets: %w", err)
	}

	for i := range list.Items {
		ds := &list.Items[i]

		annotation, ok := ds.Spec.Template.Annotations[ProxyInjectAnnotation]

		if !ok {
			continue
		}

		if err := c.applyProxyVars(ctx, "apps/v1", "DaemonSet", ds.Name, ds.Namespace, annotation, proxyVars); err != nil {
			return err
		}
	}

	return nil
}

func (c *ProxyController) applyToStatefulSets(ctx context.Context, proxyVars []corev1.EnvVar) error {
	list := &appsv1.StatefulSetList{}
	if err := c.trackingCache.List(ctx, list); err != nil {
		return fmt.Errorf("listing managed statefulsets: %w", err)
	}

	for i := range list.Items {
		ss := &list.Items[i]

		annotation, ok := ss.Spec.Template.Annotations[ProxyInjectAnnotation]

		if !ok {
			continue
		}

		if err := c.applyProxyVars(ctx, "apps/v1", "StatefulSet", ss.Name, ss.Namespace, annotation, proxyVars); err != nil {
			return err
		}
	}

	return nil
}

// applyProxyVars SSA-applies the proxy env vars to each container named in the
// comma-separated annotation value. The proxy controller owns only the three
// proxy env var entries; all other fields remain owned by boxcutter.
func (c *ProxyController) applyProxyVars(
	ctx context.Context,
	apiVersion, kind, name, namespace, annotation string,
	proxyVars []corev1.EnvVar,
) error {
	log := ctrl.LoggerFrom(ctx).WithName(proxyControllerName)

	containerNames := strings.Split(annotation, ",")
	containerPatches := make([]map[string]interface{}, 0, len(containerNames))

	envEntries := proxyEnvEntries(proxyVars)

	for _, containerName := range containerNames {
		containerName = strings.TrimSpace(containerName)
		if containerName == "" {
			continue
		}

		containerPatches = append(containerPatches, map[string]interface{}{
			"name": containerName,
			"env":  envEntries,
		})
	}

	if len(containerPatches) == 0 {
		return nil
	}

	patch := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": apiVersion,
			"kind":       kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
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

	if err := c.client.Patch(ctx, patch, util.ApplyConfigPatch(patch.Object), client.FieldOwner(proxyFieldManager), client.ForceOwnership); err != nil {
		return fmt.Errorf("applying proxy env vars to %s %s/%s: %w", kind, namespace, name, err)
	}

	log.Info("Applied proxy env vars", "kind", kind, "name", name, "namespace", namespace, "containers", containerNames)

	return nil
}

// proxyEnvEntries converts the proxy env var slice to the unstructured map
// representation used in the SSA patch. An empty slice results in an empty
// list, which causes the proxy controller's previously-owned env entries to be
// removed (when the proxy is unconfigured).
func proxyEnvEntries(vars []corev1.EnvVar) []map[string]interface{} {
	entries := make([]map[string]interface{}, 0, len(vars))
	for _, v := range vars {
		entries = append(entries, map[string]interface{}{
			"name":  v.Name,
			"value": v.Value,
		})
	}

	return entries
}
