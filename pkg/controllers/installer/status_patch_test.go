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
	"context"
	"testing"

	. "github.com/onsi/gomega"
	operatorv1 "github.com/openshift/api/operator/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestCurrentRevisionStatusPatch(t *testing.T) {
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
	r := &InstallerController{client: c}
	g.Expect(r.writeCurrentRevision(context.Background(), clusterAPI, "revision-1")).To(Succeed())
	g.Expect(patchData).To(MatchJSON(`{
        "apiVersion": "operator.openshift.io/v1",
        "kind": "ClusterAPI",
        "metadata": {"name": "cluster", "uid": "test-uid"},
        "status": {"currentRevision": "revision-1"}
    }`))
}
