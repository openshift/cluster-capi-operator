package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	gconfig "github.com/onsi/ginkgo/config"
	"github.com/onsi/ginkgo/reporters"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
)

const (
	junitDirEnvVar     = "JUNIT_DIR"
	infrastructureName = "cluster"
	infraAPIVersion    = "infrastructure.cluster.x-k8s.io/v1beta1"
)

var (
	cl          runtimeclient.Client
	ctx         = context.Background()
	platform    configv1.PlatformType
	clusterName string
)

func init() {
	utilruntime.Must(configv1.Install(scheme.Scheme))
	utilruntime.Must(awsv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(mapiv1.AddToScheme(scheme.Scheme))
}

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecsWithDefaultAndCustomReporters(t, "Cluster API Suite", e2eReporters())
}

func e2eReporters() []Reporter {
	reportDir := os.Getenv(junitDirEnvVar)
	if reportDir != "" {
		// Include `ParallelNode` so tests running in parallel do not overwrite the same file.
		// Include timestamp so test suite can be called multiple times with focus within same CI job
		// without overwriting files.
		junitFileName := fmt.Sprintf("%s/junit_cluster_api_e2e_%d_%d.xml", reportDir, time.Now().UnixNano(), gconfig.GinkgoConfig.ParallelNode)
		return []Reporter{reporters.NewJUnitReporter(junitFileName)}
	}
	return []Reporter{printer.NewlineReporter{}}
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
	clusterName = infra.Status.InfrastructureName
	platform = infra.Status.PlatformStatus.Type
})
