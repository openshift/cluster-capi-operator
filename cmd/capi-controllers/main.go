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
	"flag"
	"os"
	"time"

	metal3v1 "github.com/metal3-io/cluster-api-provider-metal3/api/v1beta1"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	klog "k8s.io/klog/v2"

	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	azurev1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	gcpv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	clusterctlv1 "sigs.k8s.io/cluster-api/cmd/clusterctl/api/v1alpha3"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	crwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/clusteroperator"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/corecluster"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/infracluster"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/kubeconfig"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/secretsync"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	"github.com/openshift/cluster-capi-operator/pkg/webhook"
)

const (
	managerName = "cluster-capi-operator"

	defaultMachineAPINamespace = "openshift-machine-api"
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

func main() {
	scheme := runtime.NewScheme()
	initScheme(scheme)

	opts := util.InitCommonOptions(managerName, controllers.DefaultCAPINamespace)

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

	opts.Parse()

	cacheOpts := getDefaultCacheOptions(*opts.CAPINamespace, 10*time.Minute)

	cfg := ctrl.GetConfigOrDie()
	ctx := ctrl.SetupSignalHandler()

	mgrOpts, tlsOptions := opts.GetCommonManagerOptions()
	mgrOpts.Cache = cacheOpts
	mgrOpts.Scheme = scheme
	mgrOpts.WebhookServer = crwebhook.NewServer(crwebhook.Options{
		Port:    *webhookPort,
		CertDir: *webhookCertDir,
		TLSOpts: tlsOptions,
	})

	mgr, err := ctrl.NewManager(cfg, mgrOpts)
	if err != nil {
		klog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := util.AddCommonChecks(mgr); err != nil {
		klog.Error(err, "unable to add common checks")
		os.Exit(1)
	}

	platform, infra, err := util.GetPlatform(ctx, mgr.GetAPIReader())
	if err != nil {
		klog.Error(err, "unable to get platform")
		os.Exit(1)
	}

	setupPlatformReconcilers(mgr, opts, infra, platform)

	klog.Info("Starting manager")

	if err := mgr.Start(ctx); err != nil {
		klog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func setupPlatformReconcilers(mgr manager.Manager, opts *util.CommonOptions, infra *configv1.Infrastructure, platform configv1.PlatformType) {
	// Only setup reconcile controllers and webhooks when the platform is supported.
	// This avoids unnecessary CAPI providers discovery, installs and reconciles when the platform is not supported.
	isUnsupportedPlatform := false

	switch platform {
	case configv1.AWSPlatformType:
		setupReconcilers(mgr, opts, infra, platform, &awsv1.AWSCluster{})
		setupWebhooks(mgr)
	case configv1.GCPPlatformType:
		setupReconcilers(mgr, opts, infra, platform, &gcpv1.GCPCluster{})
		setupWebhooks(mgr)
	case configv1.AzurePlatformType:
		azureCloudEnvironment := getAzureCloudEnvironment(infra.Status.PlatformStatus)
		if azureCloudEnvironment == configv1.AzureStackCloud {
			klog.Infof("Detected Azure Cloud Environment %q on platform %q is not supported, skipping capi controllers setup", azureCloudEnvironment, platform)

			isUnsupportedPlatform = true
		} else {
			// The ClusterOperator Controller must run in all cases.
			setupReconcilers(mgr, opts, infra, platform, &azurev1.AzureCluster{})
			setupWebhooks(mgr)
		}
	case configv1.PowerVSPlatformType:
		setupReconcilers(mgr, opts, infra, platform, &ibmpowervsv1.IBMPowerVSCluster{})
		setupWebhooks(mgr)
	case configv1.VSpherePlatformType:
		setupReconcilers(mgr, opts, infra, platform, &vspherev1.VSphereCluster{})
		setupWebhooks(mgr)
	case configv1.OpenStackPlatformType:
		setupReconcilers(mgr, opts, infra, platform, &openstackv1.OpenStackCluster{})
		setupWebhooks(mgr)
	case configv1.BareMetalPlatformType:
		setupReconcilers(mgr, opts, infra, platform, &metal3v1.Metal3Cluster{})
		setupWebhooks(mgr)
	default:
		klog.Infof("Detected platform %q is not supported, skipping capi controllers setup", platform)

		isUnsupportedPlatform = true
	}

	// The ClusterOperator Controller must run under all circumstances as it manages the ClusterOperator object for this operator.
	setupClusterOperatorController(mgr, opts, platform, isUnsupportedPlatform)
}

func setupReconcilers(mgr manager.Manager, opts *util.CommonOptions, infra *configv1.Infrastructure, platform configv1.PlatformType, infraClusterObject client.Object) {
	if err := (&corecluster.CoreClusterController{
		ClusterOperatorStatusClient: opts.GetClusterOperatorStatusClient(mgr, platform, "cluster-resource"),
		Cluster:                     &clusterv1.Cluster{},
		Platform:                    platform,
		Infra:                       infra,
	}).SetupWithManager(mgr); err != nil {
		klog.Error(err, "unable to create controller", "controller", "CoreCluster")
		os.Exit(1)
	}

	if err := (&secretsync.UserDataSecretController{
		ClusterOperatorStatusClient: opts.GetClusterOperatorStatusClient(mgr, platform, "user-data-secret"),
		Scheme:                      mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		klog.Error(err, "unable to create user-data-secret controller", "controller", "UserDataSecret")
		os.Exit(1)
	}

	if err := (&kubeconfig.KubeconfigReconciler{
		ClusterOperatorStatusClient: opts.GetClusterOperatorStatusClient(mgr, platform, "kubeconfig"),
		Scheme:                      mgr.GetScheme(),
		RestCfg:                     mgr.GetConfig(),
	}).SetupWithManager(mgr); err != nil {
		klog.Error(err, "unable to create controller", "controller", "Kubeconfig")
		os.Exit(1)
	}

	if err := (&infracluster.InfraClusterController{
		ClusterOperatorStatusClient: opts.GetClusterOperatorStatusClient(mgr, platform, "infracluster"),
		Scheme:                      mgr.GetScheme(),
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

// getAzureCloudEnvironment returns the current AzureCloudEnvironment.
func getAzureCloudEnvironment(ps *configv1.PlatformStatus) configv1.AzureCloudEnvironment {
	if ps == nil || ps.Azure == nil {
		return ""
	}

	return ps.Azure.CloudName
}

func setupClusterOperatorController(mgr manager.Manager, opts *util.CommonOptions, platform configv1.PlatformType, isUnsupportedPlatform bool) {
	// ClusterOperator watches and keeps the cluster-api ClusterObject up to date.
	if err := (&clusteroperator.ClusterOperatorController{
		ClusterOperatorStatusClient: opts.GetClusterOperatorStatusClient(mgr, platform, "clusteroperator"),
		Scheme:                      mgr.GetScheme(),
		IsUnsupportedPlatform:       isUnsupportedPlatform,
	}).SetupWithManager(mgr); err != nil {
		klog.Error(err, "unable to create clusteroperator controller", "controller", "ClusterOperator")
		os.Exit(1)
	}
}

func getDefaultCacheOptions(capiNamespace string, sync time.Duration) cache.Options {
	return cache.Options{
		DefaultNamespaces: map[string]cache.Config{
			capiNamespace: {},
		},
		SyncPeriod: &sync,
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Secret{}: {
				Namespaces: map[string]cache.Config{
					capiNamespace:                    {},
					secretsync.SecretSourceNamespace: {},
					"kube-system":                    {}, // For fetching cloud credentials.
				},
			},
			&mapiv1.ControlPlaneMachineSet{}: {
				Namespaces: map[string]cache.Config{
					defaultMachineAPINamespace: {},
				},
			},
			&mapiv1beta1.MachineSet{}: {
				Namespaces: map[string]cache.Config{
					defaultMachineAPINamespace: {},
				},
			},
			&mapiv1beta1.Machine{}: {
				Namespaces: map[string]cache.Config{
					defaultMachineAPINamespace: {},
				},
			},
		},
	}
}
