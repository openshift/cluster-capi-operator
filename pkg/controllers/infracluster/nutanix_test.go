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

package infracluster

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	nutanixv1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	credentialTypes "github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	configv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var _ = Describe("ensureNutanixCluster", func() {
	const (
		infraName          = "test-infra-cluster-name"
		capiNamespace      = "openshift-cluster-api"
		mapiNamespace      = "openshift-machine-api"
		mapiSecretName     = "nutanix-credentials"
		credentialsPayload = `[{"type":"basic_auth","data":{"prismCentral":{"username":"user","password":"pass"}}}]`
	)

	var (
		testScheme *runtime.Scheme
		ocpInfra   *configv1.Infrastructure
	)

	BeforeEach(func() {
		testScheme = runtime.NewScheme()
		Expect(nutanixv1.AddToScheme(testScheme)).To(Succeed())
		Expect(corev1.AddToScheme(testScheme)).To(Succeed())
		Expect(mapiv1.AddToScheme(testScheme)).To(Succeed())
		Expect(mapiv1beta1.AddToScheme(testScheme)).To(Succeed())

		ocpInfra = configv1resourcebuilder.Infrastructure().AsNutanix(infraName).Build()
	})

	newController := func(objs ...client.Object) *InfraClusterController {
		return &InfraClusterController{
			Client:        fake.NewClientBuilder().WithScheme(testScheme).WithObjects(objs...).Build(),
			Platform:      configv1.NutanixPlatformType,
			Infra:         ocpInfra,
			CAPINamespace: capiNamespace,
			MAPINamespace: mapiNamespace,
		}
	}

	mapiCredentials := func(data []byte) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: mapiSecretName, Namespace: mapiNamespace},
			Data:       map[string][]byte{credentialTypes.KeyName: data},
		}
	}

	controlPlaneMachine := func() *mapiv1beta1.Machine {
		GinkgoHelper()

		raw, err := json.Marshal(&mapiv1.NutanixMachineProviderConfig{
			TypeMeta:          metav1.TypeMeta{APIVersion: "machine.openshift.io/v1", Kind: "NutanixMachineProviderConfig"},
			CredentialsSecret: &corev1.LocalObjectReference{Name: mapiSecretName},
		})
		Expect(err).NotTo(HaveOccurred(), "failed to marshal Nutanix Machine API provider spec")

		return &mapiv1beta1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "control-plane",
				Namespace: mapiNamespace,
				Labels:    map[string]string(controlPlaneSelector()),
			},
			Spec: mapiv1beta1.MachineSpec{
				ProviderSpec: mapiv1beta1.ProviderSpec{Value: &runtime.RawExtension{Raw: raw}},
			},
		}
	}

	It("creates a NutanixCluster populated from the Infrastructure resource", func() {
		r := newController(mapiCredentials([]byte(credentialsPayload)), controlPlaneMachine())

		obj, err := r.ensureNutanixCluster(context.Background(), logf.Log)
		Expect(err).NotTo(HaveOccurred(), "failed to create NutanixCluster")

		nutanixCluster, ok := obj.(*nutanixv1.NutanixCluster)
		Expect(ok).To(BeTrue(), "expected a *NutanixCluster")

		Expect(nutanixCluster).To(SatisfyAll(
			HaveField("ObjectMeta.Name", Equal(infraName)),
			HaveField("ObjectMeta.Namespace", Equal(capiNamespace)),
			HaveField("ObjectMeta.Annotations", HaveKeyWithValue(
				clusterv1.ManagedByAnnotation, managedByAnnotationValueClusterCAPIOperatorInfraClusterController)),
			HaveField("Spec.ControlPlaneEndpoint.Host", Equal("api-int.test-cluster.test-domain")),
			HaveField("Spec.ControlPlaneEndpoint.Port", BeEquivalentTo(6443)),
		))

		Expect(nutanixCluster.Spec.PrismCentral).To(SatisfyAll(
			Not(BeNil()),
			HaveField("Address", Equal(ocpInfra.Spec.PlatformSpec.Nutanix.PrismCentral.Address)),
			HaveField("Port", Equal(ocpInfra.Spec.PlatformSpec.Nutanix.PrismCentral.Port)),
			HaveField("CredentialRef", SatisfyAll(
				Not(BeNil()),
				HaveField("Kind", Equal(credentialTypes.SecretKind)),
				HaveField("Name", Equal(infraName)),
				HaveField("Namespace", Equal(capiNamespace)),
			)),
		))

		Expect(nutanixCluster.Spec.PrismCentral.AdditionalTrustBundle).To(BeNil(), "expected no trust bundle when no user CA bundle is configured")

		copied := &corev1.Secret{}
		Expect(r.Get(context.Background(), client.ObjectKey{Name: infraName, Namespace: capiNamespace}, copied)).To(Succeed(), "expected the Prism Central credentials secret to be created")
		Expect(copied.Data).To(HaveKeyWithValue(credentialTypes.KeyName, []byte(credentialsPayload)), "expected the copied secret to contain the Machine API credentials")
	})

	It("copies the user CA bundle and references it when Prism Central uses a non-public CA", func() {
		const caBundle = "-----BEGIN CERTIFICATE-----\nMIIexample\n-----END CERTIFICATE-----"

		userCABundle := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: userCABundleConfigMapName, Namespace: ConfigNamespace},
			Data:       map[string]string{userCABundleKey: caBundle},
		}

		r := newController(mapiCredentials([]byte(credentialsPayload)), controlPlaneMachine(), userCABundle)

		obj, err := r.ensureNutanixCluster(context.Background(), logf.Log)
		Expect(err).NotTo(HaveOccurred(), "failed to create NutanixCluster")

		nutanixCluster, ok := obj.(*nutanixv1.NutanixCluster)
		Expect(ok).To(BeTrue(), "expected a *NutanixCluster")

		Expect(nutanixCluster.Spec.PrismCentral.AdditionalTrustBundle).To(SatisfyAll(
			Not(BeNil()),
			HaveField("Kind", Equal(credentialTypes.NutanixTrustBundleKindConfigMap)),
			HaveField("Name", Equal(nutanixTrustBundleConfigMapName)),
			HaveField("Namespace", Equal(capiNamespace)),
		), "expected the trust bundle to be referenced")

		copied := &corev1.ConfigMap{}
		Expect(r.Get(context.Background(), client.ObjectKey{Name: nutanixTrustBundleConfigMapName, Namespace: capiNamespace}, copied)).To(Succeed(), "expected the trust bundle configmap to be created")
		Expect(copied.Data).To(HaveKeyWithValue(nutanixTrustBundleKey, caBundle), "expected the copied configmap to carry the CA bundle under the key the provider reads")
	})

	It("returns an error and does not create a secret when the Machine API credentials data is missing", func() {
		r := newController(mapiCredentials(nil), controlPlaneMachine())

		_, err := r.ensureNutanixCluster(context.Background(), logf.Log)
		Expect(err).To(MatchError(ContainSubstring(errNutanixCredentialsDataMissing.Error())), "expected missing credentials data to fail closed")

		copied := &corev1.Secret{}
		Expect(r.Get(context.Background(), client.ObjectKey{Name: infraName, Namespace: capiNamespace}, copied)).To(MatchError(ContainSubstring("not found")), "did not expect an empty credentials secret to be created")
	})

	It("returns the existing NutanixCluster when one already exists", func() {
		existing := &nutanixv1.NutanixCluster{}
		existing.Name = infraName
		existing.Namespace = capiNamespace

		r := newController(existing, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: infraName, Namespace: capiNamespace},
			Data:       map[string][]byte{credentialTypes.KeyName: []byte(credentialsPayload)},
		})

		obj, err := r.ensureNutanixCluster(context.Background(), logf.Log)
		Expect(err).NotTo(HaveOccurred(), "failed to retrieve existing NutanixCluster")
		Expect(obj.GetName()).To(Equal(infraName))
		Expect(obj.GetNamespace()).To(Equal(capiNamespace))
	})

	It("returns an error when the Nutanix PlatformSpec is not set", func() {
		ocpInfra.Spec.PlatformSpec.Nutanix = nil
		r := newController()

		_, err := r.ensureNutanixCluster(context.Background(), logf.Log)
		Expect(err).To(MatchError(errNutanixPrismCentralNotSet))
	})
})
