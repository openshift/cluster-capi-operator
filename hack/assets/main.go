package main

import (
	"fmt"
	"os"
	"path"

	certmangerv1 "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1"
	admissionregistration "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

var (
	scheme              = runtime.NewScheme()
	projDir             = path.Join("..", "..")
	providersAssetsPath = path.Join(projDir, "assets", "infrastructure-providers")
	coreCAPIAssetsPath  = path.Join(projDir, "assets", "core-capi")
	manifestsPath       = path.Join(projDir, "manifests")
	providerListPath    = path.Join(projDir, "providers-list.yaml")
	operatorConfigPath  = path.Join(projDir, "cluster-api-operator.yaml")
	manifestPrefix      = "0000_30_cluster-api_"
	targetNamespace     = "openshift-cluster-api"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(admissionregistration.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(certmangerv1.AddToScheme(scheme))
}

func main() {
	providerName := getProviderFromArgs()
	if providerName == "" || providerName == "operator" {
		if err := importCAPIOperator(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	if providerName != "operator" {
		if err := importProviders(providerName); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}
}

func getProviderFromArgs() string {
	if len(os.Args) == 2 {
		return os.Args[1]
	}
	return ""
}
