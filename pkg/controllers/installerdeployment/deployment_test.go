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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/sets"
)

var _ = Describe("buildDesiredDeployment", func() {
	const (
		testImage = "quay.io/openshift/cluster-capi-operator:latest"
	)

	It("should parse the embedded YAML and overlay dynamic fields", func() {
		deployment := buildDesiredDeployment(testImage, testNamespace, sets.New[string]())

		Expect(deployment.Name).To(Equal("capi-installer"))
		Expect(deployment.Namespace).To(Equal(testNamespace))
		Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal(testImage))
		// Verify the static base content survived YAML parsing.
		Expect(deployment.Spec.Template.Spec.ServiceAccountName).To(Equal("capi-installer"))
	})

	It("should create image volumes and mounts for all image refs", func() {
		imageRefs := sets.New(
			"registry/aws@sha256:abc",
			"registry/core@sha256:def",
		)

		deployment := buildDesiredDeployment(testImage, testNamespace, imageRefs)

		volumes := deployment.Spec.Template.Spec.Volumes
		// 2 image volumes + 1 metrics-cert volume from base.
		Expect(volumes).To(HaveLen(3))

		// Verify all image refs are present in volumes.
		var volumeImageRefs []string

		for _, vol := range volumes {
			if vol.Image != nil {
				volumeImageRefs = append(volumeImageRefs, vol.Image.Reference)
			}
		}

		Expect(volumeImageRefs).To(ConsistOf(
			"registry/aws@sha256:abc",
			"registry/core@sha256:def",
		))

		// Verify volume mounts include both image mounts and metrics-cert.
		container := deployment.Spec.Template.Spec.Containers[0]
		// 2 image mounts + 1 metrics-cert mount from base.
		Expect(container.VolumeMounts).To(HaveLen(3))
	})

	It("should produce deterministic output when called multiple times", func() {
		imageRefs := sets.New[string](
			"registry/gcp@sha256:123",
			"registry/aws@sha256:abc",
			"registry/core@sha256:def",
		)

		deployment1 := buildDesiredDeployment(testImage, testNamespace, imageRefs)

		deployment2 := buildDesiredDeployment(testImage, testNamespace, imageRefs)

		Expect(deployment1).To(Equal(deployment2))

		// Verify image volumes are sorted by name for determinism.
		var imageVolumeNames []string

		for _, vol := range deployment1.Spec.Template.Spec.Volumes {
			if vol.Image != nil {
				imageVolumeNames = append(imageVolumeNames, vol.Name)
			}
		}

		for i := 1; i < len(imageVolumeNames); i++ {
			Expect(imageVolumeNames[i] > imageVolumeNames[i-1]).To(BeTrue())
		}
	})

	It("should have only base volumes when imageRefs is empty", func() {
		deployment := buildDesiredDeployment(testImage, testNamespace, sets.New[string]())

		// Only the metrics-cert volume from the base.
		Expect(deployment.Spec.Template.Spec.Volumes).To(HaveLen(1))
		Expect(deployment.Spec.Template.Spec.Volumes[0].Name).To(Equal("metrics-cert"))

		// Only the metrics-cert volume mount from the base.
		Expect(deployment.Spec.Template.Spec.Containers[0].VolumeMounts).To(HaveLen(1))
		Expect(deployment.Spec.Template.Spec.Containers[0].VolumeMounts[0].Name).To(Equal("metrics-cert"))
	})
})
