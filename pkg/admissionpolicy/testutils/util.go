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
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// This set of utils is intended to assist with VAP debugging.

// We provide common audit policies that may be useful across CCAPIO.
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

const (
	// DefaultProfile is the profile name for core/shared admission policies.
	DefaultProfile = "default"

	// AWSProfile is the profile name for AWS-specific admission policies.
	AWSProfile = "aws"

	yamlChunk = 4096
)

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

// SentinelValidationExpression is a CEL expression that blocks resources with the "test-sentinel" label.
// Use this in tests to verify a VAP is actively enforcing.
const SentinelValidationExpression = "!(has(object.metadata.labels) && \"test-sentinel\" in object.metadata.labels)"

// AddSentinelValidation appends a sentinel validation rule to a VAP.
func AddSentinelValidation(vap *admissionregistrationv1.ValidatingAdmissionPolicy) {
	vap.Spec.Validations = append(vap.Spec.Validations, admissionregistrationv1.Validation{
		Expression: SentinelValidationExpression,
		Message:    "policy in place",
	})
}

// VerifySentinelValidation tries to update a resource to hit the sentinel validation to see that the VAP is in-place.
func VerifySentinelValidation(k komega.Komega, sentinelObject client.Object, timeout time.Duration) {
	Eventually(k.Update(sentinelObject, func() {
		SetSentinelValidationLabel(sentinelObject)
	}), timeout).Should(MatchError(ContainSubstring("policy in place")))
}

// SetSentinelValidationLabel sets the sentinel validation label on a resource.
func SetSentinelValidationLabel(sentinelObject client.Object) {
	currentLabels := sentinelObject.GetLabels()
	if currentLabels == nil {
		currentLabels = map[string]string{}
	}

	currentLabels["test-sentinel"] = "fubar"
	sentinelObject.SetLabels(currentLabels)
}

// UpdateVAPBindingNamespaces updates a VAP binding's namespace configuration.
//
// Parameters:
//   - binding: The ValidatingAdmissionPolicyBinding to update
//   - paramNamespace: Namespace containing parameter resources, or "" if no paramRef
//   - targetNamespace: Namespace where policy is enforced
func UpdateVAPBindingNamespaces(binding *admissionregistrationv1.ValidatingAdmissionPolicyBinding, paramNamespace, targetNamespace string) {
	// Validate paramNamespace matches binding structure
	hasParamRef := binding.Spec.ParamRef != nil
	ExpectWithOffset(1, hasParamRef && paramNamespace == "").ToNot(BeTrue(),
		"paramNamespace cannot be empty for binding %q with paramRef", binding.Name)
	ExpectWithOffset(1, !hasParamRef && paramNamespace != "").ToNot(BeTrue(),
		"paramNamespace %q provided but binding %q has no paramRef", paramNamespace, binding.Name)

	// Update paramRef namespace if parameterized
	if hasParamRef {
		binding.Spec.ParamRef.Namespace = paramNamespace
	}

	// Validate MatchResources structure
	ExpectWithOffset(1, binding.Spec.MatchResources).ToNot(BeNil(),
		"binding %q has nil MatchResources", binding.Name)
	ExpectWithOffset(1, binding.Spec.MatchResources.NamespaceSelector).ToNot(BeNil(),
		"binding %q has nil NamespaceSelector", binding.Name)

	// Always update target namespace
	binding.Spec.MatchResources.NamespaceSelector.MatchLabels = map[string]string{
		"kubernetes.io/metadata.name": targetNamespace,
	}
}

// LoadAdmissionPolicyProfiles loads admission policies from the generated
// manifests.yaml files under capi-operator-manifests/, returning a map of
// []client.Object keyed by profile name.
//
// This is intended to allow for loading the admission policies into envtest,
// therefore it doesn't return errors, but Expects() them not to happen.
func LoadAdmissionPolicyProfiles() map[string][]client.Object {
	By("Loading admission policy profiles")

	profiles := []string{DefaultProfile, AWSProfile}
	result := map[string][]client.Object{}

	for _, profile := range profiles {
		manifestPath := filepath.Join("..", "..", "..", "capi-operator-manifests", profile, "manifests.yaml")

		data, err := os.Open(manifestPath) //nolint:gosec // path is constructed from known profile constants
		Expect(err).WithOffset(1).ToNot(HaveOccurred(), "failed to open manifests.yaml for profile %q", profile)
		DeferCleanup(func() {
			Expect(data.Close()).To(Succeed())
		})

		decoder := yaml.NewYAMLOrJSONDecoder(data, yamlChunk)

		var objs []client.Object

		for {
			var r runtime.RawExtension
			if err := decoder.Decode(&r); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}

				Expect(err).WithOffset(1).NotTo(HaveOccurred())
			}

			if len(r.Raw) == 0 {
				continue
			}

			o, _, err := scheme.Codecs.UniversalDeserializer().Decode(r.Raw, nil, nil)
			Expect(err).WithOffset(1).NotTo(HaveOccurred())

			if co, ok := o.(client.Object); ok {
				objs = append(objs, co)
			}
		}

		result[profile] = objs
	}

	return result
}

// WarningCollector is to provide a way to collect
// kube client warnings, intended for testing VAPs that return warnings.
type WarningCollector struct {
	sync.Mutex
	messages []string
}

// HandleWarningHeaderWithContext implements rest.WarningHandlerWithContext.
// For test simplicity, only the message is captured; code and agent are ignored.
func (w *WarningCollector) HandleWarningHeaderWithContext(_ context.Context, code int, _ string, message string) {
	w.Lock()
	w.messages = append(w.messages, message)
	w.Unlock()
}

// Messages returns messages collected by a warning collector.
func (w *WarningCollector) Messages() []string {
	w.Lock()
	defer w.Unlock()

	// return a copy for thread-safety
	out := make([]string, len(w.messages))
	copy(out, w.messages)

	return out
}

// Reset clears the messages, used between tests to reset state.
func (w *WarningCollector) Reset() {
	w.Lock()
	w.messages = nil
	w.Unlock()
}

// SetupClientWithWarningCollector creates a new client.Client, with a warning handler that writes to a returned WarningCollector.
func SetupClientWithWarningCollector(cfg *rest.Config, scheme *runtime.Scheme) (client.Client, *WarningCollector, error) {
	warnSink := &WarningCollector{}
	// copy to avoid mutating the passed-in config
	newcfg := rest.CopyConfig(cfg)

	newcfg.WarningHandlerWithContext = warnSink

	// Build the client with this config
	client, err := client.New(newcfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, nil, fmt.Errorf("error creating new client: %w", err)
	}

	return client, warnSink, nil
}
