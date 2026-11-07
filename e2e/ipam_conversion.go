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
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
)

const (
	ipamConversionGroup       = "ipam.cluster.x-k8s.io"
	ipamConversionWebhookName = "capi-ipam-conversion-webhook"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:ClusterAPIOperator] Cluster API IPAM conversion",
	Label("skip-topology:External"), func() {
		BeforeEach(func() {
			if IsMicroShift {
				Skip("Cluster API is not supported on MicroShift")
			}
			if infra.Status.ControlPlaneTopology == configv1.ExternalTopologyMode {
				Skip("The IPAM conversion webhook is not deployed on External topology clusters")
			}
			if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIOperator) {
				Skip("ClusterAPIOperator is not enabled")
			}
			if framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIMachineManagement) {
				Skip("ClusterAPIMachineManagement uses the full Cluster API conversion webhook")
			}
		})

		It("should configure the standalone webhook for both IPAM CRDs", func() {
			framework.AssertDeploymentAvailable(ipamConversionWebhookName, framework.CAPINamespace)

			for _, plural := range []string{"ipaddresses", "ipaddressclaims"} {
				crdName := plural + "." + ipamConversionGroup
				By("Checking " + crdName)
				Eventually(func(g Gomega) {
					crd := &apiextensionsv1.CustomResourceDefinition{}
					g.Expect(cl.Get(ctx, client.ObjectKey{Name: crdName}, crd)).To(Succeed())
					g.Expect(crd.Spec.Versions).To(HaveLen(3))

					var served, stored []string
					for _, version := range crd.Spec.Versions {
						if version.Served {
							served = append(served, version.Name)
						}
						if version.Storage {
							stored = append(stored, version.Name)
						}
					}
					g.Expect(served).To(ConsistOf("v1alpha1", "v1beta1", "v1beta2"))
					g.Expect(stored).To(ConsistOf("v1beta1"))
					g.Expect(crd.Spec.Conversion).NotTo(BeNil())
					g.Expect(crd.Spec.Conversion.Strategy).To(Equal(apiextensionsv1.WebhookConverter))
					g.Expect(crd.Spec.Conversion.Webhook).NotTo(BeNil())
					g.Expect(crd.Spec.Conversion.Webhook.ClientConfig.CABundle).NotTo(BeEmpty())
					service := crd.Spec.Conversion.Webhook.ClientConfig.Service
					g.Expect(service).NotTo(BeNil())
					g.Expect(service.Name).To(Equal(ipamConversionWebhookName))
					g.Expect(service.Namespace).To(Equal(framework.CAPINamespace))
					g.Expect(service.Path).To(HaveValue(Equal("/convert")))
					g.Expect(service.Port).To(HaveValue(Equal(int32(443))))
				}).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(Succeed())
			}
		})

		DescribeTable("should preserve IPAM resources across API versions", func(kind string) {
			framework.AssertDeploymentAvailable(ipamConversionWebhookName, framework.CAPINamespace)

			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "ipam-conversion-e2e-"}}
			framework.CreateAndCleanup(ctx, cl, namespace)

			original := newIPAMConversionObject(kind, namespace.Name)
			framework.CreateAndCleanup(ctx, cl, original)
			if kind == "IPAddressClaim" {
				By("Setting the claim status through the status subresource")
				Expect(unstructured.SetNestedMap(original.Object, map[string]interface{}{
					"addressRef": map[string]interface{}{"name": "address"},
					"conditions": []interface{}{map[string]interface{}{
						"type": "Ready", "status": "True", "lastTransitionTime": "2026-01-01T00:00:00Z",
						"reason": "Allocated", "message": "Address allocated",
					}},
				}, "status")).To(Succeed())
				Expect(cl.Status().Update(ctx, original)).To(Succeed())
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(original), original)).To(Succeed())
			}

			By("Reading the resource through v1beta2")
			converted := getIPAMConversionObject(kind, "v1beta2", original)
			assertIPAMConversionFields(converted, original)
			if kind == "IPAddressClaim" {
				addressName, found, err := unstructured.NestedString(converted.Object, "status", "addressRef", "name")
				Expect(err).NotTo(HaveOccurred())
				Expect(found).To(BeTrue())
				Expect(addressName).To(Equal("address"))
				conditions, found, err := unstructured.NestedSlice(converted.Object, "status", "deprecated", "v1beta1", "conditions")
				Expect(err).NotTo(HaveOccurred())
				Expect(found).To(BeTrue())
				Expect(conditions).To(ContainElement(SatisfyAll(
					HaveKeyWithValue("type", "Ready"),
					HaveKeyWithValue("status", "True"),
				)))
			}

			By("Updating through v1beta2 and reading both older versions")
			converted.SetLabels(map[string]string{"ipam-conversion-e2e": "updated"})
			Expect(cl.Update(ctx, converted)).To(Succeed())
			for _, version := range []string{"v1beta1", "v1alpha1"} {
				readBack := getIPAMConversionObject(kind, version, original)
				assertIPAMConversionFields(readBack, original)
				Expect(readBack.GetLabels()).To(HaveKeyWithValue("ipam-conversion-e2e", "updated"))
				if kind == "IPAddressClaim" && version == "v1alpha1" {
					Expect(readBack.Object["status"]).To(Equal(original.Object["status"]))
				}
			}
		},
			Entry("IPAddress", "IPAddress"),
			Entry("IPAddressClaim", "IPAddressClaim"),
		)
	})

func newIPAMConversionObject(kind, namespace string) *unstructured.Unstructured {
	spec := map[string]interface{}{
		"poolRef": map[string]interface{}{"apiGroup": "ipam.example.com", "kind": "IPPool", "name": "pool"},
	}
	if kind == "IPAddress" {
		spec["address"] = "192.0.2.10"
		spec["prefix"] = int64(24)
		spec["gateway"] = "192.0.2.1"
		spec["claimRef"] = map[string]interface{}{"name": "claim"}
	}
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": ipamConversionGroup + "/v1alpha1",
		"kind":       kind,
		"spec":       spec,
	}}
	obj.SetName("conversion-test")
	obj.SetNamespace(namespace)
	return obj
}

func getIPAMConversionObject(kind, version string, original *unstructured.Unstructured) *unstructured.Unstructured {
	GinkgoHelper()
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(ipamConversionGroup + "/" + version)
	obj.SetKind(kind)
	Expect(cl.Get(ctx, client.ObjectKeyFromObject(original), obj)).To(Succeed())
	return obj
}

func assertIPAMConversionFields(converted, original *unstructured.Unstructured) {
	GinkgoHelper()
	Expect(converted.GetUID()).To(Equal(original.GetUID()))
	Expect(converted.GetName()).To(Equal(original.GetName()))
	Expect(converted.GetNamespace()).To(Equal(original.GetNamespace()))
	Expect(converted.Object["spec"]).To(Equal(original.Object["spec"]))
}
