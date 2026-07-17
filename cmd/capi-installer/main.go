// Copyright 2026 Red Hat, Inc.
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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"

	"github.com/openshift/cluster-capi-operator/pkg/commoncmdoptions"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/installer"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/revision"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"github.com/openshift/cluster-capi-operator/pkg/runtimetransformer"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

var errPodIdentityNotSet = errors.New("POD_NAME and POD_NAMESPACE must be set")

const (
	managerName = "capi-installer"
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
	providerImageDir := extraflags.String(
		"provider-image-dir",
		providerimages.ProviderImageMountBase,
		"Directory containing provider image manifests. In dev mode, set to a local directory to skip pod spec reading.",
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
		SyncPeriod: ptr.To(12 * time.Hour),
	}

	mgr, err := initManager(ctx, cancel, mgrOpts)
	if err != nil {
		log.Error(err, "unable to initialize manager")
		os.Exit(1)
	}

	if err := setupControllers(ctx, mgr, operatorConfig, *providerImageDir); err != nil {
		log.Error(err, "unable to setup controllers")
		os.Exit(1)
	}

	log.Info("Starting " + managerName + " manager")

	if err := mgr.Start(ctx); err != nil {
		log.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func setupControllers(ctx context.Context, mgr ctrl.Manager, operatorConfig commoncmdoptions.OperatorConfig, providerImageDir string) error {
	allProviderProfiles, err := loadProviderImages(ctx, mgr, providerImageDir)
	if err != nil {
		return err
	}

	currentReleaseRefs, err := loadCurrentReleaseImageRefs(ctx, mgr, *operatorConfig.OperatorNamespace)
	if err != nil {
		return err
	}

	currentReleaseProfiles := make([]providerimages.ProviderImageManifests, 0, len(allProviderProfiles))
	for _, profile := range allProviderProfiles {
		if currentReleaseRefs.Has(profile.ImageRef) {
			currentReleaseProfiles = append(currentReleaseProfiles, profile)
		}
	}

	log := ctrl.LoggerFrom(ctx)
	for _, profile := range allProviderProfiles {
		log.Info("loaded provider profile", "name", profile.Name, "imageRef", profile.ImageRef, "profile", profile.Profile)
	}

	transformers := []runtimetransformer.RuntimeTransformer{
		&runtimetransformer.AdoptExistingTransformer{},
	}

	if err := (&revision.RevisionController{
		Client:           mgr.GetClient(),
		ProviderProfiles: currentReleaseProfiles,
		ReleaseVersion:   util.GetReleaseVersion(),
		Transformers:     transformers,
	}).SetupWithManager(mgr, operatorConfig.TLSOptions); err != nil {
		log.Error(err, "unable to create revision controller", "controller", "RevisionController")
		return fmt.Errorf("unable to create revision controller: %w", err)
	}

	if err := installer.SetupWithManager(mgr, allProviderProfiles, transformers); err != nil {
		return fmt.Errorf("unable to create installer controller: %w", err)
	}

	return nil
}

func loadProviderImages(ctx context.Context, mgr ctrl.Manager, providerImageDir string) ([]providerimages.ProviderImageManifests, error) {
	podName := os.Getenv("POD_NAME")
	podNamespace := os.Getenv("POD_NAMESPACE")

	if podName == "" || podNamespace == "" {
		return nil, errPodIdentityNotSet
	}

	var pod corev1.Pod
	if err := mgr.GetAPIReader().Get(ctx, types.NamespacedName{Name: podName, Namespace: podNamespace}, &pod); err != nil {
		return nil, fmt.Errorf("unable to get pod %s/%s: %w", podNamespace, podName, err)
	}

	imageRefMap, err := providerimages.BuildImageRefMap(pod.Spec, managerName)
	if err != nil {
		return nil, fmt.Errorf("unable to build image ref map from pod spec: %w", err)
	}

	log := ctrl.LoggerFrom(ctx)

	providerProfiles, err := providerimages.ScanProviderImages(log, providerImageDir, imageRefMap)
	if err != nil {
		return nil, fmt.Errorf("unable to scan provider images: %w", err)
	}

	return providerProfiles, nil
}

func loadCurrentReleaseImageRefs(ctx context.Context, mgr ctrl.Manager, operatorNamespace string) (sets.Set[string], error) {
	configMap := &corev1.ConfigMap{}

	if err := mgr.GetAPIReader().Get(ctx, types.NamespacedName{
		Name:      providerimages.ConfigMapName,
		Namespace: operatorNamespace,
	}, configMap); err != nil {
		return nil, fmt.Errorf("unable to get ConfigMap %s/%s: %w", operatorNamespace, providerimages.ConfigMapName, err)
	}

	imageRefs, err := providerimages.ImageRefsFromConfigMap(configMap)
	if err != nil {
		return nil, fmt.Errorf("unable to extract image refs from ConfigMap: %w", err)
	}

	return imageRefs, nil
}
