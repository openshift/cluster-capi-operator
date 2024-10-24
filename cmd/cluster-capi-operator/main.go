// Copyright 2024 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	metal3v1 "github.com/metal3-io/cluster-api-provider-metal3/api/v1beta1"
	"github.com/spf13/pflag"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/component-base/config"
	"k8s.io/component-base/config/options"
	klog "k8s.io/klog/v2"
	"k8s.io/klog/v2/textlogger"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	azurev1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	gcpv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	clusterctlv1 "sigs.k8s.io/cluster-api/cmd/clusterctl/api/v1alpha3"
	capiflags "sigs.k8s.io/cluster-api/util/flags"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	crwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/capiinstaller"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/cluster"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/infracluster"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/kubeconfig"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/secretsync"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/unsupported"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	"github.com/openshift/cluster-capi-operator/pkg/webhook"
)

const (
	defaultImagesLocation = "./dev-images.json"
)

func initScheme(scheme *runtime.Scheme) {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(admissionregistrationv1.AddToScheme(scheme))
	utilruntime.Must(awsv1.AddToScheme(scheme))
	utilruntime.Must(azurev1.AddToScheme(scheme))
	utilruntime.Must(gcpv1.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))
	utilruntime.Must(clusterctlv1.AddToScheme(scheme))
	utilruntime.Must(ibmpowervsv1.AddToScheme(scheme))
	utilruntime.Must(openstackv1.AddToScheme(scheme))
	utilruntime.Must(vspherev1.AddToScheme(scheme))
	utilruntime.Must(mapiv1.AddToScheme(scheme))
	utilruntime.Must(mapiv1beta1.AddToScheme(scheme))
	utilruntime.Must(metal3v1.AddToScheme(scheme))
}

//nolint:funlen
func main() {
	scheme := runtime.NewScheme()
	initScheme(scheme)

	leaderElectionConfig := config.LeaderElectionConfiguration{
		LeaderElect:       true,
		LeaseDuration:     util.LeaseDuration,
		RenewDeadline:     util.RenewDeadline,
		RetryPeriod:       util.RetryPeriod,
		ResourceName:      "cluster-capi-operator-leader",
		ResourceNamespace: "openshift-cluster-api",
	}
	capiManagerOptions := capiflags.ManagerOptions{}

	healthAddr := flag.String(
		"health-addr",
		":9440",
		"The address for health checking.",
	)
	managedNamespace := flag.String(
		"namespace",
		controllers.DefaultManagedNamespace,
		"The namespace where CAPI components will run.",
	)
	imagesFile := flag.String(
		"images-json",
		defaultImagesLocation,
		"The location of images file to use by operator for managed CAPI binaries.",
	)
	webhookPort := flag.Int(
		"webhook-port",
		9443,
		"The port for the webhook server to listen on.",
	)
	webhookCertDir := flag.String(
		"webhook-cert-dir",
		"/tmp/k8s-webhook-server/serving-certs/",
		"Webhook cert dir, only used when webhook-port is specified.",
	)

	logToStderr := flag.Bool(
		"logtostderr",
		true,
		"log to standard error instead of files",
	)

	textLoggerConfig := textlogger.NewConfig()
	textLoggerConfig.AddFlags(flag.CommandLine)
	ctrl.SetLogger(textlogger.NewLogger(textLoggerConfig))

	// Once all the flags are regitered, switch to pflag
	// to allow leader lection flags to be bound
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	options.BindLeaderElectionFlags(&leaderElectionConfig, pflag.CommandLine)
	capiflags.AddManagerOptions(pflag.CommandLine, &capiManagerOptions)
	pflag.Parse()

	if err := setFeatureGatesEnvVars(); err != nil {
		klog.Error(err, "unable to set feature gates environment variables")
		os.Exit(1)
	}

	if logToStderr != nil {
		klog.LogToStderr(*logToStderr)
	}

	_, diagnosticsOpts, err := capiflags.GetManagerOptions(capiManagerOptions)
	if err != nil {
		klog.Error(err, "unable to get manager options")
		os.Exit(1)
	}

	syncPeriod := 10 * time.Minute

	cacheOpts := cache.Options{
		DefaultNamespaces: map[string]cache.Config{
			*managedNamespace:                {},
			secretsync.SecretSourceNamespace: {},
			"kube-system":                    {}, // For fetching cloud credentials.
		},
		SyncPeriod: &syncPeriod,
	}

	cfg := ctrl.GetConfigOrDie()

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                  scheme,
		Metrics:                 *diagnosticsOpts,
		HealthProbeBindAddress:  *healthAddr,
		LeaderElectionNamespace: leaderElectionConfig.ResourceNamespace,
		LeaderElection:          leaderElectionConfig.LeaderElect,
		LeaseDuration:           &leaderElectionConfig.LeaseDuration.Duration,
		LeaderElectionID:        leaderElectionConfig.ResourceName,
		RetryPeriod:             &leaderElectionConfig.RetryPeriod.Duration,
		RenewDeadline:           &leaderElectionConfig.RenewDeadline.Duration,
		Cache:                   cacheOpts,
		WebhookServer: crwebhook.NewServer(crwebhook.Options{
			Port:    *webhookPort,
			CertDir: *webhookCertDir,
		}),
	})
	if err != nil {
		klog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	applyClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Error(err, "unable to set up apply client")
		os.Exit(1)
	}

	apiextensionsClient, err := apiextensionsclient.NewForConfig(cfg)
	if err != nil {
		klog.Error(err, "unable to set up apply client")
		os.Exit(1)
	}

	containerImages, err := util.ReadImagesFile(*imagesFile)
	if err != nil {
		klog.Error(err, "unable to get images from file", "name", *imagesFile)
		os.Exit(1)
	}

	infra, err := util.GetInfra(context.Background(), mgr.GetAPIReader())
	if err != nil {
		klog.Error(err, "unable to get infrastructure object")
		os.Exit(1)
	}

	platform, err := util.GetPlatform(context.Background(), infra)
	if err != nil {
		klog.Error(err, "unable to get platform from infrastructure object")
		os.Exit(1)
	}

	setupPlatformReconcilers(mgr, infra, platform, containerImages, applyClient, apiextensionsClient, *managedNamespace)

	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("health", healthz.Ping); err != nil {
		klog.Error(err, "unable to set up health check")
		os.Exit(1)
	}

	if err := mgr.AddReadyzCheck("check", healthz.Ping); err != nil {
		klog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	klog.Info("Starting manager")

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		klog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func getClusterOperatorStatusClient(mgr manager.Manager, controller string, managedNamespace string) operatorstatus.ClusterOperatorStatusClient {
	return operatorstatus.ClusterOperatorStatusClient{
		Client:           mgr.GetClient(),
		Recorder:         mgr.GetEventRecorderFor(controller),
		ReleaseVersion:   util.GetReleaseVersion(),
		ManagedNamespace: managedNamespace,
	}
}

func setupPlatformReconcilers(mgr manager.Manager, infra *configv1.Infrastructure, platform configv1.PlatformType, containerImages map[string]string, applyClient *kubernetes.Clientset, apiextensionsClient *apiextensionsclient.Clientset, managedNamespace string) {
	// Only setup reconcile controllers and webhooks when the platform is supported.
	// This avoids unnecessary CAPI providers discovery, installs and reconciles when the platform is not supported.
	switch platform {
	case configv1.AWSPlatformType:
		setupReconcilers(mgr, infra, platform, &awsv1.AWSCluster{}, containerImages, applyClient, apiextensionsClient, managedNamespace)
		setupWebhooks(mgr)
	case configv1.GCPPlatformType:
		setupReconcilers(mgr, infra, platform, &gcpv1.GCPCluster{}, containerImages, applyClient, apiextensionsClient, managedNamespace)
		setupWebhooks(mgr)
	case configv1.AzurePlatformType:
		azureCloudEnvironment := getAzureCloudEnvironment(infra.Status.PlatformStatus)
		if azureCloudEnvironment == configv1.AzureStackCloud {
			klog.Infof("Detected Azure Cloud Environment %q on platform %q is not supported, skipping capi controllers setup", azureCloudEnvironment, platform)
			setupUnsupportedController(mgr, managedNamespace)
		} else {
			setupReconcilers(mgr, infra, platform, &azurev1.AzureCluster{}, containerImages, applyClient, apiextensionsClient, managedNamespace)
			setupWebhooks(mgr)
		}
	case configv1.PowerVSPlatformType:
		setupReconcilers(mgr, infra, platform, &ibmpowervsv1.IBMPowerVSCluster{}, containerImages, applyClient, apiextensionsClient, managedNamespace)
		setupWebhooks(mgr)
	case configv1.VSpherePlatformType:
		setupReconcilers(mgr, infra, platform, &vspherev1.VSphereCluster{}, containerImages, applyClient, apiextensionsClient, managedNamespace)
		setupWebhooks(mgr)
	case configv1.OpenStackPlatformType:
		setupReconcilers(mgr, infra, platform, &openstackv1.OpenStackCluster{}, containerImages, applyClient, apiextensionsClient, managedNamespace)
		setupWebhooks(mgr)
	case configv1.BareMetalPlatformType:
		setupReconcilers(mgr, infra, platform, &metal3v1.Metal3Cluster{}, containerImages, applyClient, apiextensionsClient, managedNamespace)
		setupWebhooks(mgr)
	default:
		klog.Infof("Detected platform %q is not supported, skipping capi controllers setup", platform)
		setupUnsupportedController(mgr, managedNamespace)
	}
}

func setupReconcilers(mgr manager.Manager, infra *configv1.Infrastructure, platform configv1.PlatformType, infraClusterObject client.Object, containerImages map[string]string, applyClient *kubernetes.Clientset, apiextensionsClient *apiextensionsclient.Clientset, managedNamespace string) {
	if err := (&cluster.CoreClusterReconciler{
		ClusterOperatorStatusClient: getClusterOperatorStatusClient(mgr, "cluster-capi-operator-cluster-resource-controller", managedNamespace),
		Cluster:                     &clusterv1.Cluster{},
	}).SetupWithManager(mgr); err != nil {
		klog.Error(err, "unable to create controller", "controller", "CoreCluster")
		os.Exit(1)
	}

	if err := (&secretsync.UserDataSecretController{
		ClusterOperatorStatusClient: getClusterOperatorStatusClient(mgr, "cluster-capi-operator-user-data-secret-controller", managedNamespace),
		Scheme:                      mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		klog.Error(err, "unable to create user-data-secret controller", "controller", "UserDataSecret")
		os.Exit(1)
	}

	if err := (&kubeconfig.KubeconfigReconciler{
		ClusterOperatorStatusClient: getClusterOperatorStatusClient(mgr, "cluster-capi-operator-kubeconfig-controller", managedNamespace),
		Scheme:                      mgr.GetScheme(),
		RestCfg:                     mgr.GetConfig(),
	}).SetupWithManager(mgr); err != nil {
		klog.Error(err, "unable to create controller", "controller", "Kubeconfig")
		os.Exit(1)
	}

	if err := (&capiinstaller.CapiInstallerController{
		ClusterOperatorStatusClient: getClusterOperatorStatusClient(mgr, "cluster-capi-operator-capi-installer-controller", managedNamespace),
		Scheme:                      mgr.GetScheme(),
		Images:                      containerImages,
		RestCfg:                     mgr.GetConfig(),
		Platform:                    platform,
		ApplyClient:                 applyClient,
		APIExtensionsClient:         apiextensionsClient,
	}).SetupWithManager(mgr); err != nil {
		klog.Error(err, "unable to create capi installer controller", "controller", "CAPIInstaller")
		os.Exit(1)
	}

	if err := (&infracluster.InfraClusterController{
		ClusterOperatorStatusClient: getClusterOperatorStatusClient(mgr, "cluster-capi-operator-infracluster-controller", managedNamespace),
		Scheme:                      mgr.GetScheme(),
		Images:                      containerImages,
		RestCfg:                     mgr.GetConfig(),
		Platform:                    platform,
		Infra:                       infra,
	}).SetupWithManager(mgr, infraClusterObject); err != nil {
		klog.Error(err, "unable to create infracluster controller", "controller", "InfraCluster")
		os.Exit(1)
	}
}

func setupWebhooks(mgr ctrl.Manager) {
	if err := (&webhook.ClusterWebhook{}).SetupWebhookWithManager(mgr); err != nil {
		klog.Error(err, "unable to create webhook", "webhook", "Cluster")
		os.Exit(1)
	}
}

// setFeatureGatesEnvVars sets the explicit values for the listed feature gates in the environment.
// These will then be loaded by envsubst and templated into the applied CAPI manifests.
func setFeatureGatesEnvVars() error {
	featureGates := map[string]string{
		"EXP_BOOTSTRAP_FORMAT_IGNITION": "true",
	}

	for k, v := range featureGates {
		if err := os.Setenv(k, v); err != nil {
			return fmt.Errorf("error setting environment variable: %s: %w", k, err)
		}
	}

	return nil
}

// getAzureCloudEnvironment returns the current AzureCloudEnvironment.
func getAzureCloudEnvironment(ps *configv1.PlatformStatus) configv1.AzureCloudEnvironment {
	if ps == nil || ps.Azure == nil {
		return ""
	}

	return ps.Azure.CloudName
}

func setupUnsupportedController(mgr manager.Manager, ns string) {
	// UnsupportedController runs on unsupported platforms, it watches and keeps the cluster-api ClusterObject up to date.
	if err := (&unsupported.UnsupportedController{
		ClusterOperatorStatusClient: getClusterOperatorStatusClient(mgr, "cluster-capi-operator-unsupported-controller", ns),
		Scheme:                      mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		klog.Error(err, "unable to create unsupported controller", "controller", "Unsupported")
		os.Exit(1)
	}
}
