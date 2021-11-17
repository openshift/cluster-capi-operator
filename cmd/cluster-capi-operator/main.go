package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/pflag"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/component-base/config"
	"k8s.io/component-base/config/options"
	"k8s.io/klog/klogr"
	"k8s.io/klog/v2"
	operatorv1 "sigs.k8s.io/cluster-api/exp/operator/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")

	leaderElectionConfig = config.LeaderElectionConfiguration{
		LeaderElect:   true,
		LeaseDuration: util.LeaseDuration,
		RenewDeadline: util.RenewDeadline,
		RetryPeriod:   util.RetryPeriod,
		ResourceName:  "cluster-capi-operator-leader",
	}
)

const (
	defaultImagesLocation         = "/etc/cluster-api-config/images.json"
	releaseVersionEnvVariableName = "RELEASE_VERSION"
	unknownVersionValue           = "unknown"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(operatorv1.AddToScheme(scheme))

	// +kubebuilder:scaffold:scheme
}

func main() {
	flag.Set("logtostderr", "true") //nolint:errcheck
	klog.InitFlags(nil)

	metricsAddr := flag.String(
		"metrics-bind-address",
		":8080",
		"Address for hosting metrics",
	)

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

	// Once all the flags are regitered, switch to pflag
	// to allow leader lection flags to be bound
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	options.BindLeaderElectionFlags(&leaderElectionConfig, pflag.CommandLine)
	pflag.Parse()

	ctrl.SetLogger(klogr.New().WithName("ClusterAPIOperator"))

	syncPeriod := 10 * time.Minute
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Namespace:               *managedNamespace,
		Scheme:                  scheme,
		SyncPeriod:              &syncPeriod,
		MetricsBindAddress:      *metricsAddr,
		Port:                    9443,
		HealthProbeBindAddress:  *healthAddr,
		LeaderElectionNamespace: leaderElectionConfig.ResourceNamespace,
		LeaderElection:          leaderElectionConfig.LeaderElect,
		LeaseDuration:           &leaderElectionConfig.LeaseDuration.Duration,
		LeaderElectionID:        leaderElectionConfig.ResourceName,
		RetryPeriod:             &leaderElectionConfig.RetryPeriod.Duration,
		RenewDeadline:           &leaderElectionConfig.RenewDeadline.Duration,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	jsonData, err := ioutil.ReadFile(filepath.Clean(*imagesFile))
	if err != nil {
		setupLog.Error(err, "unable to read file", "name", *imagesFile)
		os.Exit(1)
	}
	containerImages := map[string]string{}
	if err := json.Unmarshal(jsonData, &containerImages); err != nil {
		setupLog.Error(err, "unable to unmarshal image names from file", "name", *imagesFile)
		os.Exit(1)
	}

	if err = (&controllers.ClusterOperatorReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		Recorder:         mgr.GetEventRecorderFor("cluster-capi-operator"),
		ReleaseVersion:   getReleaseVersion(),
		ManagedNamespace: *managedNamespace,
		Images:           containerImages,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterOperator")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("health", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("check", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
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
