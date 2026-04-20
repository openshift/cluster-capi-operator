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
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestProxyEnvVars(t *testing.T) {
	for _, tc := range []struct {
		name     string
		proxy    *configv1.Proxy
		wantLen  int
		wantNil  bool
		wantVars map[string]string
	}{
		{
			name: "all fields set",
			proxy: &configv1.Proxy{
				Status: configv1.ProxyStatus{
					HTTPProxy:  "http://proxy:3128",
					HTTPSProxy: "https://proxy:3129",
					NoProxy:    ".cluster.local",
				},
			},
			wantLen: 3,
			wantVars: map[string]string{
				"HTTP_PROXY":  "http://proxy:3128",
				"HTTPS_PROXY": "https://proxy:3129",
				"NO_PROXY":    ".cluster.local",
			},
		},
		{
			name: "only HTTPProxy set",
			proxy: &configv1.Proxy{
				Status: configv1.ProxyStatus{
					HTTPProxy: "http://proxy:3128",
				},
			},
			wantLen: 1,
			wantVars: map[string]string{
				"HTTP_PROXY": "http://proxy:3128",
			},
		},
		{
			name:    "empty proxy",
			proxy:   &configv1.Proxy{},
			wantNil: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			envVars := ProxyEnvVars(tc.proxy)
			if tc.wantNil {
				g.Expect(envVars).To(BeNil())
				return
			}

			g.Expect(envVars).To(HaveLen(tc.wantLen))

			for _, ev := range envVars {
				g.Expect(ev.Value).To(Equal(tc.wantVars[ev.Name]), "env var %s", ev.Name)
			}
		})
	}
}

func TestInjectProxyEnvVarsIntoUnstructured(t *testing.T) {
	proxyEnvVars := []corev1.EnvVar{
		{Name: "HTTP_PROXY", Value: "http://proxy:3128"},
		{Name: "HTTPS_PROXY", Value: "https://proxy:3129"},
		{Name: "NO_PROXY", Value: ".cluster.local"},
	}

	for _, tc := range []struct {
		name               string
		obj                *unstructured.Unstructured
		envVars            []corev1.EnvVar
		wantEnvCount       int // per container
		wantContainers     int // expected number of containers with env vars
		wantInitEnvCount   int // per init container, 0 means don't check
		wantInitContainers int
		wantSkipInspect    bool
		wantEnvValues      map[string]string // optional: verify specific env var values
		wantValueFromNames []string          // optional: verify valueFrom fields are preserved
	}{
		{
			name:            "ConfigMap (non-workload) is a no-op",
			obj:             makeUnstructuredConfigMap("test-cm"),
			envVars:         proxyEnvVars,
			wantSkipInspect: true,
		},
		{
			name:           "existing env var preserved and not duplicated",
			obj:            makeUnstructuredWorkload([]string{"mgr"}, map[string]string{"HTTP_PROXY": "custom"}, nil),
			envVars:        proxyEnvVars,
			wantEnvCount:   3, // 1 existing (preserved) + 2 new
			wantContainers: 1,
			wantEnvValues:  map[string]string{"HTTP_PROXY": "custom"},
		},
		{
			name:            "empty env vars slice is a no-op",
			obj:             makeUnstructuredWorkload([]string{"mgr"}, nil, nil),
			envVars:         nil,
			wantSkipInspect: true,
		},
		{
			name:           "container with existing non-proxy env preserved",
			obj:            makeUnstructuredWorkload([]string{"mgr"}, map[string]string{"FOO": "bar"}, nil),
			envVars:        proxyEnvVars,
			wantEnvCount:   4, // 1 existing + 3 proxy
			wantContainers: 1,
			wantEnvValues:  map[string]string{"FOO": "bar"},
		},
		{
			name:           "multiple containers all get env vars",
			obj:            makeUnstructuredWorkload([]string{"a", "b"}, nil, nil),
			envVars:        proxyEnvVars,
			wantEnvCount:   3,
			wantContainers: 2,
		},
		{
			name:               "init containers get env vars",
			obj:                makeUnstructuredWorkload([]string{"main"}, nil, []string{"init"}),
			envVars:            proxyEnvVars,
			wantEnvCount:       3,
			wantContainers:     1,
			wantInitEnvCount:   3,
			wantInitContainers: 1,
		},
		{
			name:               "existing valueFrom env var is preserved",
			obj:                makeUnstructuredWorkloadWithValueFrom("mgr", "SECRET_KEY", "my-secret", "key"),
			envVars:            proxyEnvVars,
			wantEnvCount:       4, // 1 valueFrom + 3 proxy
			wantContainers:     1,
			wantValueFromNames: []string{"SECRET_KEY"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			err := InjectProxyEnvVarsIntoUnstructured(tc.obj, tc.envVars)
			g.Expect(err).NotTo(HaveOccurred())

			if tc.wantSkipInspect {
				return
			}

			containers, _, _ := unstructured.NestedSlice(tc.obj.Object, "spec", "template", "spec", "containers")
			g.Expect(containers).To(HaveLen(tc.wantContainers))

			for _, c := range containers {
				container := c.(map[string]interface{})
				env, _, _ := unstructured.NestedSlice(container, "env")
				g.Expect(env).To(HaveLen(tc.wantEnvCount))

				for wantName, wantValue := range tc.wantEnvValues {
					g.Expect(env).To(ContainElement(SatisfyAll(
						HaveKeyWithValue("name", wantName),
						HaveKeyWithValue("value", wantValue),
					)))
				}

				for _, wantName := range tc.wantValueFromNames {
					g.Expect(env).To(ContainElement(SatisfyAll(
						HaveKeyWithValue("name", wantName),
						HaveKey("valueFrom"),
						Not(HaveKey("value")),
					)))
				}
			}

			if tc.wantInitContainers > 0 {
				initContainers, _, _ := unstructured.NestedSlice(tc.obj.Object, "spec", "template", "spec", "initContainers")
				g.Expect(initContainers).To(HaveLen(tc.wantInitContainers))

				for _, c := range initContainers {
					container := c.(map[string]interface{})
					env, _, _ := unstructured.NestedSlice(container, "env")
					g.Expect(env).To(HaveLen(tc.wantInitEnvCount))
				}
			}
		})
	}
}

// Test helpers

func makeUnstructuredWorkload(containerNames []string, existingEnv map[string]string, initContainerNames []string) *unstructured.Unstructured {
	envList := make([]interface{}, 0, len(existingEnv))
	for k, v := range existingEnv {
		envList = append(envList, map[string]interface{}{"name": k, "value": v})
	}

	containers := make([]interface{}, len(containerNames))
	for i, name := range containerNames {
		c := map[string]interface{}{
			"name":  name,
			"image": "registry.example.com/test:latest",
		}
		if i == 0 && len(envList) > 0 {
			c["env"] = envList
		}

		containers[i] = c
	}

	podSpec := map[string]interface{}{
		"containers": containers,
	}

	if len(initContainerNames) > 0 {
		initContainers := make([]interface{}, len(initContainerNames))
		for i, name := range initContainerNames {
			initContainers[i] = map[string]interface{}{
				"name":  name,
				"image": "registry.example.com/init:latest",
			}
		}

		podSpec["initContainers"] = initContainers
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata":   map[string]interface{}{"name": "test", "namespace": "default"},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"spec": podSpec,
				},
			},
		},
	}
}

func makeUnstructuredWorkloadWithValueFrom(containerName, envName, secretName, secretKey string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata":   map[string]interface{}{"name": "test", "namespace": "default"},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  containerName,
								"image": "registry.example.com/test:latest",
								"env": []interface{}{
									map[string]interface{}{
										"name": envName,
										"valueFrom": map[string]interface{}{
											"secretKeyRef": map[string]interface{}{
												"name": secretName,
												"key":  secretKey,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func makeUnstructuredConfigMap(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   map[string]interface{}{"name": name, "namespace": "default"},
			"data":       map[string]interface{}{"key": "value"},
		},
	}
}
