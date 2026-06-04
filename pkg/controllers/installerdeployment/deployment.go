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

package installerdeployment

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/yaml"
)

const (
	deploymentName = "capi-installer"
)

var (
	//go:embed assets/deployment.yaml
	deploymentYAML []byte

	// staticDeployment holds the parsed deployment YAML.
	//nolint:gochecknoglobals
	staticDeployment = appsv1.Deployment{}
)

func init() {
	// Parse the embedded deployment YAML on startup
	if err := yaml.UnmarshalStrict(deploymentYAML, &staticDeployment); err != nil {
		panic(fmt.Errorf("failed to parse embedded deployment YAML: %w", err))
	}
}

// buildDesiredDeployment constructs the desired capi-installer Deployment spec
// by parsing the embedded YAML base and overlaying dynamic fields: container image,
// namespace, RELEASE_VERSION env var, and image volumes/mounts.
func buildDesiredDeployment(containerImage, namespace string, imageRefs sets.Set[string]) *appsv1.Deployment {
	deployment := staticDeployment.DeepCopy()

	// Overlay dynamic fields
	deployment.Namespace = namespace
	deployment.Spec.Template.Spec.Containers[0].Image = containerImage

	// Add RELEASE_VERSION env var from the operator's own environment
	releaseVersion := os.Getenv("RELEASE_VERSION")
	if releaseVersion == "" {
		releaseVersion = "0.0.1-snapshot"
	}

	deployment.Spec.Template.Spec.Containers[0].Env = append(
		deployment.Spec.Template.Spec.Containers[0].Env,
		corev1.EnvVar{
			Name:  "RELEASE_VERSION",
			Value: releaseVersion,
		},
	)

	// Build image volumes and volume mounts from image refs.
	// sets.List sorts image refs for deterministic output.
	for _, imageRef := range sets.List(imageRefs) {
		name := providerimages.VolumeNameForImageRef(imageRef)

		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: name,
				VolumeSource: corev1.VolumeSource{
					Image: &corev1.ImageVolumeSource{
						Reference:  imageRef,
						PullPolicy: corev1.PullIfNotPresent,
					},
				},
			},
		)

		deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(
			deployment.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{
				Name:      name,
				MountPath: fmt.Sprintf("%s/%s", providerimages.ProviderImageMountBase, name),
				ReadOnly:  true,
			},
		)
	}

	return deployment
}
