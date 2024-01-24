/*
Copyright 2024.

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

package unsupported

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var _ = Describe("CAPI unsupported controller", func() {
	ctx := context.Background()
	var r *UnsupportedController
	var capiClusterOperator *configv1.ClusterOperator
	capiClusterOperatorKey := client.ObjectKey{Name: "cluster-api"}

	BeforeEach(func() {
		r = &UnsupportedController{
			ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{
				Client: cl,
			},
		}

		capiClusterOperator = &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cluster-api",
			},
		}

		Expect(cl.Create(ctx, capiClusterOperator)).To(Succeed(), "should be able to create the 'cluster-api' ClusterOperator object")
	})

	AfterEach(func() {
		Expect(test.CleanupAndWait(ctx, cl, &configv1.ClusterOperator{})).To(Succeed())
	})

	It("should update cluster-api ClusterOperator status with an 'unsupported' message", func() {
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: capiClusterOperator.Name,
			},
		})

		Expect(err).ToNot(HaveOccurred(), "should be able to reconcile the cluster-api ClusterOperator without erroring")

		Eventually(func() (*configv1.ClusterOperator, error) {
			err := cl.Get(ctx, capiClusterOperatorKey, capiClusterOperator)
			return capiClusterOperator, err
		}).Should(HaveField("Status.Conditions",
			SatisfyAll(
				ContainElement(And(HaveField("Type", Equal(configv1.OperatorAvailable)), HaveField("Status", Equal(configv1.ConditionTrue)), HaveField("Message", Equal(capiUnsupportedPlatformMsg)))),
				ContainElement(And(HaveField("Type", Equal(configv1.OperatorProgressing)), HaveField("Status", Equal(configv1.ConditionFalse)))),
				ContainElement(And(HaveField("Type", Equal(configv1.OperatorDegraded)), HaveField("Status", Equal(configv1.ConditionFalse)))),
			),
		), "should match the expected ClusterOperator status conditions")
	})

})
