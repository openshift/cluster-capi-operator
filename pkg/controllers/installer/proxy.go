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
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

// ProxyInjectAnnotation opts the named workload containers into proxy injection.
const ProxyInjectAnnotation = operatorstatus.CAPIOperatorIdentifierDomain + "/inject-proxy"

const proxyConfigHashAnnotation = operatorstatus.CAPIOperatorIdentifierDomain + "/proxy-config-hash"

var (
	errEmptyProxyContainerList = errors.New("inject-proxy annotation must name at least one container")
	errUnknownProxyContainer   = errors.New("inject-proxy annotation names an unknown container")
)

func injectProxyEnvVars(objects []*unstructured.Unstructured, proxyEnvVars []corev1.EnvVar) ([]*unstructured.Unstructured, error) {
	transformed := make([]*unstructured.Unstructured, 0, len(objects))

	for _, obj := range objects {
		updated, err := injectProxyEnvVarsIntoObject(obj, proxyEnvVars)
		if err != nil {
			return nil, err
		}

		transformed = append(transformed, updated)
	}

	return transformed, nil
}

func injectProxyEnvVarsIntoObject(obj *unstructured.Unstructured, proxyEnvVars []corev1.EnvVar) (*unstructured.Unstructured, error) {
	gvk := obj.GroupVersionKind()
	if gvk.Group != "apps" || gvk.Version != "v1" {
		return obj, nil
	}

	switch obj.GetKind() {
	case "Deployment":
		workload := &appsv1.Deployment{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, workload); err != nil {
			return nil, fmt.Errorf("converting Deployment %s/%s: %w", obj.GetNamespace(), obj.GetName(), err)
		}

		if err := injectProxyEnvVarsIntoPodTemplate(obj, &workload.Spec.Template, proxyEnvVars); err != nil {
			return nil, err
		}

		return workloadToUnstructured(workload)
	case "DaemonSet":
		workload := &appsv1.DaemonSet{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, workload); err != nil {
			return nil, fmt.Errorf("converting DaemonSet %s/%s: %w", obj.GetNamespace(), obj.GetName(), err)
		}

		if err := injectProxyEnvVarsIntoPodTemplate(obj, &workload.Spec.Template, proxyEnvVars); err != nil {
			return nil, err
		}

		return workloadToUnstructured(workload)
	case "StatefulSet":
		workload := &appsv1.StatefulSet{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, workload); err != nil {
			return nil, fmt.Errorf("converting StatefulSet %s/%s: %w", obj.GetNamespace(), obj.GetName(), err)
		}

		if err := injectProxyEnvVarsIntoPodTemplate(obj, &workload.Spec.Template, proxyEnvVars); err != nil {
			return nil, err
		}

		return workloadToUnstructured(workload)
	default:
		return obj, nil
	}
}

func workloadToUnstructured(workload runtime.Object) (*unstructured.Unstructured, error) {
	obj, err := toUnstructured(workload)
	if err != nil {
		return nil, err
	}

	if err := addEmptyProxyEnvLists(obj); err != nil {
		return nil, err
	}

	return obj, nil
}

// addEmptyProxyEnvLists ensures an opted-in container with no remaining env
// vars renders as env: []. Empty Go slices are omitted by JSON serialization;
// rendering the empty list is required for server-side apply to remove proxy
// vars which this controller applied earlier.
func addEmptyProxyEnvLists(obj *unstructured.Unstructured) error {
	annotations, found, err := unstructured.NestedStringMap(obj.Object, "spec", "template", "metadata", "annotations")
	if err != nil {
		return fmt.Errorf("getting pod template annotations: %w", err)
	}

	if !found {
		return nil
	}

	targets := proxyContainerNames(annotations[ProxyInjectAnnotation])
	if len(targets) == 0 {
		return nil
	}

	containers, found, err := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
	if err != nil {
		return fmt.Errorf("getting pod template containers: %w", err)
	}

	if !found {
		return nil
	}

	for i, container := range containers {
		containerMap, ok := container.(map[string]interface{})
		if !ok {
			continue
		}

		name, ok := containerMap["name"].(string)
		if !ok || !containsString(targets, name) {
			continue
		}

		if _, hasEnv := containerMap["env"]; !hasEnv {
			containerMap["env"] = []interface{}{}
			containers[i] = containerMap
		}
	}

	if err := unstructured.SetNestedSlice(obj.Object, containers, "spec", "template", "spec", "containers"); err != nil {
		return fmt.Errorf("setting pod template containers: %w", err)
	}

	return nil
}

func injectProxyEnvVarsIntoPodTemplate(obj *unstructured.Unstructured, template *corev1.PodTemplateSpec, proxyEnvVars []corev1.EnvVar) error {
	annotation, ok := template.Annotations[ProxyInjectAnnotation]
	if !ok {
		return nil
	}

	targets := proxyContainerNames(annotation)
	if len(targets) == 0 {
		return fmt.Errorf("%w: %s annotation on %s %s/%s", errEmptyProxyContainerList, ProxyInjectAnnotation, obj.GetKind(), obj.GetNamespace(), obj.GetName())
	}

	for _, target := range targets {
		if !hasContainer(template.Spec.Containers, target) {
			return fmt.Errorf("%w: %s annotation on %s %s/%s: %q", errUnknownProxyContainer, ProxyInjectAnnotation, obj.GetKind(), obj.GetNamespace(), obj.GetName(), target)
		}
	}

	for i := range template.Spec.Containers {
		container := &template.Spec.Containers[i]
		if !containsString(targets, container.Name) {
			continue
		}

		filtered := make([]corev1.EnvVar, 0, len(container.Env)+len(proxyEnvVars))
		for _, envVar := range container.Env {
			if !isProxyEnvVar(envVar.Name) {
				filtered = append(filtered, envVar)
			}
		}

		container.Env = filtered
		container.Env = append(container.Env, proxyEnvVars...)
	}

	template.Annotations[proxyConfigHashAnnotation] = proxyEnvVarsHash(proxyEnvVars)

	return nil
}

func proxyEnvVarsHash(proxyEnvVars []corev1.EnvVar) string {
	hash := sha256.New()
	for _, envVar := range proxyEnvVars {
		_, _ = fmt.Fprintf(hash, "%s=%s\n", envVar.Name, envVar.Value)
	}

	return fmt.Sprintf("%x", hash.Sum(nil))
}

func proxyContainerNames(annotation string) []string {
	var names []string

	for _, name := range strings.Split(annotation, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}

	return names
}

func isProxyEnvVar(name string) bool {
	switch name {
	case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY":
		return true
	default:
		return false
	}
}

func hasContainer(containers []corev1.Container, name string) bool {
	for _, container := range containers {
		if container.Name == name {
			return true
		}
	}

	return false
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}

	return false
}

func toUnstructured(obj runtime.Object) (*unstructured.Unstructured, error) {
	object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("converting %T to unstructured: %w", obj, err)
	}

	return &unstructured.Unstructured{Object: object}, nil
}
