package main

import (
	"flag"
	"fmt"
	"os"
	"path"

	certmangerv1 "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1"
	admissionregistration "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/version"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clusterctlv1 "sigs.k8s.io/cluster-api/cmd/clusterctl/api/v1alpha3"
)

var basePath = flag.String("base-path", "", "path to the root of the provider's repository")
var providerName = flag.String("provider-name", "", "name of the provider")
var providerType = flag.String("provider-type", "", "type of the provider")
var providerVersion = flag.String("provider-version", "", "version of the provider")
var projDir string
var manifestsPath string

var (
	scheme          = runtime.NewScheme()
	manifestPrefix  = "0000_30_cluster-api_"
	targetNamespace = "openshift-cluster-api"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(admissionregistration.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(certmangerv1.AddToScheme(scheme))
}

func main() {
	flag.Parse()

	if err := validateFlags(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	projDir = path.Join(*basePath)
	manifestsPath = path.Join(projDir, "manifests")

	p := provider{
		Name: *providerName,
		// TODO: improve validation
		PType:   clusterctlv1.ProviderType(*providerType),
		Version: *providerVersion,
	}

	if err := importProvider(p); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func validateFlags() error {
	if *providerName == "" || *providerType == "" || *providerVersion == "" || *basePath == "" {
		return fmt.Errorf("error mandatory flags must be specified")
	}

	if _, err := version.ParseSemantic(*providerVersion); err != nil {
		return fmt.Errorf("invalid version %s for provider %s", *providerVersion, *providerName)
	}

	return nil
}
