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
	"fmt"
	"os"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1alpha1 "github.com/openshift/api/machine/v1alpha1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/machinemigration"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/machinesetmigration"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/machinesetsync"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/machinesync"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	"k8s.io/utils/clock"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	"github.com/openshift/api/features"
	featuregates "github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	klog "k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	managerName = "machine-api-migration"
)

var (
	// errTimedOutWaitingForFeatureGates is returned when the feature gates are not initialized within the timeout.
	errTimedOutWaitingForFeatureGates = errors.New("timed out waiting for feature gates to be initialized")
)

func initScheme(scheme *runtime.Scheme) {
	// TODO(joelspeed): Add additional schemes here once we work out exactly which will be needed.
	utilruntime.Must(mapiv1alpha1.Install(scheme))
	utilruntime.Must(mapiv1beta1.Install(scheme))
	utilruntime.Must(configv1.Install(scheme))
	utilruntime.Must(awsv1.AddToScheme(scheme))
	utilruntime.Must(openstackv1.AddToScheme(scheme))
	utilruntime.Must(vspherev1.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))
}

func main() {
	scheme := runtime.NewScheme()
	initScheme(scheme)

	opts := util.InitCommonOptions(managerName, controllers.DefaultCAPINamespace)

	opts.Parse()

	cfg := ctrl.GetConfigOrDie()

	mgrOpts, _ := opts.GetCommonManagerOptions()
	mgrOpts.Scheme = scheme
	mgrOpts.Cache = getDefaultCacheOptions(*opts.CAPINamespace, *opts.MAPINamespace, 10*time.Minute)

	mgr, err := ctrl.NewManager(cfg, mgrOpts)
	if err != nil {
		klog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	if err := util.AddCommonChecks(mgr); err != nil {
		klog.Error(err, "unable to add common checks")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()
	featureGateAccessor := checkFeatureGates(ctx, mgr)

	infra, err := util.GetInfra(ctx, mgr.GetAPIReader())
	if err != nil {
		klog.Error(err, "unable to get infrastructure")
		os.Exit(1)
	}

	infraTypes, platform, err := util.GetCAPITypesForInfrastructure(infra)
	if err != nil {
		if errors.Is(err, util.ErrUnsupportedPlatform) {
			klog.Info(fmt.Sprintf("MachineAPIMigration not implemented for platform %s, nothing to do. Waiting for termination signal.", platform))
			exitAfterTerminationSignal(ctx)
		}

		klog.Error(err, "unable to get infrastructure types")
		os.Exit(1)
	}

	checkPlatformSupported(ctx, platform, featureGateAccessor)

	for name, controller := range getControllers(opts, platform, infra, infraTypes) {
		if err := controller.SetupWithManager(mgr); err != nil {
			klog.Error(err, fmt.Sprintf("failed to set up %s reconciler with manager", name))
			os.Exit(1)
		}
	}

	klog.Info("Starting manager")

	if err := mgr.Start(ctx); err != nil {
		klog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func checkFeatureGates(ctx context.Context, mgr ctrl.Manager) featuregates.FeatureGateAccess {
	featureGateAccessor, err := getFeatureGates(ctx, mgr)
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
		exitAfterTerminationSignal(ctx)
	}

	return featureGateAccessor
}

func checkPlatformSupported(ctx context.Context, platform configv1.PlatformType, featureGateAccessor featuregates.FeatureGateAccess) {
	switch platform {
	case configv1.AWSPlatformType, configv1.OpenStackPlatformType:
		klog.Infof("MachineAPIMigration: starting %s controllers", platform)
	case configv1.VSpherePlatformType:
		currentFeatureGates, err := featureGateAccessor.CurrentFeatureGates()
		if err != nil {
			klog.Error(err, "unable to get current feature gates")
			os.Exit(1)
		}

		if !currentFeatureGates.Enabled(features.FeatureGateClusterAPIMachineManagementVSphere) {
			klog.Info("ClusterAPIMachineManagementVSphere feature gate is not enabled for vSphere platform. Waiting for termination signal.")
			exitAfterTerminationSignal(ctx)
		}

		klog.Infof("MachineAPIMigration: starting %s controllers", platform)
	default:
		klog.Infof("MachineAPIMigration not implemented for platform %s, nothing to do. Waiting for termination signal.", platform)
		exitAfterTerminationSignal(ctx)
	}
}

type controller interface {
	SetupWithManager(mgr ctrl.Manager) error
}

func getControllers(opts *util.CommonOptions, platform configv1.PlatformType, infra *configv1.Infrastructure, infraTypes util.InfraTypes) map[string]controller {
	return map[string]controller{
		"machine sync": &machinesync.MachineSyncReconciler{
			Infra:      infra,
			Platform:   platform,
			InfraTypes: infraTypes,

			MAPINamespace: *opts.MAPINamespace,
			CAPINamespace: *opts.CAPINamespace,
		},
		"machineset sync": &machinesetsync.MachineSetSyncReconciler{
			Platform:   platform,
			Infra:      infra,
			InfraTypes: infraTypes,

			MAPINamespace: *opts.MAPINamespace,
			CAPINamespace: *opts.CAPINamespace,
		},
		"machine migration": &machinemigration.MachineMigrationReconciler{
			Platform:   platform,
			Infra:      infra,
			InfraTypes: infraTypes,

			MAPINamespace: *opts.MAPINamespace,
			CAPINamespace: *opts.CAPINamespace,
		},
		"machineset migration": &machinesetmigration.MachineSetMigrationReconciler{
			Platform:   platform,
			Infra:      infra,
			InfraTypes: infraTypes,

			MAPINamespace: *opts.MAPINamespace,
			CAPINamespace: *opts.CAPINamespace,
		},
	}
}

// exitAfterTerminationSignal waits until our process receives a termination
// signal, then exits without an error.
// It is used when we are running on a cluster where this manager is not
// required. We don't want to exit immediately because we would be restarted,
// which is not required.
func exitAfterTerminationSignal(ctx context.Context) {
	<-ctx.Done()
	os.Exit(0)
}

// getFeatureGates is used to fetch the current feature gates from the cluster.
// We use this to check if the machine api migration is actually enabled or not.
func getFeatureGates(ctx context.Context, mgr ctrl.Manager) (featuregates.FeatureGateAccess, error) {
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
		events.NewLoggingEventRecorder("machineapimigration", clock.RealClock{}),
	)
	go featureGateAccessor.Run(ctx)
	go configInformers.Start(ctx.Done())

	select {
	case <-featureGateAccessor.InitialFeatureGatesObserved():
		featureGates, _ := featureGateAccessor.CurrentFeatureGates()
		klog.Infof("FeatureGates initialized: %v", featureGates.KnownFeatures())
	case <-time.After(1 * time.Minute):
		return nil, errTimedOutWaitingForFeatureGates
	}

	return featureGateAccessor, nil
}

func getDefaultCacheOptions(capiNamespace, mapiNamespace string, sync time.Duration) cache.Options {
	return cache.Options{
		DefaultNamespaces: map[string]cache.Config{
			capiNamespace: {},
		},
		SyncPeriod: &sync,
		ByObject: map[client.Object]cache.ByObject{
			&mapiv1beta1.Machine{}: {
				Namespaces: map[string]cache.Config{
					mapiNamespace: {},
				},
			},
			&mapiv1beta1.MachineSet{}: {
				Namespaces: map[string]cache.Config{
					mapiNamespace: {},
				},
			},
		},
	}
}
