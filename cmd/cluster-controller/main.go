package main

import (
	"flag"
	"os"
	"time"

	"github.com/spf13/pflag"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/component-base/config"
	"k8s.io/component-base/config/options"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/klogr"
	operatorv1 "sigs.k8s.io/cluster-api-operator/api/v1alpha1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/cluster"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

var (
	scheme               = runtime.NewScheme()
	leaderElectionConfig = config.LeaderElectionConfiguration{
		LeaderElect:       true,
		LeaseDuration:     util.LeaseDuration,
		RenewDeadline:     util.RenewDeadline,
		RetryPeriod:       util.RetryPeriod,
		ResourceName:      "cluster-controller-leader",
		ResourceNamespace: "openshift-cluster-api",
	}
)

const (
	defaultProvidersLocation      = "./providers-list.yaml"
	releaseVersionEnvVariableName = "RELEASE_VERSION"
	unknownVersionValue           = "unknown"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(operatorv1.AddToScheme(scheme))
	utilruntime.Must(awsv1.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))

	// +kubebuilder:scaffold:scheme
}

func main() {
	klog.InitFlags(nil)

	ctrl.SetLogger(klogr.New())
	providerFile := flag.String(
		"providers-yaml",
		defaultProvidersLocation,
		"The location of supported providers for CAPI",
	)

	// Once all the flags are regitered, switch to pflag
	// to allow leader lection flags to be bound
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	options.BindLeaderElectionFlags(&leaderElectionConfig, pflag.CommandLine)
	pflag.Parse()

	ctrl.SetLogger(klogr.New().WithName("CAPIClusterController"))

	syncPeriod := 10 * time.Minute
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                  scheme,
		SyncPeriod:              &syncPeriod,
		MetricsBindAddress:      "0", // we do not expose any metric at this point
		Port:                    9442,
		LeaderElectionNamespace: leaderElectionConfig.ResourceNamespace,
		LeaderElection:          leaderElectionConfig.LeaderElect,
		LeaseDuration:           &leaderElectionConfig.LeaseDuration.Duration,
		LeaderElectionID:        leaderElectionConfig.ResourceName,
		RetryPeriod:             &leaderElectionConfig.RetryPeriod.Duration,
		RenewDeadline:           &leaderElectionConfig.RenewDeadline.Duration,
	})
	if err != nil {
		klog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	supportedProviders, err := util.ReadProvidersFile(*providerFile)
	if err != nil {
		klog.Error(err, "unable to get providers from file", "name", *providerFile)
		os.Exit(1)
	}

	if err = (&cluster.ClusterReconciler{
		ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{
			Client:         mgr.GetClient(),
			Recorder:       mgr.GetEventRecorderFor("cluster-capi-operator-cluster-controller"),
			ReleaseVersion: getReleaseVersion(),
		},
		Scheme:             mgr.GetScheme(),
		SupportedPlatforms: supportedProviders,
	}).SetupWithManager(mgr); err != nil {
		klog.Error(err, "unable to create controller", "controller", "ClusterOperator")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("health", healthz.Ping); err != nil {
		klog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("check", healthz.Ping); err != nil {
		klog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	klog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		klog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func getReleaseVersion() string {
	releaseVersion := os.Getenv(releaseVersionEnvVariableName)
	if len(releaseVersion) == 0 {
		releaseVersion = unknownVersionValue
		klog.Infof("%s environment variable is missing, defaulting to %q", releaseVersionEnvVariableName, unknownVersionValue)
	}
	return releaseVersion
}
