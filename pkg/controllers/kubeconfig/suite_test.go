package kubeconfig

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var (
	testEnv *envtest.Environment
	cfg     *rest.Config
	cl      client.Client
	ctx     = context.Background()
	timeout = 10 * time.Second
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kubeconfig Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(klog.Background())

	By("bootstrapping test environment")
	var err error
	testEnv = &envtest.Environment{}
	cfg, cl, err = test.StartEnvTest(testEnv)
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())
	Expect(cl).NotTo(BeNil())

	komega.SetClient(cl)
	komega.SetContext(ctx)
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	Expect(test.StopEnvTest(testEnv)).To(Succeed())
})
