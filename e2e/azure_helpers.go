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
	azurev1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
)

// verifyCAPIAzureClusterOwnershipTag asserts that the AzureCluster has the
// kubernetes.io_cluster.<infraName>=owned tag that openshift-install destroy cluster
// relies on to identify and clean up cloud resources.
func verifyCAPIAzureClusterOwnershipTag(infraName string) {
	GinkgoHelper()
	By("Verifying AzureCluster has cluster ownership tag")

	Expect(infraName).ToNot(BeEmpty())

	azureCluster := &azurev1.AzureCluster{}
	key := types.NamespacedName{Name: infraName, Namespace: framework.CAPINamespace}
	Expect(cl.Get(ctx, key, azureCluster)).To(Succeed(), "should be able to get AzureCluster %s", infraName)

	expectedTagKey := fmt.Sprintf("kubernetes.io_cluster.%s", infraName)
	Expect(azureCluster.Spec.AzureClusterClassSpec.AdditionalTags).ToNot(BeNil(),
		"expected AzureCluster to have AdditionalTags set")
	Expect(azureCluster.Spec.AzureClusterClassSpec.AdditionalTags).To(HaveKeyWithValue(expectedTagKey, "owned"),
		"expected AzureCluster to have tag %s=owned for cluster destroy to find CAPI-created resources", expectedTagKey)
}
