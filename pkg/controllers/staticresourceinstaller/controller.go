// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package staticresourceinstaller

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8serrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	k8syaml "sigs.k8s.io/yaml"

	"github.com/openshift/cluster-capi-operator/pkg/util"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
)

// Assets is an interface that can be used to read assets from a filesystem.
type Assets interface {
	Asset(name string) ([]byte, error)
	ReadAssets() ([]fs.DirEntry, error)
}

type staticResourceInstallerController struct {
	assetNames           []string // The names of the assets to install.
	kubeClient           kubernetes.Interface
	eventObjectReference v1.ObjectReference

	assets                  Assets
	resourceCache           resourceapply.ResourceCache
	webhookReadinessChecker healthz.Checker
	mutex                   sync.Mutex
}

// NewStaticResourceInstallerController creates a new static resource installer controller.
func NewStaticResourceInstallerController(assets Assets, eventObjectReference v1.ObjectReference) *staticResourceInstallerController {
	return &staticResourceInstallerController{
		assets:               assets,
		eventObjectReference: eventObjectReference,
		resourceCache:        resourceapply.NewResourceCache(),
	}
}

// SetupWithManager sets up the static resource installer controller with the given manager.
func (c *staticResourceInstallerController) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// The assets are an embedded filesystem and won't change over time.
	assets, err := c.assets.ReadAssets()
	if err != nil {
		return fmt.Errorf("failed to read assets: %w", err)
	}

	c.assetNames = util.SliceMap(assets, func(asset fs.DirEntry) string {
		return filepath.Join("assets", asset.Name())
	})

	c.kubeClient, err = kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		return fmt.Errorf("failed to create kube client: %w", err)
	}

	c.webhookReadinessChecker = mgr.GetWebhookServer().StartedChecker()

	if err := mgr.Add(initialResourceInstaller{controller: c}); err != nil {
		return fmt.Errorf("failed to add initial resource installer: %w", err)
	}

	build := ctrl.NewControllerManagedBy(mgr).Named("static-resource-installer")

	// Watch each asset to correct drift after the initial installation.
	for _, asset := range c.assetNames {
		obj, err := assetToObject(c.assets, asset)
		if err != nil {
			return fmt.Errorf("failed to convert asset to object: %w", err)
		}

		build = build.Watches(
			obj,
			&handler.EnqueueRequestForObject{},
			builder.WithPredicates(objectNamePredicate(obj.GetName())),
		)
	}

	if err := build.Complete(c); err != nil {
		return fmt.Errorf("failed to complete controller: %w", err)
	}

	return nil
}

type initialResourceInstaller struct {
	controller *staticResourceInstallerController
}

// Start installs static resources once leadership is acquired and waits for cancellation.
func (i initialResourceInstaller) Start(ctx context.Context) error {
	if err := waitForWebhookServer(ctx, i.controller.webhookReadinessChecker); err != nil {
		return fmt.Errorf("failed waiting for webhook server: %w", err)
	}

	if _, err := i.controller.Reconcile(ctx, ctrl.Request{}); err != nil {
		return fmt.Errorf("failed to install initial static resources: %w", err)
	}

	<-ctx.Done()

	return nil
}

// NeedLeaderElection ensures initial installation is performed only by the active manager.
func (i initialResourceInstaller) NeedLeaderElection() bool {
	return true
}

func waitForWebhookServer(ctx context.Context, checker healthz.Checker) error {
	if err := wait.PollUntilContextCancel(ctx, time.Second, true, func(ctx context.Context) (bool, error) {
		return checker(&http.Request{}) == nil, nil
	}); err != nil {
		return fmt.Errorf("webhook server did not become ready: %w", err)
	}

	return nil
}

// Reconcile reconciles the static resource installer controller.
// This will apply the static manifests from the assets member to the cluster.
func (c *staticResourceInstallerController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	results := resourceapply.ApplyDirectly(
		ctx,
		resourceapply.NewKubeClientHolder(c.kubeClient),
		events.NewKubeRecorder(c.kubeClient.CoreV1().Events("default"), "static-resource-installer", &c.eventObjectReference, clock.RealClock{}),
		c.resourceCache,
		c.assets.Asset,
		c.assetNames...,
	)

	var errs []error

	for _, result := range results {
		if result.Error != nil {
			errs = append(errs, result.Error)
		}
	}

	if len(errs) > 0 {
		return ctrl.Result{}, k8serrors.NewAggregate(errs)
	}

	return ctrl.Result{}, nil
}

func assetToObject(assets Assets, asset string) (*unstructured.Unstructured, error) {
	raw, err := assets.Asset(asset)
	if err != nil {
		return nil, fmt.Errorf("failed to read asset %s: %w", asset, err)
	}

	obj := &unstructured.Unstructured{}
	if err := k8syaml.Unmarshal(raw, obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal asset to object: %w", err)
	}

	return obj, nil
}

func objectNamePredicate(name string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetName() == name
	})
}
