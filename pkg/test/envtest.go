package test

import (
	"errors"
	"path"
	goruntime "runtime"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	operatorv1 "sigs.k8s.io/cluster-api-operator/api/v1alpha1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	configv1 "github.com/openshift/api/config/v1"
	clusteroperatorv1 "github.com/openshift/api/operator/v1"
)

func init() {
	utilruntime.Must(configv1.Install(scheme.Scheme))
	utilruntime.Must(clusteroperatorv1.Install(scheme.Scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(operatorv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(awsv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme.Scheme))
}

func StartEnvTest(testEnv *envtest.Environment) (*rest.Config, client.Client, error) {
	// Get the root of the current file to use in CRD paths.
	_, filename, _, _ := goruntime.Caller(0) //nolint:dogsled
	root := path.Join(path.Dir(filename), "..", "..", "..", "cluster-capi-operator")

	testEnv.CRDs = []*apiextensionsv1.CustomResourceDefinition{
		fakeCoreProviderCRD,
		fakeInfrastructureProviderCRD,
		fakeAWSClusterCRD,
		fakeClusterCRD,
	}
	testEnv.CRDDirectoryPaths = []string{
		path.Join(root, "vendor", "github.com", "openshift", "api", "config", "v1"),
		path.Join(root, "vendor", "github.com", "openshift", "api", "operator", "v1"),
	}
	testEnv.ErrorIfCRDPathMissing = true

	cfg, err := testEnv.Start()
	if err != nil {
		return nil, nil, err
	}

	if cfg == nil {
		return nil, nil, errors.New("envtest.Environment.Start() returned nil config")
	}

	cl, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return nil, nil, err
	}

	return cfg, cl, nil
}

func StopEnvTest(testEnv *envtest.Environment) error {
	return testEnv.Stop()
}
