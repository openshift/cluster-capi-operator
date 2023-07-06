/*
Copyright 2022 The Kubernetes Authors.

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
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
)

const (
	deploymentKind       = "Deployment"
	namespaceKind        = "Namespace"
	managerContainerName = "manager"
	defaultVerbosity     = 1
)

var (
	bool2Str = map[bool]string{true: "true", false: "false"}
)

// customizeObjectsFn apply provider specific customization to a list of manifests.
func customizeObjectsFn(provider provider, images map[string]string) func(objs []unstructured.Unstructured) ([]unstructured.Unstructured, error) {
	return func(objs []unstructured.Unstructured) ([]unstructured.Unstructured, error) {
		results := []unstructured.Unstructured{}
		for i := range objs {
			o := objs[i]

			if o.GetKind() == namespaceKind {
				// filter out namespaces as the targetNamespace already exists as the provider object is in it.
				continue
			}

			// if o.GetNamespace() != "" {
			// 	// only set the ownership on namespaced objects.
			// 	o.SetOwnerReferences(util.EnsureOwnerRef(provider.GetOwnerReferences(),
			// 		metav1.OwnerReference{
			// 			APIVersion: operatorv1.GroupVersion.String(),
			// 			Kind:       provider.GetObjectKind().GroupVersionKind().Kind,
			// 			Name:       provider.GetName(),
			// 			UID:        provider.GetUID(),
			// 		}))
			// }

			if o.GetKind() == deploymentKind {
				d := &appsv1.Deployment{}
				if err := scheme.Scheme.Convert(&o, d, nil); err != nil {
					return nil, err
				}
				customizeContainers(provider, d, images)
				if err := scheme.Scheme.Convert(d, &o, nil); err != nil {
					return nil, err
				}
			}
			results = append(results, o)
		}
		return results, nil
	}
}

// customizeContainer customize provider container base on provider spec input.
func customizeContainers(provider provider, d *appsv1.Deployment, images map[string]string) {
	for j, c := range d.Spec.Template.Spec.Containers {
		if c.Name == "manager" {
			c.Image = images[providerNameToImageKey(provider.Name)]
			c.Command = []string{providerNameToCommand(provider.Name)}
			d.Spec.Template.Spec.Containers[j] = c
		}
	}
}

func providerNameToImageKey(name string) string {
	switch name {
	case "aws":
		return "aws-cluster-api-controllers"
	case "azure":
		return "azure-cluster-api-controllers"
	case "gcp":
		return "gcp-cluster-api-controllers"
	case "vsphere":
		return "vsphere-cluster-api-controllers"
	case "ibmcloud":
		return "ibmcloud-cluster-api-controllers"
	case "openstack":
		return "openstack-cluster-api-controllers"
	case "cluster-api":
		return "cluster-capi-controllers"
	default:
		return "none"
	}
}

func providerNameToCommand(name string) string {
	switch name {
	case "aws", "azure", "gcp", "vsphere", "ibmcloud", "openstack":
		return "./bin/cluster-api-provider-" + name + "-controller-manager"
	case "cluster-api":
		return "./bin/cluster-api-controller-manager"
	default:
		return "./manager"
	}
}
