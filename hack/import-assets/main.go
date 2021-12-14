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
	providersAssetsPath = path.Join(projDir, "assets", "providers")
	operatorAssetsPath  = path.Join(projDir, "assets", "capi-operator")
	manifestsPath       = path.Join(projDir, "manifests")
	providerListPath    = path.Join(projDir, "providers-list.yaml")
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
	if err := importCAPIOperator(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if err := importProviders(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
