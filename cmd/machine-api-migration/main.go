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

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1alpha1 "github.com/openshift/api/machine/v1alpha1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	"github.com/openshift/api/features"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/commoncmdoptions"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/machinemigration"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/machinesetmigration"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/machinesetsync"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/machinesync"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const (
	managerName = "machine-api-migration"
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

func main() {
	cfg := ctrl.GetConfigOrDie()
	ctx := ctrl.SetupSignalHandler()
	scheme := runtime.NewScheme()
	initScheme(scheme)

	log, operatorConfig, mgrOpts, initManager, err := commoncmdoptions.InitOperatorConfig(ctx, cfg, scheme, managerName, controllers.DefaultCAPINamespace, nil)
	if err != nil {
		log.Error(err, "unable to initialize operator config")
		os.Exit(1)
	}

	mgrOpts.Cache = getDefaultCacheOptions(*operatorConfig.CAPINamespace, *operatorConfig.MAPINamespace, 10*time.Minute)

	ctx, cancel := context.WithCancel(ctx)

	mgr, err := initManager(ctx, cancel, mgrOpts)
	if err != nil {
		log.Error(err, "unable to initialize manager")
		os.Exit(1)
	}

	// Get controllers to add to the manager.
	//
	// This will return an empty map if the platform does not support
	// MachineAPIMigration. In this case we still run the manager, but without
	// any controllers. This will shut down cleanly when the pod is killed, but
	// otherwise do nothing.
	controllers, err := getControllers(ctx, log, mgr, operatorConfig, cancel)
	if err != nil {
		log.Error(err, "unable to get controllers")
		os.Exit(1)
	}

	for name, controller := range controllers {
		if err := controller.SetupWithManager(mgr); err != nil {
			log.Error(err, "failed to set up reconciler with manager", "reconciler", name)
			os.Exit(1)
		}
	}

	log.Info("Starting manager")

	if err := mgr.Start(ctx); err != nil {
		log.Error(err, "problem running manager")
		os.Exit(1)
	}
}

type controller interface {
	SetupWithManager(mgr ctrl.Manager) error
}

func getControllers(ctx context.Context, log logr.Logger, mgr ctrl.Manager, opts commoncmdoptions.OperatorConfig, cancel context.CancelFunc) (map[string]controller, error) {
	featureGates, err := util.GetFeatureGates(ctx, log, managerName, mgr.GetConfig(), cancel)
	if err != nil {
		return nil, fmt.Errorf("unable to get feature gates: %w", err)
	}

	if !featureGates.Enabled(features.FeatureGateMachineAPIMigration) {
		log.Info("MachineAPIMigration feature gate is not enabled, nothing to do. Waiting for termination signal.")
		return map[string]controller{}, nil
	}

	infra, err := util.GetInfra(ctx, mgr.GetAPIReader())
	if err != nil {
		return nil, fmt.Errorf("unable to get infrastructure: %w", err)
	}

	platform, err := util.GetPlatformFromInfra(infra)
	if err != nil {
		return nil, fmt.Errorf("unable to get platform from infrastructure: %w", err)
	}

	if !util.IsMAPIMigrationEnabledForPlatform(platform, featureGates) {
		log.Info("MachineAPIMigration not implemented for platform, nothing to do. Waiting for termination signal.", "platform", platform)
		return map[string]controller{}, nil
	}

	infraTypes, err := util.GetCAPITypesForInfrastructure(infra)
	if err != nil {
		if errors.Is(err, util.ErrUnsupportedPlatform) {
			log.Info("MachineAPIMigration not implemented for platform, nothing to do. Waiting for termination signal.", "platform", platform)
			return map[string]controller{}, nil
		}

		return nil, fmt.Errorf("unable to get infrastructure types: %w", err)
	}

	return allControllers(infra, platform, infraTypes, opts), nil
}

func allControllers(infra *configv1.Infrastructure, platform configv1.PlatformType, infraTypes util.InfraTypes, opts commoncmdoptions.OperatorConfig) map[string]controller {
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
