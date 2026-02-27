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

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8serrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"
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
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
)

// Assets is an interface that can be used to read assets from a filesystem.
type Assets interface {
	Asset(name string) ([]byte, error)
	ReadAssets() ([]fs.DirEntry, error)
}

type staticResourceInstallerController struct {
	assetNames                          []string // The names of the assets to install.
	client                              client.Client
	kubeClient                          kubernetes.Interface
	initialClusterOperatorsBootstrapped bool

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
	c.client = mgr.GetClient()

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
		// We only want to reconcile updates until we have observed that the cluster operators are all bootstrapped.
		// This allows us to inject FailurePolicy: Ignore for webhooks during cluster bootstrap.
		For(&configv1.ClusterOperator{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc:  func(e event.CreateEvent) bool { return e.Object.GetName() == controllers.ClusterOperatorName },
			UpdateFunc:  func(e event.UpdateEvent) bool { return !c.initialClusterOperatorsBootstrapped },
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
		c.mutateAsset(ctx),
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

func (c *staticResourceInstallerController) mutateAsset(ctx context.Context) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		raw, err := c.assets.Asset(name)
		if err != nil {
			return nil, fmt.Errorf("failed to read asset %s: %w", name, err)
		}

		requiredObj, err := resourceread.ReadGenericWithUnstructured(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to decode asset %s: %w", name, err)
		}

		switch t := requiredObj.(type) {
		case *admissionregistrationv1.ValidatingWebhookConfiguration:
			return c.mutateValidatingWebhookConfiguration(ctx, raw, t)
		case *admissionregistrationv1.MutatingWebhookConfiguration:
			return c.mutateMutatingWebhookConfiguration(ctx, raw, t)
		}

		return raw, nil
	}
}

type webhookPolicy struct {
	Name          string
	FailurePolicy *admissionregistrationv1.FailurePolicyType
}

func (c *staticResourceInstallerController) mutateValidatingWebhookConfiguration(ctx context.Context, raw []byte, obj *admissionregistrationv1.ValidatingWebhookConfiguration) ([]byte, error) {
	currentObj := &admissionregistrationv1.ValidatingWebhookConfiguration{}

	if err := c.client.Get(ctx, client.ObjectKey{Name: obj.Name}, currentObj); err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get validating webhook configuration %s: %w", obj.Name, err)
	} else if err != nil && apierrors.IsNotFound(err) {
		// If the object doesn't currently exist, apply it initially with the failure policy set to ignore
		// so that we don't block cluster operators during cluster bootstrap.
		for i := range obj.Webhooks {
			obj.Webhooks[i].FailurePolicy = ptr.To(admissionregistrationv1.Ignore)
		}

		data, err := k8syaml.Marshal(obj)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal object: %w", err)
		}

		return data, nil
	}

	clusterBootstrapped, err := c.clusterBootstrapped(ctx,
		util.SliceMap(obj.Webhooks, func(webhook admissionregistrationv1.ValidatingWebhook) webhookPolicy {
			return webhookPolicy{
				Name:          webhook.Name,
				FailurePolicy: webhook.FailurePolicy,
			}
		}),
		util.SliceMap(currentObj.Webhooks, func(webhook admissionregistrationv1.ValidatingWebhook) webhookPolicy {
			return webhookPolicy{
				Name:          webhook.Name,
				FailurePolicy: webhook.FailurePolicy,
			}
		}))
	if err != nil {
		return nil, fmt.Errorf("failed to check if cluster is bootstrapped: %w", err)
	}

	if clusterBootstrapped {
		return raw, nil
	}

	// Cluster isn't yet bootstrapped, force all webhooks to ignore failures so that we don't block cluster operators during cluster bootstrap.
	for i := range obj.Webhooks {
		obj.Webhooks[i].FailurePolicy = ptr.To(admissionregistrationv1.Ignore)
	}

	data, err := k8syaml.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal object: %w", err)
	}

	return data, nil
}

func (c *staticResourceInstallerController) mutateMutatingWebhookConfiguration(ctx context.Context, raw []byte, obj *admissionregistrationv1.MutatingWebhookConfiguration) ([]byte, error) {
	currentObj := &admissionregistrationv1.MutatingWebhookConfiguration{}

	if err := c.client.Get(ctx, client.ObjectKey{Name: obj.Name}, currentObj); err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get validating webhook configuration %s: %w", obj.Name, err)
	} else if err != nil && apierrors.IsNotFound(err) {
		// If the object doesn't currently exist, apply it initially with the failure policy set to ignore
		// so that we don't block cluster operators during cluster bootstrap.
		for i := range obj.Webhooks {
			obj.Webhooks[i].FailurePolicy = ptr.To(admissionregistrationv1.Ignore)
		}

		data, err := k8syaml.Marshal(obj)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal object: %w", err)
		}

		return data, nil
	}

	clusterBootstrapped, err := c.clusterBootstrapped(ctx,
		util.SliceMap(obj.Webhooks, func(webhook admissionregistrationv1.MutatingWebhook) webhookPolicy {
			return webhookPolicy{
				Name:          webhook.Name,
				FailurePolicy: webhook.FailurePolicy,
			}
		}),
		util.SliceMap(currentObj.Webhooks, func(webhook admissionregistrationv1.MutatingWebhook) webhookPolicy {
			return webhookPolicy{
				Name:          webhook.Name,
				FailurePolicy: webhook.FailurePolicy,
			}
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to check if cluster is bootstrapped: %w", err)
	}

	if clusterBootstrapped {
		return raw, nil
	}

	// Cluster isn't yet bootstrapped, force all webhooks to ignore failures so that we don't block cluster operators during cluster bootstrap.
	for i := range obj.Webhooks {
		obj.Webhooks[i].FailurePolicy = ptr.To(admissionregistrationv1.Ignore)
	}

	data, err := k8syaml.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal object: %w", err)
	}

	return data, nil
}

func (c *staticResourceInstallerController) clusterBootstrapped(ctx context.Context, webhooks, currentWebhooks []webhookPolicy) (bool, error) {
	// First check if the existing policies match the desired policies.
	// This means we already applied the manifest as it comes from the assets directly
	// without any mutation. To do that, the cluster must already have been bootstrapped.
	policiesMatch := true

	for _, webhook := range webhooks {
		for _, currentWebhook := range currentWebhooks {
			if webhook.Name == currentWebhook.Name {
				policiesMatch = policiesMatch && webhook.FailurePolicy != nil && currentWebhook.FailurePolicy != nil && *webhook.FailurePolicy == *currentWebhook.FailurePolicy
			}
		}
	}

	if policiesMatch {
		return true, nil
	}

	return c.clusterOperatorsBootstrapped(ctx)
}

func (c *staticResourceInstallerController) clusterOperatorsBootstrapped(ctx context.Context) (bool, error) {
	if c.initialClusterOperatorsBootstrapped {
		// We have previously seen all cluster operators bootstrapped since we started the controller.
		return true, nil
	}

	// Check all cluster operators and wait for them all to be bootstrapped.
	// Once they are bootstrapped, we can apply the manifest directly
	// as it is within the assets folder.
	clusterOperators := &configv1.ClusterOperatorList{}
	if err := c.client.List(ctx, clusterOperators); err != nil {
		return false, fmt.Errorf("failed to list cluster operators: %w", err)
	}

	for _, clusterOperator := range clusterOperators.Items {
		if !clusterOperatorBootstrapped(clusterOperator) {
			return false, nil
		}
	}

	c.initialClusterOperatorsBootstrapped = true

	return true, nil
}

func clusterOperatorBootstrapped(clusterOperator configv1.ClusterOperator) bool {
	conditions := clusterOperator.Status.Conditions

	available, ok := clusterOperatorCondition(conditions, configv1.OperatorAvailable)
	if !ok {
		return false
	}

	progressing, ok := clusterOperatorCondition(conditions, configv1.OperatorProgressing)
	if !ok {
		return false
	}

	degraded, ok := clusterOperatorCondition(conditions, configv1.OperatorDegraded)
	if !ok {
		return false
	}

	return available.Status == configv1.ConditionTrue &&
		progressing.Status == configv1.ConditionFalse &&
		degraded.Status == configv1.ConditionFalse
}

func clusterOperatorCondition(conditions []configv1.ClusterOperatorStatusCondition, conditionType configv1.ClusterStatusConditionType) (configv1.ClusterOperatorStatusCondition, bool) {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition, true
		}
	}
	return configv1.ClusterOperatorStatusCondition{}, false
}
