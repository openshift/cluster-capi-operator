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
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

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
	// ClusterAPIAdmissionPolicies is the name of the ClusterAPIAdmissionPolicies transport config map.
	ClusterAPIAdmissionPolicies string = "cluster-api-admission-policies"

	// ClusterAPICustomAdmissionPolicies is the name of the ClusterAPICustomAdmissionPolicies transport config map.
	ClusterAPICustomAdmissionPolicies string = "cluster-api-custom-admission-policies"

	// ClusterAPIAWSAdmissionPolicies is the name of the ClusterAPIAWSAdmissionPolicies transport config map.
	ClusterAPIAWSAdmissionPolicies string = "cluster-api-aws-admission-policies"

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

// LoadTransportConfigMaps loads admission policies from the transport config maps in
// `manifests`, providing a map of []client.Object, one per transport config map.
//
// This is intended to allow for loading the admission policies into envtest,
// therefore it doesn't return errors, but Expects() them not to happen.
//
//nolint:gocognit,funlen
func LoadTransportConfigMaps() map[string][]client.Object {
	By("Unmarshalling the admission policy transport configmaps")

	configMaps, err := os.Open("../../../manifests/0000_30_cluster-api_09_admission-policies.yaml")
	Expect(err).WithOffset(1).ToNot(HaveOccurred())
	DeferCleanup(func() {
		Expect(configMaps.Close()).WithOffset(1).To(Succeed())
	})

	decoder := yaml.NewYAMLOrJSONDecoder(configMaps, yamlChunk)

	// When we add more provider specific admission policies, we'll need to update this list.
	// e.g for clusterAPI<Provider>AdmissionPolicies

	// ClusterAPIAdmissionPolicies and ClusterAPIAWSAdmissionPolicies exist commented out in
	// the admission policies manifest.
	configMapByName := map[string]*corev1.ConfigMap{
		// ClusterAPIAdmissionPolicies:       nil,
		ClusterAPICustomAdmissionPolicies: nil,
		ClusterAPIAWSAdmissionPolicies:    nil,
	}

	for {
		var cm corev1.ConfigMap
		if err := decoder.Decode(&cm); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			Expect(err).WithOffset(1).NotTo(HaveOccurred())
		}

		if _, want := configMapByName[cm.Name]; want {
			configMapByName[cm.Name] = cm.DeepCopy()
		}
	}

	// Assert we found everything we care about.
	for name, cm := range configMapByName {
		Expect(cm).WithOffset(1).NotTo(BeNil(), "expected ConfigMap %q in manifest", name)
	}

	By("Unmarshalling the admission policies in each configmap")

	// each ConfigMap produces a list of client.Objects obtained from that configMap
	mapObjs := map[string][]client.Object{}

	for _, configMap := range configMapByName {
		objs := []client.Object{}

		if components, ok := configMap.Data["components"]; ok && len(components) > 0 { //nolint: nestif
			policyDecoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(components), yamlChunk)

			for {
				var r runtime.RawExtension
				if err := policyDecoder.Decode(&r); err != nil {
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

				// only keep objects that implement client.Object
				if co, ok := o.(client.Object); ok {
					objs = append(objs, co)
				}
			}
		}

		// sets the client.Objects we've just extracted
		mapObjs[configMap.GetName()] = objs
	}

	return mapObjs
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
