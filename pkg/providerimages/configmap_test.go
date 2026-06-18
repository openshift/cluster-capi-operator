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

package providerimages

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

var _ = Describe("ImageRefsFromConfigMap", func() {
	It("should return a map of provider names to image refs from ConfigMap data", func() {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "capi-installer-images",
				Namespace: "openshift-cluster-api-operator",
			},
			Data: map[string]string{
				"aws-cluster-api-controllers": "registry/aws@sha256:abc",
				"gcp-cluster-api-controllers": "registry/gcp@sha256:def",
			},
		}

		result, err := ImageRefsFromConfigMap(cm)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(sets.New[string](
			"registry/aws@sha256:abc",
			"registry/gcp@sha256:def",
		)))
	})

	It("should return an empty map when ConfigMap data is empty", func() {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "capi-installer-images",
				Namespace: "openshift-cluster-api-operator",
			},
			Data: map[string]string{},
		}

		result, err := ImageRefsFromConfigMap(cm)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeEmpty())
	})

	It("should return an error when ConfigMap is nil", func() {
		result, err := ImageRefsFromConfigMap(nil)
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(errConfigMapNil))
		Expect(result).To(BeNil())
	})
})
