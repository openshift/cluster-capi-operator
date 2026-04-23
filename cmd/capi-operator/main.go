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
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"

	"github.com/openshift/cluster-capi-operator/pkg/commoncmdoptions"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/clusteroperator"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/installer"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/revision"
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
	ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())
	cfg := ctrl.GetConfigOrDie()

	scheme := runtime.NewScheme()
	initScheme(scheme)

	extraflags := flag.NewFlagSet("", flag.ContinueOnError)
	imagesFile := extraflags.String(
		"images-json",
		defaultImagesLocation,
		"The location of images file to use by operator for managed CAPI binaries.",
	)

	log, operatorConfig, mgrOpts, initManager, err := commoncmdoptions.InitOperatorConfig(ctx, cfg, scheme, managerName, controllers.DefaultOperatorNamespace, extraflags)
	if err != nil {
		log.Error(err, "unable to initialize operator config")
		os.Exit(1)
	}

	mgrOpts.Cache = cache.Options{
		DefaultNamespaces: map[string]cache.Config{
			*operatorConfig.CAPINamespace:     {},
			*operatorConfig.OperatorNamespace: {},
		},
		SyncPeriod: ptr.To(10 * time.Minute),
	}

	mgr, err := initManager(ctx, cancel, mgrOpts)
	if err != nil {
		log.Error(err, "unable to initialize manager")
		os.Exit(1)
	}

	if err := setupControllers(ctx, log, mgr, operatorConfig, *imagesFile, cancel); err != nil {
		log.Error(err, "unable to setup controllers")
		os.Exit(1)
	}

	log.Info("Starting " + managerName + " manager")

	if err := mgr.Start(ctx); err != nil {
		log.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func setupControllers(ctx context.Context, log logr.Logger, mgr ctrl.Manager, operatorConfig commoncmdoptions.OperatorConfig, imagesFile string, cancel context.CancelFunc) error {
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
		ClusterOperatorStatusClient: operatorConfig.GetClusterOperatorStatusClient(mgr, platform, "clusteroperator"),
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

	providerProfiles, err := loadProviderImages(ctx, mgr, imagesFile)
	if err != nil {
		return err
	}

	if err := (&revision.RevisionController{
		Client:           mgr.GetClient(),
		ProviderProfiles: providerProfiles,
		ReleaseVersion:   util.GetReleaseVersion(),
	}).SetupWithManager(mgr, operatorConfig.TLSOptions); err != nil {
		log.Error(err, "unable to create revision controller", "controller", "RevisionController")
		return fmt.Errorf("unable to create revision controller: %w", err)
	}

	if err := installer.SetupWithManager(mgr, providerProfiles); err != nil {
		return fmt.Errorf("unable to create installer controller: %w", err)
	}

	return nil
}

func loadProviderImages(ctx context.Context, mgr ctrl.Manager, imagesFile string) ([]providerimages.ProviderImageManifests, error) {
	containerImages, err := util.ReadImagesFile(imagesFile)
	if err != nil {
		return nil, fmt.Errorf("unable to get images from file: %w", err)
	}

	providerImageDir := os.Getenv(providerImageDirEnvVar)
	if providerImageDir == "" {
		providerImageDir = defaultProviderImageDirPath
	}

	containerImageRefs := slices.Collect(maps.Values(containerImages))

	providerProfiles, err := providerimages.ReadProviderImages(ctx, mgr.GetAPIReader(), mgr.GetLogger(), containerImageRefs, providerImageDir)
	if err != nil {
		return nil, fmt.Errorf("unable to get provider image metadata: %w", err)
	}

	return providerProfiles, nil
}
