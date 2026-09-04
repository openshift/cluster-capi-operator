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
	"reflect"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"
	metav1applyconfig "k8s.io/client-go/applyconfigurations/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	"pkg.package-operator.run/boxcutter/managedcache"

	appsv1 "k8s.io/api/apps/v1"
)

const (
	controllerName = "ProxyController"

	// ProxyInjectAnnotation is the annotation placed on a pod template to opt
	// into cluster-wide proxy injection. The value is a comma-separated list of
	// container names that should receive HTTP_PROXY, HTTPS_PROXY, and NO_PROXY
	// environment variables. The annotation is added by manifests-gen and can be
	// overridden by provider manifests.
	ProxyInjectAnnotation = operatorstatus.CAPIOperatorIdentifierDomain + "/inject-proxy"
)

// Controller applies cluster-wide proxy environment variables to managed
// workloads that carry the ProxyInjectAnnotation on their pod templates.
// It is used as a sub-reconciler called directly from the InstallerController
// rather than as a standalone controller, so that both share a single
// trackingCache.Source() registration and avoid the shutdown issues that arise
// from double-registering handlers on the TrackingCache.
type Controller struct {
	client        client.Client
	trackingCache managedcache.TrackingCache
	extractor     metav1applyconfig.UnstructuredExtractor
}

// New creates a Controller. The extractor must be created once at setup time
// (e.g. via metav1applyconfig.NewUnstructuredExtractor) because building it
// is expensive — it fetches the cluster's OpenAPI schema.
func New(c client.Client, tc managedcache.TrackingCache, extractor metav1applyconfig.UnstructuredExtractor) *Controller {
	return &Controller{
		client:        c,
		trackingCache: tc,
		extractor:     extractor,
	}
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
func (c *Controller) Reconcile(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx).WithName(controllerName)
	log.Info("Reconciling proxy environment variables")

	proxyVars, err := util.GetProxyEnvVars(ctx, c.client)
	if err != nil {
		return fmt.Errorf("fetching cluster-wide proxy: %w", err)
	}

	if err := c.applyToAllWorkloads(ctx, proxyVars); err != nil {
		return err
	}

	log.Info("Proxy environment variables reconciled")

	return nil
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
// The apply is skipped when the UnstructuredExtractor reports that what we
// currently own already matches the desired patch, avoiding unnecessary KAS
// round-trips during installation (when the controller reconciles for every
// newly-applied managed object).
func (c *Controller) applyProxyVars(ctx context.Context, item workloadItem, proxyVars []corev1.EnvVar) error {
	log := ctrl.LoggerFrom(ctx).WithName(controllerName)

	if len(item.containers) == 0 {
		return nil
	}

	// Build the set of containers that should receive proxy env vars.
	annotatedSet := sets.New[string]()

	if annotation, ok := item.podAnnotations[ProxyInjectAnnotation]; ok {
		for _, name := range containerNamesFromAnnotation(annotation) {
			annotatedSet.Insert(name)
		}
	}

	patch := buildPatch(item, annotatedSet, proxyVars)

	// Skip the apply if what we currently own already matches the desired
	// patch. This avoids a KAS round-trip on every reconcile during
	// installation. We use UnstructuredExtractor rather than inspecting the
	// full container env (which may contain vars owned by other managers) so
	// that we compare only our SSA-owned state against the desired state.
	if c.proxyVarsAlreadyApplied(ctx, item, patch) {
		log.Info("Proxy env vars already up to date, skipping apply",
			"kind", item.kind, "name", item.name, "namespace", item.namespace)

		return nil
	}

	if err := c.client.Patch(ctx, patch, util.ApplyConfigPatch(patch.Object), operatorstatus.CAPIFieldOwner("proxy"), client.ForceOwnership); err != nil {
		return fmt.Errorf("applying proxy env vars to %s %s/%s: %w", item.kind, item.namespace, item.name, err)
	}

	log.Info("Applied proxy env vars", "kind", item.kind, "name", item.name, "namespace", item.namespace)

	return nil
}

// proxyVarsAlreadyApplied returns true when the fields we own (as reported by
// UnstructuredExtractor for our field manager) already match the desired patch.
// This is the correct check because it inspects only what WE own, not the full
// container env — other field managers may legitimately set proxy var names on
// containers we do not annotate, and those should not trigger our reconcile.
func (c *Controller) proxyVarsAlreadyApplied(ctx context.Context, item workloadItem, desired *unstructured.Unstructured) bool {
	// Fetch the current object from the tracking cache to get its managed fields.
	current := &unstructured.Unstructured{}
	current.SetAPIVersion(item.apiVersion)
	current.SetKind(item.kind)

	if err := c.trackingCache.Get(ctx, client.ObjectKey{Name: item.name, Namespace: item.namespace}, current); err != nil {
		return false
	}

	extracted, err := c.extractor.Extract(current, string(operatorstatus.CAPIFieldOwner("proxy")))
	if err != nil {
		return false
	}

	return reflect.DeepEqual(extracted.Object, desired.Object)
}

// buildPatch constructs the unstructured SSA patch for the workload covering all
// containers: annotated containers get proxy env vars, others get an empty list.
func buildPatch(item workloadItem, annotatedSet sets.Set[string], proxyVars []corev1.EnvVar) *unstructured.Unstructured {
	containerPatches := make([]map[string]interface{}, 0, len(item.containers))

	for _, container := range item.containers {
		envEntries := make([]map[string]interface{}, 0)

		if annotatedSet.Has(container.Name) {
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
