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
	klog "k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"

	"github.com/openshift/cluster-capi-operator/pkg/commoncmdoptions"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/capiinstaller"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/clusteroperator"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/revision"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const (
	managerName = "cluster-capi-installer"

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

	opts := commoncmdoptions.InitCommonOptions(managerName, controllers.DefaultOperatorNamespace)

	imagesFile := flag.String(
		"images-json",
		defaultImagesLocation,
		"The location of images file to use by operator for managed CAPI binaries.",
	)

	opts.Parse()

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

	cfg := ctrl.GetConfigOrDie()
	ctx := ctrl.SetupSignalHandler()

	mgr, err := ctrl.NewManager(cfg, mgrOpts)
	if err != nil {
		klog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	if err := commoncmdoptions.AddCommonChecks(mgr); err != nil {
		klog.Error(err, "unable to add common checks")
		os.Exit(1)
	}

	if err := setupControllers(ctx, mgr, opts, *imagesFile); err != nil {
		klog.Error(err, "unable to setup controllers")
		os.Exit(1)
	}

	klog.Info("Starting cluster-capi-installer manager")

	if err := mgr.Start(ctx); err != nil {
		klog.Error(err)
		os.Exit(1)
	}
}

func setupControllers(ctx context.Context, mgr ctrl.Manager, opts *commoncmdoptions.CommonOptions, imagesFile string) error {
	infra, err := util.GetInfra(ctx, mgr.GetAPIReader())
	if err != nil {
		return fmt.Errorf("unable to get infrastructure: %w", err)
	}

	isUnsupportedPlatform := false

	_, platform, err := util.GetCAPITypesForInfrastructure(infra)
	if err != nil {
		if errors.Is(err, util.ErrUnsupportedPlatform) {
			isUnsupportedPlatform = true
		} else {
			return fmt.Errorf("unable to get infrastructure types: %w", err)
		}
	}

	containerImages, providerProfiles, err := loadProviderImages(ctx, mgr, imagesFile)
	if err != nil {
		return err
	}

	if err := setupCapiInstallerController(mgr, opts, platform, containerImages, providerProfiles); err != nil {
		return err
	}

	if err := (&clusteroperator.ClusterOperatorController{
		ClusterOperatorStatusClient: opts.GetClusterOperatorStatusClient(mgr, platform, "clusteroperator"),
		Scheme:                      mgr.GetScheme(),
		IsUnsupportedPlatform:       isUnsupportedPlatform,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create clusteroperator controller: %w", err)
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

func setupCapiInstallerController(mgr ctrl.Manager, opts *commoncmdoptions.CommonOptions, platform configv1.PlatformType, containerImages map[string]string, providerProfiles []providerimages.ProviderImageManifests) error {
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

	if err := (&revision.RevisionController{
		Client:           mgr.GetClient(),
		ProviderProfiles: providerProfiles,
		ReleaseVersion:   util.GetReleaseVersion(),
	}).SetupWithManager(mgr); err != nil {
		klog.Error(err, "unable to create revision controller", "controller", "RevisionController")
		return fmt.Errorf("unable to create revision controller: %w", err)
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
//
// XXX: This function is unrelated to feature gates. It sets a single
// environment variable which applies only to the AWS provider. It is replaced
// by logic in revisiongenerator, and can be removed when the capiinstaller
// controller is removed.
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
