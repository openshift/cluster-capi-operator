package e2e

import (
	"context"
	"testing"

	metal3v1 "github.com/metal3-io/cluster-api-provider-metal3/api/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	gcpv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1beta1"
)

const (
	infrastructureName                                                = "cluster"
	infraAPIVersion                                                   = "infrastructure.cluster.x-k8s.io/v1beta1"
	managedByAnnotationValueClusterCAPIOperatorInfraClusterController = "cluster-capi-operator-infracluster-controller"
)

var (
	cl                 runtimeclient.Client
	ctx                = context.Background()
	platform           configv1.PlatformType
	clusterName        string
	mapiInfrastructure *configv1.Infrastructure
)

func init() {
	utilruntime.Must(configv1.Install(scheme.Scheme))
	utilruntime.Must(awsv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(gcpv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(mapiv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(ibmpowervsv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(vspherev1.AddToScheme(scheme.Scheme))
	utilruntime.Must(metal3v1.AddToScheme(scheme.Scheme))
}

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cluster API Suite")
}

var _ = BeforeSuite(func() {
	cfg, err := config.GetConfig()
	Expect(err).ToNot(HaveOccurred())

	cl, err = runtimeclient.New(cfg, runtimeclient.Options{})
	Expect(err).ToNot(HaveOccurred())

	infra := &configv1.Infrastructure{}
	infraName := runtimeclient.ObjectKey{
		Name: infrastructureName,
	}
	Expect(cl.Get(ctx, infraName, infra)).To(Succeed())
	Expect(infra.Status.PlatformStatus).ToNot(BeNil())
	mapiInfrastructure = infra
	clusterName = infra.Status.InfrastructureName
	platform = infra.Status.PlatformStatus.Type
})
