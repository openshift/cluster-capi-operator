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
	"maps"
	"os"
	"slices"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"

	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/capiinstaller"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/clusteroperator"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const (
	managerName = "capi-operator"

	defaultImagesLocation       = "./dev-images.json"
	providerImageDirEnvVar      = "PROVIDER_IMAGE_DIR"
	defaultProviderImageDirPath = "/var/lib/provider-images"
)

func initScheme(scheme *runtime.Scheme) {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(operatorv1alpha1.AddToScheme(scheme))
}

func main() {
	scheme := runtime.NewScheme()
	initScheme(scheme)

	opts := util.InitCommonOptions(managerName, controllers.DefaultOperatorNamespace)

	imagesFile := flag.String(
		"images-json",
		defaultImagesLocation,
		"The location of images file to use by operator for managed CAPI binaries.",
	)

	opts.Parse()

	log := ctrl.Log.WithName("capi-operator")

	cacheOpts := cache.Options{
		DefaultNamespaces: map[string]cache.Config{
			*opts.CAPINamespace:     {},
			*opts.OperatorNamespace: {},
		},
		SyncPeriod: ptr.To(10 * time.Minute),
	}

	mgrOpts, _ := opts.GetCommonManagerOptions()
	mgrOpts.Cache = cacheOpts
	mgrOpts.Scheme = scheme
	mgrOpts.Logger = log

	cfg := ctrl.GetConfigOrDie()
	ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())

	mgr, err := ctrl.NewManager(cfg, mgrOpts)
	if err != nil {
		log.Error(err, "unable to create manager")
		os.Exit(1)
	}

	if err := util.AddCommonChecks(mgr); err != nil {
		log.Error(err, "unable to add common checks")
		os.Exit(1)
	}

	if err := setupControllers(ctx, log, mgr, opts, *imagesFile, cancel); err != nil {
		log.Error(err, "unable to setup controllers")
		os.Exit(1)
	}

	log.Info("Starting " + managerName + " manager")

	if err := mgr.Start(ctx); err != nil {
		log.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func setupControllers(ctx context.Context, log logr.Logger, mgr ctrl.Manager, opts *util.CommonOptions, imagesFile string, cancel context.CancelFunc) error {
	infra, err := util.GetInfra(ctx, mgr.GetAPIReader())
	if err != nil {
		return fmt.Errorf("unable to get infrastructure: %w", err)
	}

	platform, err := util.GetPlatformFromInfra(infra)
	if err != nil {
		return fmt.Errorf("unable to get platform: %w", err)
	}

	featureGates, err := util.GetFeatureGates(ctx, log, managerName, mgr.GetConfig(), cancel)
	if err != nil {
		return fmt.Errorf("unable to get feature gates: %w", err)
	}

	supportedPlatform := util.IsCAPIEnabledForPlatform(featureGates, infra.Status.PlatformStatus.Type)

	if err := (&clusteroperator.ClusterOperatorController{
		ClusterOperatorStatusClient: opts.GetClusterOperatorStatusClient(mgr, platform, "clusteroperator"),
		Scheme:                      mgr.GetScheme(),
		IsUnsupportedPlatform:       !supportedPlatform,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create clusteroperator controller: %w", err)
	}

	// The ClusterOperatorController MUST run if we were installed, otherwise
	// our ClusterOperator will not be reconciled and installation will not
	// progress. We don't run any other controllers if the current platform is
	// not supported.
	if !supportedPlatform {
		return nil
	}

	containerImages, providerProfiles, err := loadProviderImages(ctx, mgr, imagesFile)
	if err != nil {
		return err
	}

	if err := setupCapiInstallerController(mgr, opts, platform, containerImages, providerProfiles); err != nil {
		return err
	}

	return nil
}

func loadProviderImages(ctx context.Context, mgr ctrl.Manager, imagesFile string) (map[string]string, []providerimages.ProviderImageManifests, error) {
	containerImages, err := util.ReadImagesFile(imagesFile)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to get images from file: %w", err)
	}

	providerImageDir := os.Getenv(providerImageDirEnvVar)
	if providerImageDir == "" {
		providerImageDir = defaultProviderImageDirPath
	}

	containerImageRefs := slices.Collect(maps.Values(containerImages))

	providerProfiles, err := providerimages.ReadProviderImages(ctx, mgr.GetAPIReader(), mgr.GetLogger(), containerImageRefs, providerImageDir)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to get provider image metadata: %w", err)
	}

	return containerImages, providerProfiles, nil
}

func setupCapiInstallerController(mgr ctrl.Manager, opts *util.CommonOptions, platform configv1.PlatformType, containerImages map[string]string, providerProfiles []providerimages.ProviderImageManifests) error {
	applyClient, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		return fmt.Errorf("unable to set up apply client: %w", err)
	}

	apiextensionsClient, err := apiextensionsclient.NewForConfig(mgr.GetConfig())
	if err != nil {
		return fmt.Errorf("unable to set up api extensions client: %w", err)
	}

	if err := setFeatureGatesEnvVars(); err != nil {
		return fmt.Errorf("unable to set feature gates environment variables: %w", err)
	}

	if err := (&capiinstaller.CapiInstallerController{
		ClusterOperatorStatusClient: opts.GetClusterOperatorStatusClient(mgr, platform, "installer"),
		Scheme:                      mgr.GetScheme(),
		Images:                      containerImages,
		ProviderImages:              providerProfiles,
		RestCfg:                     mgr.GetConfig(),
		Platform:                    platform,
		ApplyClient:                 applyClient,
		APIExtensionsClient:         apiextensionsClient,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create capi installer controller: %w", err)
	}

	return nil
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
