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
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

var (
	allowedPlatformTypes = []string{
		string(configv1.AWSPlatformType),
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
	manifestsPath          string
	profileName            string
	kustomizeDir           string
	name                   string
	platform               string
	protectClusterResource string
	selfImageRef           string
	installOrder           int
	attributes             map[string]string
}

// attributeFlags allows collecting multiple --attribute flags.
type attributeFlags map[string]string

func (a attributeFlags) String() string {
	return fmt.Sprintf("%v", map[string]string(a))
}

func (a attributeFlags) Set(value string) error {
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("attribute must be in key=value format: %s", value)
	}

	a[parts[0]] = parts[1]

	return nil
}

func main() {
	var (
		manifestsPath = flag.String("manifests-path", "", "Path to the desired directory where to output the generated manifests. Required.")
		profileName   = flag.String("profile-name", "default", "Name of the profile, e.g 'featuregate-foo' (default: 'default'.'")
		kustomizeDir  = flag.String("kustomize-dir", "", "Directory containing kustomization.yaml file used to generate the base resources, relative to the current working directory. Required.")

		name = flag.String("name", "", "Name of the provider, e.g. 'cluster-api-provider-aws'. Required.")

		platform               = flag.String("platform", "", "OpenShift platform type (i.e. the same value found in the Infrastructure object). Optional.")
		protectClusterResource = flag.String("protect-cluster-resource", "", "Singular name of a cluster resource, e.g. 'awscluster'. Generates a ValidatingAdmissionPolicy which prevents modification of cluster resources created by the CAPI Operator. If provided matches any CRD in the manifests with this name. If not provided, matches any CRD in the manifests in the 'infrastructure.cluster.x-k8s.io' group whose plural name ends in 'clusters'. Optional.")
		selfImageRef           = flag.String("self-image-ref", "", "Image reference of the provider in generated manifests, e.g. registry.ci.openshift.org/openshift:aws-cluster-api-controllers. If specified, this string will be substituted with the provider's release image when the manifests are installed. Optional.")
		installOrder           = flag.Int("install-order", 0, "Order in which providers are installed. Lower values are installed first. Optional.")
	)

	attributes := make(attributeFlags)
	flag.Var(attributes, "attribute", "Provider attribute in key=value format. Can be specified multiple times. Optional.")

	flag.Parse()

	opts := cmdlineOptions{
		manifestsPath:          *manifestsPath,
		profileName:            *profileName,
		kustomizeDir:           *kustomizeDir,
		name:                   *name,
		platform:               *platform,
		protectClusterResource: *protectClusterResource,
		selfImageRef:           *selfImageRef,
		installOrder:           *installOrder,
		attributes:             attributes,
	}

	if err := validateFlags(opts); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if err := generateManifests(opts); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func validateFlags(opts cmdlineOptions) error {
	return errors.Join(
		hasValue("kustomize directory", opts.kustomizeDir),
		hasValue("manifests path", opts.manifestsPath),
		hasValue("name", opts.name),

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
