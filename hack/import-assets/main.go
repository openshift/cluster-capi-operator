package main

import (
	"flag"
	"fmt"
	"os"
	"path"
	"strings"

	admissionregistration "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

var (
	scheme             = runtime.NewScheme()
	projDir            = path.Join("..", "..")
	cmdMoveRBAC        = "move-rbac-manifests"
	cmdImportProviders = "import-providers"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(admissionregistration.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
}

func usage() {
	fmt.Fprint(flag.CommandLine.Output(), "usage:\n")
	fmt.Fprintf(flag.CommandLine.Output(), "  %s %s\n", os.Args[0], cmdMoveRBAC)
	fmt.Fprintf(flag.CommandLine.Output(), "  %s %s\n", os.Args[0], cmdImportProviders)
	flag.PrintDefaults()
}

func checkArgs(required int) {
	if len(flag.Args()) != required {
		usage()
		os.Exit(2)
	}
}

func main() {
	flag.Usage = usage
	flag.Parse()

	var err error
	switch strings.ToLower(flag.Arg(0)) {
	case cmdMoveRBAC:
		checkArgs(1)
		err = moveRBACToManifests()
	case cmdImportProviders:
		checkArgs(1)
		err = importProviders()
	}
	if err != nil {
		fmt.Println(err)
		os.Exit(1)

	}
}
