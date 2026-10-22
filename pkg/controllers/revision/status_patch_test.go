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

package revision

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	operatorv1 "github.com/openshift/api/operator/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestRevisionsStatusPatch(t *testing.T) {
	g := NewWithT(t)

	var patchData []byte

	c := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		SubResourcePatch: func(_ context.Context, _ client.Client, subResource string, obj client.Object, patch client.Patch, _ ...client.SubResourcePatchOption) error {
			g.Expect(subResource).To(Equal("status"))

			var err error

			patchData, err = patch.Data(obj)

			return err
		},
	}).Build()
	clusterAPI := &operatorv1.ClusterAPI{ObjectMeta: metav1.ObjectMeta{Name: "cluster", UID: "test-uid", Generation: 3}}
	r := &RevisionController{Client: c}
	err := r.writeRevisions(context.Background(), logr.Discard(), clusterAPI, []operatorv1.ClusterAPIInstallerRevision{{
		Name: "revision-1", Revision: 1,
		Components: []operatorv1.ClusterAPIInstallerComponent{{
			Name: "core",
			Source: operatorv1.ClusterAPIInstallerComponentSource{
				Type: operatorv1.InstallerComponentTypeImage,
				Image: operatorv1.ClusterAPIInstallerComponentImage{
					Ref:     "quay.io/openshift/core@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					Profile: "default",
				},
			},
		}},
	}})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(patchData).To(MatchJSON(`{
        "apiVersion": "operator.openshift.io/v1",
        "kind": "ClusterAPI",
        "metadata": {"name": "cluster"},
        "status": {
            "observedRevisionGeneration": 3,
            "desiredRevision": "revision-1",
            "revisions": [{
                "name": "revision-1",
                "revision": 1,
                "components": [{
                    "name": "core",
                    "source": {
                        "type": "Image",
                        "image": {
                            "ref": "quay.io/openshift/core@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                            "profile": "default"
                        }
                    }
                }]
            }]
        }
    }`))
}
