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
	"fmt"
	"os"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/commoncmdoptions"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibilityoperator"
)

func initScheme(scheme *runtime.Scheme) {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(policyv1.AddToScheme(scheme))
	utilruntime.Must(configv1.Install(scheme))
}

const (
	managerName = "compatibility-requirements-operator"
)

var (
	errPodIdentityNotSet = errors.New("POD_NAME and POD_NAMESPACE must be set")
	errContainerNotInPod = errors.New("container not found in pod spec")
)

func main() {
	ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())
	cfg := ctrl.GetConfigOrDie()

	scheme := runtime.NewScheme()
	initScheme(scheme)

	log, _, mgrOpts, initManager, err := commoncmdoptions.InitOperatorConfig(ctx, cfg, scheme, managerName, controllers.DefaultCRDCompatibilityCheckerNamespace, nil)
	if err != nil {
		log.Error(err, "unable to initialize operator config")
		os.Exit(1)
	}

	// Limit cache to operator namespace only
	mgrOpts.Cache = cache.Options{
		DefaultNamespaces: map[string]cache.Config{
			controllers.DefaultCRDCompatibilityCheckerNamespace: {},
		},
		SyncPeriod: ptr.To(10 * time.Minute),
	}

	mgr, err := initManager(ctx, cancel, mgrOpts)
	if err != nil {
		log.Error(err, "unable to initialize manager")
		os.Exit(1)
	}

	// Get container image from own pod spec
	containerImage, err := getContainerImage(ctx, mgr.GetAPIReader())
	if err != nil {
		log.Error(err, "unable to get container image")
		os.Exit(1)
	}

	if err := setupController(mgr, containerImage); err != nil {
		log.Error(err, "unable to setup controller")
		os.Exit(1)
	}

	log.Info("Starting CRD Compatibility Checker Operator")

	if err := mgr.Start(ctx); err != nil {
		log.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func setupController(mgr ctrl.Manager, operandImage string) error {
	controller := crdcompatibilityoperator.NewCRDCompatibilityOperatorController(
		mgr.GetClient(),
		mgr.GetScheme(),
		controllers.DefaultCRDCompatibilityCheckerNamespace,
		operandImage,
	)

	if err := controller.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller %s: %w", "CRDCompatibilityOperatorController", err)
	}

	return nil
}

// getContainerImage reads the container image from the crd-compatibility-checker-operator pod spec.
func getContainerImage(ctx context.Context, k8sClient client.Reader) (string, error) {
	podName := os.Getenv("POD_NAME")
	podNamespace := os.Getenv("POD_NAMESPACE")

	if podName == "" || podNamespace == "" {
		return "", errPodIdentityNotSet
	}

	var pod corev1.Pod
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: podName, Namespace: podNamespace}, &pod); err != nil {
		return "", fmt.Errorf("unable to get pod %s/%s: %w", podNamespace, podName, err)
	}

	// Find the crd-compatibility-checker-operator container
	for _, container := range pod.Spec.Containers {
		if container.Name == managerName {
			return container.Image, nil
		}
	}

	return "", fmt.Errorf("%s: %w", managerName, errContainerNotInPod)
}
