/*
Copyright 2024 Red Hat, Inc.

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
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1builder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/test"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/textlogger"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	//+kubebuilder:scaffold:imports
)

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var testScheme *runtime.Scheme
var testRESTMapper meta.RESTMapper
var ctx = context.Background()

const (
	timeout = time.Second * 2
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	klog.SetOutput(GinkgoWriter)

	logf.SetLogger(textlogger.NewLogger(textlogger.NewConfig()))

	By("writing out the audit policy to a temp dir")
	policyYaml := `
apiVersion: audit.k8s.io/v1
kind: Policy
# Drop the very first “RequestReceived” stage across the board
omitStages:
  - RequestReceived

rules:
  # 1) Full request+response for machine UPDATEs
  - level: RequestResponse
    verbs: ["update"]
    resources:
      - group: "machine.openshift.io"
        resources: ["machines"]

  # 2) Drop all other events (empty resources list => all groups & resources)
  - level: None
    resources: []
`
	tmp := os.TempDir()
	policyPath := filepath.Join(tmp, "audit-policy.yaml")

	Expect(os.WriteFile(policyPath, []byte(policyYaml), 0644)).To(Succeed())

	By("bootstrapping test environment")
	var err error
	testEnv = &envtest.Environment{
		ControlPlaneStartTimeout: 30 * time.Second,
		ControlPlane: envtest.ControlPlane{
			APIServer: &envtest.APIServer{
				Args: []string{
					"--vmodule=validatingadmissionpolicy*=6,cel*=6",
					"--audit-policy-file=" + policyPath,
					"--audit-log-path=/tmp/kube-apiserver-audit.log",
					"--audit-log-format=json",
					"--advertise-address=127.0.0.1",
				},
			},
		},
	}
	cfg, k8sClient, err = test.StartEnvTest(testEnv)

	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())
	Expect(k8sClient).NotTo(BeNil())

	infrastructure := configv1builder.Infrastructure().AsAWS("test", "eu-west-2").WithName("cluster").Build()
	Expect(k8sClient.Create(ctx, infrastructure)).To(Succeed())

	httpClient, err := rest.HTTPClientFor(cfg)
	Expect(err).NotTo(HaveOccurred())
	Expect(httpClient).NotTo(BeNil())

	testRESTMapper, err = apiutil.NewDynamicRESTMapper(cfg, httpClient)
	Expect(err).NotTo(HaveOccurred())

	komega.SetClient(k8sClient)
	komega.SetContext(ctx)
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
