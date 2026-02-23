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
	"path/filepath"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8serrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	k8syaml "sigs.k8s.io/yaml"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
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
	assetNames []string // The names of the assets to install.
	kubeClient kubernetes.Interface

	assets        Assets
	resourceCache resourceapply.ResourceCache
}

// NewStaticResourceInstallerController creates a new static resource installer controller.
func NewStaticResourceInstallerController(assets Assets) *staticResourceInstallerController {
	return &staticResourceInstallerController{
		assets:        assets,
		resourceCache: resourceapply.NewResourceCache(),
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

	build := ctrl.NewControllerManagedBy(mgr).
		Named("static-resource-installer").
		// We only want to reconcile an initial time when the cluster operator is created
		// in the cache, later reconciles will happen based on watches for individual assets.
		For(&configv1.ClusterOperator{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc:  func(e event.CreateEvent) bool { return e.Object.GetName() == controllers.ClusterOperatorName },
			UpdateFunc:  func(e event.UpdateEvent) bool { return false },
			DeleteFunc:  func(e event.DeleteEvent) bool { return false },
			GenericFunc: func(e event.GenericEvent) bool { return false },
		}))

	// Watch each asset with a predicate and map to the cluster operator
	// so that we trigger a reconcile on writes to any asset.
	for _, asset := range c.assetNames {
		obj, err := assetToObject(c.assets, asset)
		if err != nil {
			return fmt.Errorf("failed to convert asset to object: %w", err)
		}

		build = build.Watches(
			obj,
			handler.EnqueueRequestsFromMapFunc(operatorstatus.ToClusterOperator),
			builder.WithPredicates(objectNamePredicate(obj.GetName())),
		)
	}

	if err := build.Complete(c); err != nil {
		return fmt.Errorf("failed to complete controller: %w", err)
	}

	return nil
}

// Reconcile reconciles the static resource installer controller.
// This will apply the static manifests from the assets member to the cluster.
func (c *staticResourceInstallerController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	results := resourceapply.ApplyDirectly(
		ctx,
		resourceapply.NewKubeClientHolder(c.kubeClient),
		events.NewKubeRecorder(c.kubeClient.CoreV1().Events("default"), "static-resource-installer", &v1.ObjectReference{
			Kind: "ClusterOperator",
			Name: "cluster-api",
		}, clock.RealClock{}),
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
