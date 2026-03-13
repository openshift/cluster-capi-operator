/*
Copyright 2026 Red Hat, Inc.

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
package objectvalidation

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidatingWebhookConfigurationFor(t *testing.T) {
	g := NewWithT(t)

	compatibilityRequirement := &apiextensionsv1alpha1.CompatibilityRequirement{
		ObjectMeta: metav1.ObjectMeta{Name: "test-compatibility-requirement"},
	}
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "test-crd"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "test.example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural: "test-crds",
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name: "v1",
					Subresources: &apiextensionsv1.CustomResourceSubresources{
						Status: &apiextensionsv1.CustomResourceSubresourceStatus{},
					},
				},
			},
		},
	}

	validatingWebhookConfig := ValidatingWebhookConfigurationFor(compatibilityRequirement, crd)

	g.Expect(validatingWebhookConfig).Should(SatisfyAll(
		HaveField("ObjectMeta.Name", BeEquivalentTo(compatibilityRequirement.Name)),
		HaveField("ObjectMeta.Annotations", HaveKey("service.beta.openshift.io/inject-cabundle")),
		HaveField("Webhooks", ConsistOf(SatisfyAll(
			HaveField("Name", BeEquivalentTo("compatibilityrequirement.operator.openshift.io")),
			HaveField("ClientConfig.Service.Name", BeEquivalentTo("compatibility-requirements-controllers-webhook-service")),
			HaveField("ClientConfig.Service.Namespace", BeEquivalentTo("openshift-compatibility-requirements-operator")),
			HaveField("ClientConfig.Service.Path", HaveValue(BeEquivalentTo(fmt.Sprintf("%s%s", webhookPrefix, compatibilityRequirement.Name)))),
			HaveField("SideEffects", HaveValue(BeEquivalentTo(admissionregistrationv1.SideEffectClassNone))),
			HaveField("FailurePolicy", HaveValue(BeEquivalentTo(admissionregistrationv1.Fail))),
			HaveField("MatchPolicy", HaveValue(BeEquivalentTo(admissionregistrationv1.Exact))),
			HaveField("Rules", ConsistOf(SatisfyAll(
				HaveField("APIGroups", BeEquivalentTo([]string{crd.Spec.Group})),
				HaveField("APIVersions", BeEquivalentTo([]string{crd.Spec.Versions[0].Name})),
				HaveField("Resources", BeEquivalentTo([]string{crd.Spec.Names.Plural, crd.Spec.Names.Plural + "/status"})),
				HaveField("Scope", HaveValue(BeEquivalentTo(admissionregistrationv1.ScopeType(crd.Spec.Scope)))),
				HaveField("Operations", ConsistOf(
					BeEquivalentTo("CREATE"),
					BeEquivalentTo("UPDATE"),
				)),
			))),
		))),
	))
}
