package assets

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	operatorv1 "sigs.k8s.io/cluster-api-operator/api/v1alpha1"
)

func init() {
	utilruntime.Must(operatorv1.AddToScheme(scheme.Scheme))
}

var _ = Describe("Read assets suite", func() {
	It("should read core provider assets", func() {

		objs, err := ReadCoreProviderAssets(scheme.Scheme)
		Expect(err).NotTo(HaveOccurred())

		Expect(objs).To(HaveLen(2))

		Expect(objs).Should(HaveKey(CoreProviderKey))
		Expect(objs[CoreProviderKey]).ToNot(BeNil())
		Expect(objs[CoreProviderKey].GetObjectKind().GroupVersionKind().Kind).To(Equal("CoreProvider"))

		Expect(objs).Should(HaveKey(CoreProviderConfigMapKey))
		Expect(objs[CoreProviderConfigMapKey]).ToNot(BeNil())
		Expect(objs[CoreProviderConfigMapKey].GetObjectKind().GroupVersionKind().Kind).To(Equal("ConfigMap"))
	})

	It("should read infra provider assets", func() {
		objs, err := ReadInfrastructureProviderAssets(scheme.Scheme, "aws")
		Expect(err).NotTo(HaveOccurred())

		Expect(objs).To(HaveLen(2))

		Expect(objs).Should(HaveKey(InfrastructureProviderKey))
		Expect(objs[InfrastructureProviderKey]).ToNot(BeNil())
		Expect(objs[InfrastructureProviderKey].GetObjectKind().GroupVersionKind().Kind).To(Equal("InfrastructureProvider"))

		Expect(objs).Should(HaveKey(InfrastructureProviderConfigMapKey))
		Expect(objs[InfrastructureProviderConfigMapKey]).ToNot(BeNil())
		Expect(objs[InfrastructureProviderConfigMapKey].GetObjectKind().GroupVersionKind().Kind).To(Equal("ConfigMap"))
	})
})
