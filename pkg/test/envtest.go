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
package test

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	mapiv1 "github.com/openshift/api/machine/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"golang.org/x/tools/go/packages"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	azurev1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	gcpv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	. "github.com/onsi/ginkgo/v2"
	configv1 "github.com/openshift/api/config/v1"
	clusteroperatorv1 "github.com/openshift/api/operator/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
)

func init() {
	utilruntime.Must(configv1.Install(scheme.Scheme))
	utilruntime.Must(operatorv1alpha1.Install(scheme.Scheme))
	utilruntime.Must(mapiv1.Install(scheme.Scheme))
	utilruntime.Must(mapiv1beta1.Install(scheme.Scheme))
	utilruntime.Must(clusteroperatorv1.Install(scheme.Scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(awsv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(azurev1.AddToScheme(scheme.Scheme))
	utilruntime.Must(gcpv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(openstackv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(apiextensionsv1alpha1.Install(scheme.Scheme))
}

// StartEnvTest starts a new test environment and returns a client and config.
// The returned client implements client.WithWatch for use with interceptor clients.
func StartEnvTest(testEnv *envtest.Environment) (*rest.Config, client.WithWatch, error) {
	// Get the directory containing the openshift/api package.
	openshiftAPIPath, err := getPackageDir(context.TODO(), "github.com/openshift/api")
	if err != nil {
		return nil, nil, err
	}

	testEnv.CRDs = []*apiextensionsv1.CustomResourceDefinition{
		fakeCoreProviderCRD,
		fakeInfrastructureProviderCRD,
		fakeClusterCRD,
		fakeMachineCRD,
		fakeMachineSetCRD,
		fakeControlPlaneMachineSetCRD,
		fakeAWSClusterCRD,
		fakeAWSMachineTemplateCRD,
		fakeAWSMachineCRD,
		fakeAzureClusterCRD,
		fakeGCPClusterCRD,
		fakeOpenStackClusterCRD,
		fakeOpenStackMachineTemplateCRD,
	}

	testEnv.CRDDirectoryPaths = []string{
		path.Join(openshiftAPIPath, "config", "v1", "zz_generated.crd-manifests"),
		path.Join(openshiftAPIPath, "operator", "v1", "zz_generated.crd-manifests", "0000_10_config-operator_01_configs.crd.yaml"),
	}
	testEnv.ErrorIfCRDPathMissing = true

	testEnv.CRDInstallOptions = envtest.CRDInstallOptions{
		Paths: []string{
			path.Join(openshiftAPIPath, "machine", "v1beta1", "zz_generated.crd-manifests", "0000_10_machine-api_01_machinesets-CustomNoUpgrade.crd.yaml"),
			path.Join(openshiftAPIPath, "machine", "v1beta1", "zz_generated.crd-manifests", "0000_10_machine-api_01_machines-CustomNoUpgrade.crd.yaml"),
			path.Join(openshiftAPIPath, "config", "v1", "zz_generated.crd-manifests", "0000_00_cluster-version-operator_01_clusteroperators.crd.yaml"),
			path.Join(openshiftAPIPath, "apiextensions", "v1alpha1", "zz_generated.crd-manifests", "0000_20_crd-compatibility-checker_01_compatibilityrequirements.crd.yaml"),
			path.Join(openshiftAPIPath, "operator", "v1alpha1", "zz_generated.crd-manifests", "0000_30_cluster-api_01_clusterapis.crd.yaml"),
		},
		ErrorIfPathMissing: true,
	}

	cfg, err := testEnv.Start()
	if err != nil {
		return nil, nil, err
	}

	if cfg == nil {
		return nil, nil, errors.New("envtest.Environment.Start() returned nil config")
	}

	cl, err := client.NewWithWatch(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return nil, nil, err
	}

	return cfg, cl, nil
}

// StopEnvTest stops the test environment.
func StopEnvTest(testEnv *envtest.Environment) error {
	return testEnv.Stop()
}

func getPackageDir(ctx context.Context, pkgName string) (string, error) {
	cfg := &packages.Config{
		Mode:    packages.NeedFiles | packages.NeedModule,
		Context: ctx,
	}

	pkgs, err := packages.Load(cfg, pkgName)
	if err != nil {
		return "", err
	}

	if len(pkgs) == 0 {
		return "", fmt.Errorf("package %s not found", pkgName)
	}

	if len(pkgs) > 1 {
		return "", fmt.Errorf("multiple packages found for %s", pkgName)
	}

	// Follow the chain of module replacements to find the actual module
	module := pkgs[0].Module
	for module != nil && module.Dir == "" && module.Replace != nil {
		module = module.Replace
	}

	if module == nil {
		return "", fmt.Errorf("module not found for %s", pkgName)
	}

	// Fallback to the package dir if nothing else is found.
	// This can be the case when using vendoring with a remote replacement.
	if module.Dir == "" && pkgs[0].Dir != "" {
		return pkgs[0].Dir, nil
	}

	return module.Dir, nil
}

// NewVerboseGinkgoLogger sets up a new logr.Logger that writes to GinkoWriter, and uses the passed verbosity
// Useful for debugging.
func NewVerboseGinkgoLogger(verbosity int) logr.Logger {
	return funcr.New(func(prefix, args string) {
		if prefix == "" {
			fmt.Fprintf(GinkgoWriter, "%s\n", args) //nolint:errcheck
		} else {
			fmt.Fprintf(GinkgoWriter, "%s %s\n", prefix, args) //nolint:errcheck
		}
	}, funcr.Options{Verbosity: verbosity})
}
