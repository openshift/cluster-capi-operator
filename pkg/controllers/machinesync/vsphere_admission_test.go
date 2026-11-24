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

package machinesync

import (
	"bytes"
	"errors"
	"io"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var _ = Describe("Unsupported vSphere fields admission", Ordered, func() {
	var (
		namespace *corev1.Namespace
		policies  []client.Object
	)

	newObject := func(kind string) *unstructured.Unstructured {
		spec := map[string]interface{}{"template": "test-template", "network": map[string]interface{}{"devices": []interface{}{}}}
		if kind == "VSphereMachineTemplate" {
			spec = map[string]interface{}{"template": map[string]interface{}{"spec": spec}}
		}

		return &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "infrastructure.cluster.x-k8s.io/v1beta1", "kind": kind,
			"metadata": map[string]interface{}{"generateName": "vsphere-admission-", "namespace": namespace.Name},
			"spec":     spec,
		}}
	}

	BeforeAll(func() {
		_, err := envtest.InstallCRDs(cfg, envtest.CRDInstallOptions{CRDs: []*apiextensionsv1.CustomResourceDefinition{
			test.GenerateSchemalessSpecStatusCRD(schema.GroupVersionKind{Group: "infrastructure.cluster.x-k8s.io", Version: "v1beta1", Kind: "VSphereMachine"}),
			test.GenerateSchemalessSpecStatusCRD(schema.GroupVersionKind{Group: "infrastructure.cluster.x-k8s.io", Version: "v1beta1", Kind: "VSphereMachineTemplate"}),
		}})
		Expect(err).NotTo(HaveOccurred())

		namespace = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "vsphere-admission-"}}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())

		data, err := os.ReadFile("../../../admission-policies/vsphere/unsupported-vsphere-spec-fields.yaml")
		Expect(err).NotTo(HaveOccurred())

		decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)

		for {
			var raw runtime.RawExtension

			err := decoder.Decode(&raw)
			if errors.Is(err, io.EOF) {
				break
			}

			Expect(err).NotTo(HaveOccurred())
			decoded, _, err := unstructured.UnstructuredJSONScheme.Decode(raw.Raw, nil, nil)
			Expect(err).NotTo(HaveOccurred())

			u := decoded.(*unstructured.Unstructured)

			var policy client.Object

			if u.GetKind() == "ValidatingAdmissionPolicyBinding" {
				binding := &admissionv1.ValidatingAdmissionPolicyBinding{}
				Expect(runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, binding)).To(Succeed())
				binding.Spec.MatchResources.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": namespace.Name}}
				policy = binding
			} else {
				vap := &admissionv1.ValidatingAdmissionPolicy{}
				Expect(runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, vap)).To(Succeed())
				policy = vap
			}

			Expect(k8sClient.Create(ctx, policy)).To(Succeed())
			policies = append(policies, policy)
		}

		sentinel := newObject("VSphereMachine")
		Expect(unstructured.SetNestedField(sentinel.Object, "unsupported", "spec", "thumbprint")).To(Succeed())
		Eventually(func() error { return k8sClient.Create(ctx, sentinel, client.DryRunAll) }, setupTimeout).
			Should(MatchError(ContainSubstring("thumbprint is not supported")))
	})

	AfterAll(func() {
		for _, policy := range policies {
			Expect(k8sClient.Delete(ctx, policy)).To(Succeed())
		}

		Expect(k8sClient.Delete(ctx, namespace)).To(Succeed())
	})

	DescribeTableSubtree("should validate Machines and MachineTemplates", func(kind string) {
		DescribeTable("should validate create and update", func(field string, value interface{}, denied bool) {
			obj := newObject(kind)
			Expect(k8sClient.Create(ctx, obj)).To(Succeed())
			stored := obj.DeepCopy()

			DeferCleanup(func() { Expect(k8sClient.Delete(ctx, stored)).To(Succeed()) })

			path := []string{"spec"}
			if kind == "VSphereMachineTemplate" {
				path = append(path, "template", "spec")
			}

			if field != "" {
				Expect(unstructured.SetNestedField(obj.Object, value, append(path, field)...)).To(Succeed())
			}

			err := k8sClient.Update(ctx, obj, client.DryRunAll)
			if denied {
				Expect(err).To(MatchError(ContainSubstring(field + " is not supported")))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}

			obj.SetResourceVersion("")
			obj.SetName("")

			err = k8sClient.Create(ctx, obj, client.DryRunAll)
			if denied {
				Expect(err).To(MatchError(ContainSubstring(field + " is not supported")))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		},
			Entry("omitted fields", "", nil, false),
			Entry("nestedHV true", "nestedHV", true, true),
			Entry("nestedHV false", "nestedHV", false, true),
			Entry("fault tolerance encryption", "ftEncryptionMode", "ftEncryptionRequired", true),
			Entry("migration encryption", "migrateEncryption", "required", true),
			Entry("crypto key", "cryptoKeyID", "key", true),
			Entry("crypto profile", "cryptoProfile", "profile", true),
			Entry("storage policy", "storagePolicyName", "gold", true),
			Entry("hardware version", "hardwareVersion", "vmx-19", true),
			Entry("PCI devices", "pciDevices", []interface{}{map[string]interface{}{"deviceId": int64(1), "vendorId": int64(1)}}, true),
			Entry("empty fault tolerance encryption", "ftEncryptionMode", "", false),
			Entry("empty migration encryption", "migrateEncryption", "", false),
			Entry("empty crypto key", "cryptoKeyID", "", false),
			Entry("empty crypto profile", "cryptoProfile", "", false),
			Entry("empty storage policy", "storagePolicyName", "", false),
			Entry("empty hardware version", "hardwareVersion", "", false),
			Entry("empty PCI devices", "pciDevices", []interface{}{}, false),
		)
	}, Entry("Machine", "VSphereMachine"), Entry("MachineTemplate", "VSphereMachineTemplate"))
})
