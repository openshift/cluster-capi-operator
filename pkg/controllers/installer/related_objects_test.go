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
	"os"
	"testing"

	"github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	"sigs.k8s.io/yaml"
)

func TestStaticRelatedObjectsMatchManifest(t *testing.T) {
	// This test ensures that staticRelatedObjects() and the ClusterOperator
	// manifest do not drift.
	data, err := os.ReadFile("../../../manifests/0000_30_cluster-api_12_clusteroperator.yaml")
	if err != nil {
		t.Fatalf("reading ClusterOperator manifest: %v", err)
	}

	var co configv1.ClusterOperator
	if err := yaml.Unmarshal(data, &co); err != nil {
		t.Fatalf("unmarshaling ClusterOperator manifest: %v", err)
	}

	g := gomega.NewWithT(t)
	g.Expect(staticRelatedObjects()).To(gomega.Equal(co.Status.RelatedObjects),
		"staticRelatedObjects() and the ClusterOperator manifest do not match")
}

func TestMergeRelatedObjects(t *testing.T) {
	static := []configv1.ObjectReference{
		{Group: "", Resource: "namespaces", Name: "ns-a"},
		{Group: "", Resource: "namespaces", Name: "ns-b"},
	}

	t.Run("no dynamic entries", func(t *testing.T) {
		result := mergeRelatedObjects(static, nil)
		if len(result) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(result))
		}
	})

	t.Run("dynamic entries are appended after static", func(t *testing.T) {
		dynamic := []configv1.ObjectReference{
			{Group: "rbac.authorization.k8s.io", Resource: "clusterroles", Name: "cr-1"},
		}

		result := mergeRelatedObjects(static, dynamic)
		g := gomega.NewWithT(t)
		g.Expect(result).To(gomega.Equal([]configv1.ObjectReference{
			{Group: "", Resource: "namespaces", Name: "ns-a"},
			{Group: "", Resource: "namespaces", Name: "ns-b"},
			{Group: "rbac.authorization.k8s.io", Resource: "clusterroles", Name: "cr-1"},
		}))
	})

	t.Run("duplicates with static are removed", func(t *testing.T) {
		dynamic := []configv1.ObjectReference{
			{Group: "", Resource: "namespaces", Name: "ns-a"},
			{Group: "rbac.authorization.k8s.io", Resource: "clusterroles", Name: "cr-1"},
		}

		result := mergeRelatedObjects(static, dynamic)
		g := gomega.NewWithT(t)
		g.Expect(result).To(gomega.Equal([]configv1.ObjectReference{
			{Group: "", Resource: "namespaces", Name: "ns-a"},
			{Group: "", Resource: "namespaces", Name: "ns-b"},
			{Group: "rbac.authorization.k8s.io", Resource: "clusterroles", Name: "cr-1"},
		}))
	})

	t.Run("dynamic entries are sorted", func(t *testing.T) {
		dynamic := []configv1.ObjectReference{
			{Group: "z.io", Resource: "widgets", Name: "w-1"},
			{Group: "a.io", Resource: "gadgets", Name: "g-1"},
		}

		result := mergeRelatedObjects(static, dynamic)
		g := gomega.NewWithT(t)
		g.Expect(result).To(gomega.Equal([]configv1.ObjectReference{
			{Group: "", Resource: "namespaces", Name: "ns-a"},
			{Group: "", Resource: "namespaces", Name: "ns-b"},
			{Group: "a.io", Resource: "gadgets", Name: "g-1"},
			{Group: "z.io", Resource: "widgets", Name: "w-1"},
		}))
	})
}
