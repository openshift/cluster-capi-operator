/*
Copyright 2024 Red Hat, Inc.

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
package util

import (
	"strings"

	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// IsCAPIManagedLabel determines of a label is managed by CAPI or not.
// This means, a label that when present on the Cluster API Machine, will be propagated down to the corresponding Node.
func IsCAPIManagedLabel(key string) bool {
	dnsSubdomainOrName := strings.Split(key, "/")[0]

	return dnsSubdomainOrName == clusterv1beta1.NodeRoleLabelPrefix ||
		dnsSubdomainOrName == clusterv1beta1.NodeRestrictionLabelDomain || strings.HasSuffix(dnsSubdomainOrName, "."+clusterv1beta1.NodeRestrictionLabelDomain) ||
		dnsSubdomainOrName == clusterv1beta1.ManagedNodeLabelDomain || strings.HasSuffix(dnsSubdomainOrName, "."+clusterv1beta1.ManagedNodeLabelDomain)
}
