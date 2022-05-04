package assets

import (
	"testing"

	. "github.com/onsi/gomega"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	operatorv1 "sigs.k8s.io/cluster-api-operator/api/v1alpha1"
)

func init() {
	utilruntime.Must(operatorv1.AddToScheme(scheme.Scheme))
}

func TestReadCoreProviderAssets(t *testing.T) {
	g := NewGomegaWithT(t)

	objs, err := ReadCoreProviderAssets(scheme.Scheme)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(objs).To(HaveLen(2))

	g.Expect(objs).Should(HaveKey(CoreProviderKey))
	g.Expect(objs[CoreProviderKey]).ToNot(BeNil())
	g.Expect(objs[CoreProviderKey].GetObjectKind().GroupVersionKind().Kind).To(Equal("CoreProvider"))

	g.Expect(objs).Should(HaveKey(CoreProviderConfigMapKey))
	g.Expect(objs[CoreProviderConfigMapKey]).ToNot(BeNil())
	g.Expect(objs[CoreProviderConfigMapKey].GetObjectKind().GroupVersionKind().Kind).To(Equal("ConfigMap"))
}

func TestReadInfrastructureProviderAssets(t *testing.T) {
	g := NewGomegaWithT(t)

	objs, err := ReadInfrastructureProviderAssets(scheme.Scheme, "aws")
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(objs).To(HaveLen(2))

	g.Expect(objs).Should(HaveKey(InfrastructureProviderKey))
	g.Expect(objs[InfrastructureProviderKey]).ToNot(BeNil())
	g.Expect(objs[InfrastructureProviderKey].GetObjectKind().GroupVersionKind().Kind).To(Equal("InfrastructureProvider"))

	g.Expect(objs).Should(HaveKey(InfrastructureProviderConfigMapKey))
	g.Expect(objs[InfrastructureProviderConfigMapKey]).ToNot(BeNil())
	g.Expect(objs[InfrastructureProviderConfigMapKey].GetObjectKind().GroupVersionKind().Kind).To(Equal("ConfigMap"))
}
