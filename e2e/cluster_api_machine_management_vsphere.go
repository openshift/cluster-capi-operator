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
	. "github.com/onsi/ginkgo/v2"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
)

var _ = Describe("[OTP][Jira:OCPCLOUD][OCPFeatureGate:ClusterAPIMachineManagementVSphere][sig-cluster-lifecycle] Cluster API Secret Sync",
	Label("platform:vsphere"),
	func() {
		BeforeEach(func() {
			if platform != configv1.VSpherePlatformType {
				Skip("Skipping VSphere-specific tests on non-VSphere platform.")
			}
			if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIMachineManagementVSphere) {
				Skip("Feature gate ClusterAPIMachineManagementVSphere is not enabled.")
			}
		})

		It("should re-sync worker-user-data secret after deletion", Label("Disruptive"), Label("Lifecycle:informing"), secretSyncTest)
	})
