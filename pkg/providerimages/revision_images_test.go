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
	operatorv1 "github.com/openshift/api/operator/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

var _ = Describe("ImageRefsFromRevisions", func() {
	It("should return a set of image refs from distinct component images", func() {
		revisions := []operatorv1.ClusterAPIInstallerRevision{
			{
				Components: []operatorv1.ClusterAPIInstallerComponent{
					{
						Name: "core",
						Source: operatorv1.ClusterAPIInstallerComponentSource{
							Type: operatorv1.InstallerComponentTypeImage,
							Image: operatorv1.ClusterAPIInstallerComponentImage{
								Ref: "registry/core@sha256:abc123",
							},
						},
					},
					{
						Name: "aws-infrastructure",
						Source: operatorv1.ClusterAPIInstallerComponentSource{
							Type: operatorv1.InstallerComponentTypeImage,
							Image: operatorv1.ClusterAPIInstallerComponentImage{
								Ref: "registry/aws@sha256:def456",
							},
						},
					},
				},
			},
			{
				Components: []operatorv1.ClusterAPIInstallerComponent{
					{
						Name: "gcp-infrastructure",
						Source: operatorv1.ClusterAPIInstallerComponentSource{
							Type: operatorv1.InstallerComponentTypeImage,
							Image: operatorv1.ClusterAPIInstallerComponentImage{
								Ref: "registry/gcp@sha256:789abc",
							},
						},
					},
				},
			},
		}

		result := ImageRefsFromRevisions(revisions)
		Expect(result).To(Equal(sets.New[string](
			"registry/core@sha256:abc123",
			"registry/aws@sha256:def456",
			"registry/gcp@sha256:789abc",
		)))
	})

	It("should deduplicate overlapping image refs", func() {
		revisions := []operatorv1.ClusterAPIInstallerRevision{
			{
				Components: []operatorv1.ClusterAPIInstallerComponent{
					{
						Name: "core",
						Source: operatorv1.ClusterAPIInstallerComponentSource{
							Type: operatorv1.InstallerComponentTypeImage,
							Image: operatorv1.ClusterAPIInstallerComponentImage{
								Ref: "registry/core@sha256:abc123",
							},
						},
					},
				},
			},
			{
				Components: []operatorv1.ClusterAPIInstallerComponent{
					{
						Name: "core",
						Source: operatorv1.ClusterAPIInstallerComponentSource{
							Type: operatorv1.InstallerComponentTypeImage,
							Image: operatorv1.ClusterAPIInstallerComponentImage{
								Ref: "registry/core@sha256:abc123",
							},
						},
					},
				},
			},
		}

		result := ImageRefsFromRevisions(revisions)
		Expect(result).To(Equal(sets.New[string]("registry/core@sha256:abc123")))
	})

	It("should return an empty set for empty revisions slice", func() {
		result := ImageRefsFromRevisions([]operatorv1.ClusterAPIInstallerRevision{})
		Expect(result).To(BeEmpty())
	})

	It("should return an empty set for nil revisions slice", func() {
		result := ImageRefsFromRevisions(nil)
		Expect(result).To(BeEmpty())
	})
})
