// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	"k8s.io/apimachinery/pkg/types"
	gcpv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
)

// verifyCAPIInstanceOwnershipLabel asserts that the GCPCluster has the
// kubernetes-io-cluster-<infraName>=owned label that openshift-install destroy cluster
// relies on to identify and clean up cloud resources.
func verifyCAPIInstanceOwnershipLabel(infraName string) {
	GinkgoHelper()
	By("Verifying GCPCluster has cluster ownership label")

	Expect(infraName).ToNot(BeEmpty())

	gcpCluster := &gcpv1.GCPCluster{}
	key := types.NamespacedName{Name: infraName, Namespace: framework.CAPINamespace}
	Expect(cl.Get(ctx, key, gcpCluster)).To(Succeed(), "should be able to get GCPCluster %s", infraName)

	expectedLabelKey := fmt.Sprintf("kubernetes-io-cluster-%s", infraName)
	Expect(gcpCluster.Spec.AdditionalLabels).ToNot(BeNil(),
		"expected GCPCluster to have AdditionalLabels set")
	Expect(gcpCluster.Spec.AdditionalLabels).To(HaveKeyWithValue(expectedLabelKey, "owned"),
		"expected GCPCluster to have label %s=owned for cluster destroy to find CAPI-created resources", expectedLabelKey)
}
