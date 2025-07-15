/*
Copyright 2025 Red Hat, Inc.

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

package testutils

import (
	"os"
	"path/filepath"

	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// This set of utils is intended to assist with VAP debugging.
// we provide common audit policies that may be useful across CCAPIO.
// Example usage below:
//
//  By("bootstrapping test environment")
//	var err error
//	testEnv = &envtest.Environment{}
///	testutils.EnvTestWithAuditPolicy(testutils.MachineAPIMachineUpdateAuditPolicy, testEnv)
//	cfg, k8sClient, err = test.StartEnvTest(testEnv)
//
// Once envtest is configured, audit logs are written out to /tmp/kube-apiserver-audit.log, where
// they may be useful in debugging VAPs. e.g
//
// $ cat /tmp/kube-apiserver-audit.log | jq '.user'
// {
//   "username": "system:serviceaccount:openshift-cluster-api:cluster-capi-operator",
//   "groups": [
//     "system:masters",
//     "system:authenticated"
//   ],
//   "extra": {
//     "authentication.kubernetes.io/credential-id": [
//       "X509SHA256=fubar"
//     ]
//   }
// }
//
// $ cat /tmp/kube-apiserver-audit.log | jq 'keys'
// [
//   "annotations",
//   "apiVersion",
//   "auditID",
//   "kind",
//   "level",
//   "objectRef",
//   "requestObject",
//   "requestReceivedTimestamp",
//   "requestURI",
//   "responseObject",
//   "responseStatus",
//   "sourceIPs",
//   "stage",
//   "stageTimestamp",
//   "user",
//   "userAgent",
//   "verb"
// ]

// MachineAPIMachineUpdateAuditPolicy is an audit policy that captures
// Request and Response for any UPDATE to a Machine API Machine.
const MachineAPIMachineUpdateAuditPolicy string = `
apiVersion: audit.k8s.io/v1
kind: Policy
# Drop the very first “RequestReceived” stage across the board
omitStages:
  - RequestReceived

rules:
  # 1) Full request+response for machine UPDATEs
  - level: RequestResponse
    verbs: ["update"]
    resources:
      - group: "machine.openshift.io"
        resources: ["machines"]

  # 2) Drop all other events (empty resources list => all groups & resources)
  - level: None
    resources: []
`

// ClusterAPIMachineUpdateAuditPolicy is an audit policy that captures
// Request and Response for any UPDATE to a Cluster API Machine.
const ClusterAPIMachineUpdateAuditPolicy string = `
apiVersion: audit.k8s.io/v1
kind: Policy
# Drop the very first “RequestReceived” stage across the board
omitStages:
  - RequestReceived

rules:
  # 1) Full request+response for machine UPDATEs
  - level: RequestResponse
    verbs: ["update"]
    resources:
      - group: "cluster.x-k8s.io"
        resources: ["machines"]

  # 2) Drop all other events (empty resources list => all groups & resources)
  - level: None
    resources: []
`

// writeAuditPolicy takes a policyYaml, and writes to a temp directory,
// this can then be passed to the api-server to get as args to enable audit logging.
func writeAuditPolicy(policyYaml string) string {
	tmp := os.TempDir()
	policyPath := filepath.Join(tmp, "audit-policy.yaml")

	Expect(os.WriteFile(policyPath, []byte(policyYaml), 0600)).To(Succeed())

	return policyPath
}

// EnvTestWithAuditPolicy updates an envtest.Environment in place with arguments to
// utilise a provided audit policy.
func EnvTestWithAuditPolicy(policyYaml string, env *envtest.Environment) {
	policyPath := writeAuditPolicy(policyYaml)

	if env.ControlPlane.APIServer == nil {
		env.ControlPlane.APIServer = &envtest.APIServer{}
	}

	args := env.ControlPlane.APIServer.Configure()
	args.Append("vmodule", "validatingadmissionpolicy*=6,cel*=6")
	args.Append("advertise-address", "127.0.0.1")
	args.Append("audit-policy-file", policyPath)
	args.Append("audit-log-path", "/tmp/kube-apiserver-audit.log")
	args.Append("audit-log-format", "json")
}
