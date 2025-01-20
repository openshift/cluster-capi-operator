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
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1alpha1 "github.com/openshift/api/machine/v1alpha1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/machinesetsync"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/machinesync"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	"github.com/openshift/api/features"
	featuregates "github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/config"
	"k8s.io/component-base/config/options"
	klog "k8s.io/klog/v2"
	"k8s.io/klog/v2/textlogger"
	capiflags "sigs.k8s.io/cluster-api/util/flags"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

var (
	// errTimedOutWaitingForFeatureGates is returned when the feature gates are not initialized within the timeout.
	errTimedOutWaitingForFeatureGates = errors.New("timed out waiting for feature gates to be initialized")

	// errPlatformNotFound is returned when there is no platform set on the infrastructure object.
	errPlatformNotFound = errors.New("no platform provider found on install config")
)

func initScheme(scheme *runtime.Scheme) {
	// TODO(joelspeed): Add additional schemes here once we work out exactly which will be needed.
	utilruntime.Must(mapiv1alpha1.Install(scheme))
	utilruntime.Must(mapiv1beta1.Install(scheme))
	utilruntime.Must(configv1.Install(scheme))
	utilruntime.Must(awsv1.AddToScheme(scheme))
	utilruntime.Must(openstackv1.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))
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
		ResourceName:      "machine-api-migration-leader",
		ResourceNamespace: "openshift-cluster-api",
	}

	healthAddr := flag.String(
		"health-addr",
		":9441",
		"The address for health checking.",
	)
	capiManagedNamespace := flag.String(
		"capi-namespace",
		controllers.DefaultManagedNamespace,
		"The namespace where CAPI components will run.",
	)
	mapiManagedNamespace := flag.String(
		"mapi-namespace",
		controllers.DefaultMAPIManagedNamespace,
		"The namespace to watch for MAPI resources.",
	)

	logToStderr := flag.Bool(
		"logtostderr",
		true,
		"log to standard error instead of files",
	)

	textLoggerConfig := textlogger.NewConfig()
	textLoggerConfig.AddFlags(flag.CommandLine)
	ctrl.SetLogger(textlogger.NewLogger(textLoggerConfig))

	capiManagerOptions := capiflags.ManagerOptions{}

	// Once all the flags are registered, switch to pflag
	// to allow leader lection flags to be bound.
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	options.BindLeaderElectionFlags(&leaderElectionConfig, pflag.CommandLine)
	capiflags.AddManagerOptions(pflag.CommandLine, &capiManagerOptions)
	pflag.Parse()

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
			*capiManagedNamespace: {},
			*mapiManagedNamespace: {},
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
	})
	if err != nil {
		klog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	// This will catch signals from the OS and shutdown the manager gracefully.
	// Set it up here as we may need to branch early if the feature gate is not enabled.
	stop := ctrl.SetupSignalHandler()

	featureGateAccessor, err := getFeatureGates(mgr)
	if err != nil {
		klog.Error(err, "unable to get feature gates")
		os.Exit(1)
	}

	currentFeatureGates, err := featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		klog.Error(err, "unable to get current feature gates")
		os.Exit(1)
	}

	if !currentFeatureGates.Enabled(features.FeatureGateMachineAPIMigration) {
		klog.Info("MachineAPIMigration feature gate is not enabled, nothing to do. Waiting for termination signal.")
		<-stop.Done()
		os.Exit(0)
	}

	infraClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		klog.Error(err, "unable to set up infra client")
		os.Exit(1)
	}

	infra := &configv1.Infrastructure{}
	if err := infraClient.Get(stop, client.ObjectKey{Name: controllers.InfrastructureResourceName}, infra); err != nil {
		klog.Errorf("failed to fetch infrastructure: %s", err)
		os.Exit(1)
	}

	provider, err := getProviderFromInfrastructure(infra)
	if err != nil {
		klog.Errorf("failed to fetch infrastructure: %s", err)
		os.Exit(1)
	}

	// Currently we only plan to support AWS, so all others are a noop until they're implemented.
	switch provider {
	case configv1.AWSPlatformType:
		klog.Info("MachineAPIMigration: starting AWS controllers")

	case configv1.OpenStackPlatformType:
		klog.Info("MachineAPIMigration: starting OpenStack controllers")

	default:
		klog.Infof("MachineAPIMigration not implemented for platform %s, nothing to do. Waiting for termination signal.", provider)
		<-stop.Done()
		os.Exit(0)
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

	machineSyncReconciler := machinesync.MachineSyncReconciler{
		Infra:    infra,
		Platform: provider,

		MAPINamespace: *mapiManagedNamespace,
		CAPINamespace: *capiManagedNamespace,
	}

	if err := machineSyncReconciler.SetupWithManager(mgr); err != nil {
		klog.Error(err, "failed to set up machine sync reconciler with manager")
		os.Exit(1)
	}

	machineSetSyncReconciler := machinesetsync.MachineSetSyncReconciler{
		Platform: provider,
		Infra:    infra,

		MAPINamespace: *mapiManagedNamespace,
		CAPINamespace: *capiManagedNamespace,
	}

	if err := machineSetSyncReconciler.SetupWithManager(mgr); err != nil {
		klog.Error(err, "failed to set up machineset sync reconciler with manager")
		os.Exit(1)
	}

	klog.Info("Starting manager")

	if err := mgr.Start(stop); err != nil {
		klog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// getFeatureGates is used to fetch the current feature gates from the cluster.
// We use this to check if the machine api migration is actually enabled or not.
func getFeatureGates(mgr ctrl.Manager) (featuregates.FeatureGateAccess, error) {
	desiredVersion := util.GetReleaseVersion()
	missingVersion := "0.0.1-snapshot"

	configClient, err := configv1client.NewForConfig(mgr.GetConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to create config client: %w", err)
	}

	configInformers := configinformers.NewSharedInformerFactory(configClient, 10*time.Minute)

	// By default, this will exit(0) if the featuregates change.
	featureGateAccessor := featuregates.NewFeatureGateAccess(
		desiredVersion, missingVersion,
		configInformers.Config().V1().ClusterVersions(),
		configInformers.Config().V1().FeatureGates(),
		events.NewLoggingEventRecorder("machineapimigration"),
	)
	go featureGateAccessor.Run(context.Background())
	go configInformers.Start(context.Background().Done())

	select {
	case <-featureGateAccessor.InitialFeatureGatesObserved():
		featureGates, _ := featureGateAccessor.CurrentFeatureGates()
		klog.Infof("FeatureGates initialized: %v", featureGates.KnownFeatures())
	case <-time.After(1 * time.Minute):
		return nil, errTimedOutWaitingForFeatureGates
	}

	return featureGateAccessor, nil
}

// getProviderFromInfrastructure returns the PlatformType from the Infrastructure object.
func getProviderFromInfrastructure(infra *configv1.Infrastructure) (configv1.PlatformType, error) {
	if infra.Status.PlatformStatus != nil && infra.Status.PlatformStatus.Type != "" {
		return infra.Status.PlatformStatus.Type, nil
	}

	return "", errPlatformNotFound
}
