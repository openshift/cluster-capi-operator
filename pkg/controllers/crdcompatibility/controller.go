/*
Copyright 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package crdcompatibility

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
)

const (
	fieldIndexCRDRef string = "spec.crdRef"

	controllerName string = "crdcompatibility.operator.openshift.io"
)

//+kubebuilder:rbac:groups=operator.openshift.io,resources=crdcompatibilityrequirements,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=operator.openshift.io,resources=crdcompatibilityrequirements/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=operator.openshift.io,resources=crdcompatibilityrequirements/finalizers,verbs=update
//+kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch

// NewCRDCompatibilityReconciler returns a partially initialised CRDCompatibilityReconciler.
func NewCRDCompatibilityReconciler(client client.Client) *CRDCompatibilityReconciler {
	return &CRDCompatibilityReconciler{
		client:                client,
		syncedRequirementChan: make(chan string, 5),
	}
}

// CRDCompatibilityReconciler reconciles CRDCompatibilityRequirement resources.
type CRDCompatibilityReconciler struct {
	client client.Client

	validator *crdValidator

	// Lock is required to avoid writing to closed channel, which would panic.
	//   receiver: waitForSynced()
	//   senders: reconcile loops call syncedRequirement()
	//
	// We close the channel from the receiver, which is not the normal pattern.
	// We do this because:
	// * only the receiver knows when it has received enough data
	// * the senders continue to run indefinitely after the receiver has finished
	//
	// The sender guards writing to the channel by checking synced in
	// syncedRequirement(). Consequently we need to ensure that nothing is in
	// this critical section when we close the channel.
	lock sync.RWMutex

	// synced indicates that the controller has synced the state of the webhook
	synced bool
	// syncedRequirementChan is used to send the name of a successfully
	// reconciled requirement to the sync waiter.
	syncedRequirementChan chan string
}

type controllerOption func(*builder.Builder) *builder.Builder

// MachineByNodeName contains the logic to index Machines by Node name.
func CRDByCRDRef(obj client.Object) []string {
	requirement, ok := obj.(*operatorv1alpha1.CRDCompatibilityRequirement)
	if !ok {
		panic(fmt.Sprintf("Expected a CRDCompatibilityRequirement but got a %T", obj))
	}

	return []string{requirement.Spec.CRDRef}
}

// SetupWithManager sets up the controller with the Manager.
func (r *CRDCompatibilityReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, opts ...controllerOption) error {
	// Create field index for spec.crdRef
	if err := mgr.GetFieldIndexer().IndexField(ctx, &operatorv1alpha1.CRDCompatibilityRequirement{}, fieldIndexCRDRef, CRDByCRDRef); err != nil {
		return fmt.Errorf("failed to add index to CRDCompatibilityRequirements: %w", err)
	}

	crdValidator := &crdValidator{
		client: mgr.GetClient(),
	}
	r.validator = crdValidator

	crdValidatorBuilder := ctrl.NewWebhookManagedBy(mgr).
		For(&apiextensionsv1.CustomResourceDefinition{}).
		WithValidator(crdValidator)

	crdRequirementValidatorBuilder := ctrl.NewWebhookManagedBy(mgr).
		For(&operatorv1alpha1.CRDCompatibilityRequirement{}).
		WithValidator(&crdRequirementValidator{})

	controllerBuilder := ctrl.NewControllerManagedBy(mgr).
		// We don't need to reconcile deletion because we use a finalizer
		For(&operatorv1alpha1.CRDCompatibilityRequirement{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc:  func(e event.CreateEvent) bool { return true },
			UpdateFunc:  func(e event.UpdateEvent) bool { return true },
			GenericFunc: func(e event.GenericEvent) bool { return true },
		})).
		Watches(
			&apiextensionsv1.CustomResourceDefinition{},
			handler.EnqueueRequestsFromMapFunc(r.findCRDCompatibilityRequirementsForCRD),
		)

	for _, opt := range opts {
		controllerBuilder = opt(controllerBuilder)
	}

	return errors.Join(
		crdValidatorBuilder.Complete(),
		crdRequirementValidatorBuilder.Complete(),
		controllerBuilder.Complete(r),
	)
}

// findCRDCompatibilityRequirementsForCRD finds all CRDCompatibilityRequirements that reference the given CRD.
func (r *CRDCompatibilityReconciler) findCRDCompatibilityRequirementsForCRD(ctx context.Context, obj client.Object) []reconcile.Request {
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return nil
	}

	// Use field index to find CRDCompatibilityRequirements that reference this CRD
	var requirements operatorv1alpha1.CRDCompatibilityRequirementList
	if err := r.client.List(ctx, &requirements, client.MatchingFields{fieldIndexCRDRef: crd.Name}); err != nil {
		log.FromContext(ctx).Error(err, "failed to list CRDCompatibilityRequirements for CRD", "crdName", crd.Name, "clientType", fmt.Sprintf("%T", r.client))
		return nil
	}

	requests := make([]reconcile.Request, len(requirements.Items))
	for i := range requirements.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: requirements.Items[i].Name,
			},
		}
	}

	return requests
}

// IsSynced returns true if the controller has synced the state of the webhook
// and is ready to serve validations.
func (r *CRDCompatibilityReconciler) IsSynced() bool {
	r.lock.RLock()
	defer r.lock.RUnlock()

	return r.synced
}

// syncedRequirement is called by the reconcile loop to register a successful
// reconciliation.
func (r *CRDCompatibilityReconciler) syncedRequirement(ctx context.Context, requirement string) {
	r.lock.RLock()
	defer r.lock.RUnlock()

	if !r.synced {
		select {
		case r.syncedRequirementChan <- requirement:
		case <-ctx.Done():
			return
		}
	}
}

// getRequirementsToSync returns the names of the requirements that need to be
// synced before the webhook can serve validations. It returns every requirement
// which was previously admitted, and therefore may have been in force by a
// previous incarnation of the controller.
func (r *CRDCompatibilityReconciler) getRequirementsToSync(ctx context.Context) (map[string]struct{}, error) {
	allRequirements := operatorv1alpha1.CRDCompatibilityRequirementList{}
	if err := r.client.List(ctx, &allRequirements); err != nil {
		return nil, fmt.Errorf("listing CRDCompatibilityRequirements: %w", err)
	}

	toSync := make(map[string]struct{}, len(allRequirements.Items))

	for i := range allRequirements.Items {
		requirement := allRequirements.Items[i]

		// We don't need to sync deleted requirements
		if !requirement.DeletionTimestamp.IsZero() {
			continue
		}

		// We don't need to sync requirements which were not previously admitted
		admitted := func() bool {
			for i := range requirement.Status.Conditions {
				condition := &requirement.Status.Conditions[i]
				if condition.Type == conditionTypeAdmitted {
					return condition.Status == metav1.ConditionTrue
				}
			}

			// If there is no admitted condition the requirement was not admitted
			return false
		}()
		if !admitted {
			continue
		}

		toSync[requirement.Name] = struct{}{}
	}

	return toSync, nil
}

// WaitForSynced blocks until the state of the CRD validator webhook is up to
// date. The state of the webhook is up to date when every requirement which was
// previously admitted has been reconciled at least once.
func (r *CRDCompatibilityReconciler) WaitForSynced(ctx context.Context) error {
	logger := log.FromContext(ctx)

	var toSync map[string]struct{}

	// Fetch the set of requirements to sync. We use an exponential backoff here because:
	// * returning an error is expensive because we would have to terminate the container
	// * depending on when we're starting up, cluster disruption is reasonably likely
	if err := wait.ExponentialBackoffWithContext(ctx, wait.Backoff{
		Steps:    10,
		Factor:   1.25,
		Duration: 1 * time.Second,
		Jitter:   1.0,
	}, func(ctx context.Context) (bool, error) {
		var err error
		toSync, err = r.getRequirementsToSync(ctx)
		if err != nil {
			logger.Error(err, "failed to get requirements to sync")
			return false, nil
		}

		return true, nil
	}); err != nil {
		return fmt.Errorf("WaitForSynced: %w", err)
	}

	logger.Info("Waiting for requirements to be synced", "requirements", toSync)

	for {
		select {
		case requirement := <-r.syncedRequirementChan:
			delete(toSync, requirement)
		case <-ctx.Done():
			return fmt.Errorf("WaitForSynced: %w", ctx.Err())
		}

		if len(toSync) == 0 {
			r.lock.Lock()
			defer r.lock.Unlock()

			r.synced = true
			close(r.syncedRequirementChan)

			// This ensures that the channel and any remaining buffered messages
			// will eventually be garbage collected.
			// This is only safe because we are holding the write lock, so we
			// know nothing is sending to the channel.
			r.syncedRequirementChan = nil

			return nil
		}
	}
}
