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
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	framework "github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("[OTP][Jira:OCPCLOUD][OCPFeatureGate:ClusterAPIMachineManagement][sig-cluster-lifecycle] Cluster API Webhook Validation", func() {
	BeforeEach(func() {
		if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIMachineManagement) {
			Skip("ClusterAPIMachineManagement feature gate is not enabled")
		}
	})

	It("should deny deletion of infrastructure cluster resources", Label("Disruptive"), Label("Lifecycle:informing"), func() {
		infraTypes, _, err := util.GetCAPITypesForInfrastructure(infra)
		if errors.Is(err, util.ErrUnsupportedPlatform) {
			Skip(fmt.Sprintf("Infra cluster deletion test not supported on %s", platform))
		}
		Expect(err).ToNot(HaveOccurred())

		infraCluster := infraTypes.Cluster()
		Expect(cl.Get(ctx, client.ObjectKey{Namespace: framework.CAPINamespace, Name: clusterName}, infraCluster)).To(Succeed())

		By(fmt.Sprintf("Attempting to delete %T/%s (dry-run)", infraCluster, clusterName))
		err = cl.Delete(ctx, infraCluster, client.DryRunAll)
		Expect(err).To(HaveOccurred(), "deletion should be denied")
		Expect(err.Error()).To(ContainSubstring("denied"), "error should mention denial")
	})

	It("should enforce webhook validations for Cluster API cluster resources", Label("Disruptive"), Label("Lifecycle:informing"), func() {
		switch platform {
		case configv1.AWSPlatformType, configv1.GCPPlatformType:
		default:
			Skip("Cluster API machine webhook tests only supported on AWS and GCP")
		}

		By("Getting the Cluster API cluster object")
		cluster := &clusterv1.Cluster{}
		Expect(cl.Get(ctx, client.ObjectKey{Namespace: framework.CAPINamespace, Name: clusterName}, cluster)).To(Succeed())

		By("Attempting to patch cluster with invalid infrastructureRef kind")
		patch := client.MergeFrom(cluster.DeepCopy())
		cluster.Spec.InfrastructureRef.Kind = "invalid"
		err := cl.Patch(ctx, cluster, patch)
		Expect(err).To(HaveOccurred(), "patching with invalid kind should be rejected")
		Expect(err.Error()).To(ContainSubstring("invalid"), "error should mention the invalid kind")

		By("Attempting to delete the cluster (dry-run)")
		freshCluster := &clusterv1.Cluster{}
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(cluster), freshCluster)).To(Succeed())
		err = cl.Delete(ctx, freshCluster, client.DryRunAll)
		Expect(err).To(HaveOccurred(), "cluster deletion should be denied")
		Expect(err.Error()).Should(MatchRegexp(`(?i)(denied|not allowed)`), "error should indicate deletion was denied")
	})
})
