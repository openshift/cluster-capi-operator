package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	certmangerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	configv1 "github.com/openshift/api/config/v1"
	admissionregistration "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/version"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

const (
	defaultKustomizeComponentsPath = "./config/default"

	providerTypeCore           = "core"
	providerTypeInfrastructure = "infrastructure"
)

var (
	allowedPlatformTypes = []string{
		string(configv1.AWSPlatformType),
		string(configv1.AlibabaCloudPlatformType),
		string(configv1.AzurePlatformType),
		string(configv1.BareMetalPlatformType),
		string(configv1.EquinixMetalPlatformType),
		string(configv1.ExternalPlatformType),
		string(configv1.GCPPlatformType),
		string(configv1.IBMCloudPlatformType),
		string(configv1.KubevirtPlatformType),
		string(configv1.LibvirtPlatformType),
		string(configv1.NonePlatformType),
		string(configv1.NutanixPlatformType),
		string(configv1.OpenStackPlatformType),
		string(configv1.OvirtPlatformType),
		string(configv1.PowerVSPlatformType),
		string(configv1.VSpherePlatformType),
	}

	scheme = runtime.NewScheme()
)

func init() {
	// Required by findWebhookServiceSecretName
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(admissionregistration.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(certmangerv1.AddToScheme(scheme))
}

type cmdlineOptions struct {
	basePath             string
	manifestsPath        string
	kustomizeDir         string
	name                 string
	providerType         string
	version              string
	platform             string
	infraClusterResource string
}

func main() {
	var (
		basePath             = flag.String("base-path", "", "path to the root of the provider's repository")
		manifestsPath        = flag.String("manifests-path", "", "path to the desired directory where to output the generated manifests")
		kustomizeDir         = flag.String("kustomize-dir", defaultKustomizeComponentsPath, "directory to search for kustomization.yaml file, relative to the base-path (default: ./config/default)")
		providerName         = flag.String("provider-name", "", "name of the provider")
		providerType         = flag.String("provider-type", "", "type of the provider: core or infrastructure")
		providerVersion      = flag.String("provider-version", "", "version of the provider")
		platform             = flag.String("platform", "", "platform of the provider")
		infraClusterResource = flag.String("infracluster-resource", "", "name of the infrastructure cluster resource, e.g. 'awscluster'")
	)

	flag.Parse()

	opts := cmdlineOptions{
		basePath:             *basePath,
		manifestsPath:        *manifestsPath,
		kustomizeDir:         *kustomizeDir,
		name:                 *providerName,
		providerType:         *providerType,
		version:              *providerVersion,
		platform:             *platform,
		infraClusterResource: *infraClusterResource,
	}

	if err := validateFlags(opts); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if err := generateManifestBundle(opts); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func validateFlags(opts cmdlineOptions) error {
	return errors.Join(
		hasValue("base path", opts.basePath),
		hasValue("manifests path", opts.manifestsPath),
		hasValue("provider name", opts.name),

		func() error {
			// If set, provider type must be valid
			if opts.providerType != "" {
				if opts.providerType != providerTypeCore && opts.providerType != providerTypeInfrastructure {
					return fmt.Errorf("valid provider types are %s or %s, invalid provider type: %s", providerTypeCore, providerTypeInfrastructure, opts.providerType)
				}
			}
			return nil
		}(),

		func() error {
			// If set, provider version must be valid
			if opts.version != "" {
				if _, err := version.ParseSemantic(opts.version); err != nil {
					return fmt.Errorf("invalid version %s for provider %s", opts.version, opts.name)
				}
			}
			return nil
		}(),

		func() error {
			// If set, platform must be an allowed platform type
			if opts.platform != "" {
				if !slices.Contains(allowedPlatformTypes, opts.platform) {
					return fmt.Errorf("invalid platform %s for provider %s. Allowed platforms are: %s", opts.platform, opts.name, strings.Join(allowedPlatformTypes, ", "))
				}
			}
			return nil
		}(),
	)
}

func hasValue(description string, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", description)
	}
	return nil
}
