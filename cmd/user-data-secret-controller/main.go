/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"os"
	"time"

	"github.com/spf13/pflag"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/component-base/config"
	"k8s.io/component-base/config/options"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	// +kubebuilder:scaffold:scheme
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")

	leaderElectionConfig = config.LeaderElectionConfiguration{
		LeaderElect:  true,
		ResourceName: "user-data-secret-leader",
	}
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1.AddToScheme(scheme))

	// +kubebuilder:scaffold:scheme
}

func main() {
	klog.InitFlags(nil)
	setupLog.Info("Starting secret sync controller")

	managedNamespace := flag.String(
		"namespace",
		controllers.DefaultManagedNamespace,
		"The namespace for managed objects, target worker-user-data in particular.",
	)

	// Once all the flags are registered, switch to pflag
	// to allow leader lection flags to be bound
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	options.BindLeaderElectionFlags(&leaderElectionConfig, pflag.CommandLine)
	pflag.Parse()

	restConfig := ctrl.GetConfigOrDie()

	syncPeriod := 10 * time.Minute
	cacheBuilder := cache.MultiNamespacedCacheBuilder([]string{
		*managedNamespace, controllers.SecretSourceNamespace,
	})
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Namespace:               *managedNamespace,
		Scheme:                  scheme,
		SyncPeriod:              &syncPeriod,
		MetricsBindAddress:      "0", // we do not expose any metric at this point
		LeaderElectionNamespace: leaderElectionConfig.ResourceNamespace,
		LeaderElection:          leaderElectionConfig.LeaderElect,
		LeaderElectionID:        leaderElectionConfig.ResourceName,
		LeaseDuration:           &leaderElectionConfig.LeaseDuration.Duration,
		RetryPeriod:             &leaderElectionConfig.RetryPeriod.Duration,
		RenewDeadline:           &leaderElectionConfig.RenewDeadline.Duration,
		NewCache:                cacheBuilder,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controllers.UserDataSecretController{
		ClusterOperatorStatusClient: controllers.ClusterOperatorStatusClient{
			Client:           mgr.GetClient(),
			Recorder:         mgr.GetEventRecorderFor("cluster-capi-operator-user-data-secret-controller"),
			ManagedNamespace: *managedNamespace,
		},
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create user-data-secret controller", "controller", "ClusterOperator")
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
