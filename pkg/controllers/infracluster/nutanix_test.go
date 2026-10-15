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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	nutanixv1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	configv1 "github.com/openshift/api/config/v1"
	configv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var _ = Describe("ensureNutanixCluster", func() {
	const (
		infraName     = "test-infra-cluster-name"
		capiNamespace = "openshift-cluster-api"
	)

	var (
		testScheme *runtime.Scheme
		ocpInfra   *configv1.Infrastructure
	)

	BeforeEach(func() {
		testScheme = runtime.NewScheme()
		Expect(nutanixv1.AddToScheme(testScheme)).To(Succeed())

		ocpInfra = configv1resourcebuilder.Infrastructure().AsNutanix(infraName).Build()
	})

	newController := func(objs ...client.Object) *InfraClusterController {
		return &InfraClusterController{
			Client:        fake.NewClientBuilder().WithScheme(testScheme).WithObjects(objs...).Build(),
			Platform:      configv1.NutanixPlatformType,
			Infra:         ocpInfra,
			CAPINamespace: capiNamespace,
		}
	}

	It("creates a NutanixCluster populated from the Infrastructure resource", func() {
		r := newController()

		obj, err := r.ensureNutanixCluster(context.Background(), log.Log)
		Expect(err).NotTo(HaveOccurred())

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

		Expect(nutanixCluster.Spec.PrismCentral).NotTo(BeNil())
		Expect(nutanixCluster.Spec.PrismCentral.Address).To(Equal(ocpInfra.Spec.PlatformSpec.Nutanix.PrismCentral.Address))
		Expect(nutanixCluster.Spec.PrismCentral.Port).To(Equal(ocpInfra.Spec.PlatformSpec.Nutanix.PrismCentral.Port))
	})

	It("returns the existing NutanixCluster when one already exists", func() {
		existing := &nutanixv1.NutanixCluster{}
		existing.Name = infraName
		existing.Namespace = capiNamespace

		r := newController(existing)

		obj, err := r.ensureNutanixCluster(context.Background(), log.Log)
		Expect(err).NotTo(HaveOccurred())
		Expect(obj.GetName()).To(Equal(infraName))
		Expect(obj.GetNamespace()).To(Equal(capiNamespace))
	})

	It("returns an error when the Nutanix PlatformSpec is not set", func() {
		ocpInfra.Spec.PlatformSpec.Nutanix = nil
		r := newController()

		_, err := r.ensureNutanixCluster(context.Background(), log.Log)
		Expect(err).To(MatchError(errNutanixPrismCentralNotSet))
	})
})
