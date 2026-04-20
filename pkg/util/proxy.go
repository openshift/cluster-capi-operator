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
package util

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	proxyResourceName = "cluster"
)

// GetProxy returns the cluster-wide Proxy resource.
func GetProxy(ctx context.Context, cl client.Reader) (*configv1.Proxy, error) {
	proxy := &configv1.Proxy{}

	if err := cl.Get(ctx, client.ObjectKey{Name: proxyResourceName}, proxy); err != nil {
		return nil, fmt.Errorf("failed to get proxy %q: %w", proxyResourceName, err)
	}

	return proxy, nil
}

// ProxyEnvVars converts Proxy Status fields to environment variables.
// Only non-empty fields are included.
// Returns nil if all proxy fields are empty.
func ProxyEnvVars(proxy *configv1.Proxy) []corev1.EnvVar {
	if proxy == nil {
		return nil
	}

	var envVars []corev1.EnvVar

	if proxy.Status.HTTPProxy != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "HTTP_PROXY",
			Value: proxy.Status.HTTPProxy,
		})
	}

	if proxy.Status.HTTPSProxy != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "HTTPS_PROXY",
			Value: proxy.Status.HTTPSProxy,
		})
	}

	if proxy.Status.NoProxy != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "NO_PROXY",
			Value: proxy.Status.NoProxy,
		})
	}

	return envVars
}

// InjectProxyEnvVarsIntoUnstructured injects proxy environment variables into
// all containers and initContainers of Deployment unstructured objects.
// Skips if an env var with the same name already exists. No-op for other
// kinds or if envVars is empty.
func InjectProxyEnvVarsIntoUnstructured(obj *unstructured.Unstructured, envVars []corev1.EnvVar) error {
	if len(envVars) == 0 {
		return nil
	}

	if obj.GetKind() != "Deployment" {
		return nil
	}

	containerPaths := [][]string{
		{"spec", "template", "spec", "containers"},
		{"spec", "template", "spec", "initContainers"},
	}

	for _, path := range containerPaths {
		if err := injectEnvVarsAtPath(obj, path, envVars); err != nil {
			return err
		}
	}

	return nil
}

func injectEnvVarsAtPath(obj *unstructured.Unstructured, path []string, envVars []corev1.EnvVar) error {
	containers, found, err := unstructured.NestedSlice(obj.Object, path...)
	if err != nil || !found {
		return nil //nolint:nilerr
	}

	for i, c := range containers {
		container, ok := c.(map[string]interface{})
		if !ok {
			continue
		}

		if err := injectEnvVarsIntoContainer(container, i, envVars); err != nil {
			return err
		}

		containers[i] = container
	}

	if err := unstructured.SetNestedSlice(obj.Object, containers, path...); err != nil {
		return fmt.Errorf("setting containers at %v: %w", path, err)
	}

	return nil
}

func injectEnvVarsIntoContainer(container map[string]interface{}, index int, envVars []corev1.EnvVar) error {
	existingEnv, _, _ := unstructured.NestedSlice(container, "env")

	// Build a set of existing env var names to avoid duplicates.
	names := make(map[string]bool, len(existingEnv))
	for _, e := range existingEnv {
		if envMap, ok := e.(map[string]interface{}); ok {
			if name, ok := envMap["name"].(string); ok {
				names[name] = true
			}
		}
	}

	// Append new env vars directly as unstructured maps, preserving
	// existing entries (including any valueFrom fields) untouched.
	for _, ev := range envVars {
		if names[ev.Name] {
			continue
		}

		existingEnv = append(existingEnv, map[string]interface{}{
			"name":  ev.Name,
			"value": ev.Value,
		})
	}

	if err := unstructured.SetNestedSlice(container, existingEnv, "env"); err != nil {
		return fmt.Errorf("setting env vars on container %d: %w", index, err)
	}

	return nil
}
