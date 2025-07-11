package test

import (
	"context"
	"fmt"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// createVAPs creates ValidatingAdmissionPolicies and bindings from the manifest file
func CreateVAPs(cl client.Client, ctx context.Context, namespace *corev1.Namespace, vapPath string) error {
	configMap := &corev1.ConfigMap{}

	vapsFile, err := os.Open(vapPath)
	Expect(err).ToNot(HaveOccurred())

	decoder := yaml.NewYAMLOrJSONDecoder(vapsFile, 4096)
	// First decode the ConfigMap
	Expect(decoder.Decode(configMap)).To(Succeed())
	Expect(vapsFile.Close()).To(Succeed())

	// Extract the components data from the ConfigMap
	componentsRaw, ok := configMap.Data["components"]
	Expect(ok).To(BeTrue(), "ConfigMap should contain a components key")

	// Parse all YAML documents from the components data
	var policies []*admissionregistrationv1.ValidatingAdmissionPolicy
	var policyBindings []*admissionregistrationv1.ValidatingAdmissionPolicyBinding

	// Split the components data into individual YAML documents
	documents := strings.Split(componentsRaw, "---")

	for _, document := range documents {
		document = strings.TrimSpace(document)
		if document == "" {
			continue
		}

		var rawObj map[string]interface{}
		decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(document), 4096)
		err := decoder.Decode(&rawObj)
		if err != nil {
			return fmt.Errorf("failed to decode YAML document: %w", err)
		}

		kind, ok := rawObj["kind"].(string)
		if !ok {
			return fmt.Errorf("failed to get kind from YAML document: %w", err)
		}

		switch kind {
		case "ValidatingAdmissionPolicy":
			policy := &admissionregistrationv1.ValidatingAdmissionPolicy{}
			policyDecoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(document), 4096)
			Expect(policyDecoder.Decode(policy)).To(Succeed())
			policies = append(policies, policy)
		case "ValidatingAdmissionPolicyBinding":
			policyBinding := &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
			bindingDecoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(document), 4096)
			Expect(bindingDecoder.Decode(policyBinding)).To(Succeed())
			policyBindings = append(policyBindings, policyBinding)
		}
	}

	// For policy bindings that target namespace, update the namespace selector to match the test namespace
	if namespace != nil {
		for _, policyBinding := range policyBindings {
			if policyBinding.Spec.MatchResources.NamespaceSelector != nil {
				for i, expr := range policyBinding.Spec.MatchResources.NamespaceSelector.MatchExpressions {
					if expr.Key == "kubernetes.io/metadata.name" {
						policyBinding.Spec.MatchResources.NamespaceSelector.MatchExpressions[i].Values = []string{namespace.Name}
					}
				}
			}
		}
	}

	By("creating ValidatingAdmissionPolicies")
	for _, policy := range policies {
		Expect(cl.Create(ctx, policy)).To(Succeed())
	}

	By("creating ValidatingAdmissionPolicyBindings")
	for _, policyBinding := range policyBindings {
		Expect(cl.Create(ctx, policyBinding)).To(Succeed())
	}

	return nil
}
