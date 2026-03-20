/*
Copyright 2026 Red Hat, Inc.

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

package installer

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	configv1apply "github.com/openshift/client-go/config/applyconfigurations/config/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"pkg.package-operator.run/boxcutter"
	"pkg.package-operator.run/boxcutter/machinery"
	machinerytypes "pkg.package-operator.run/boxcutter/machinery/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

// errCollision is returned when boxcutter refuses to manage an object due to
// a collision with an existing object on the cluster.
var errCollision = errors.New("collision with existing objects")

type revisionReconciler struct {
	*InstallerController
	log            logr.Logger
	gvks           sets.Set[schema.GroupVersionKind]
	relatedObjects sets.Set[configv1.ObjectReference]
}

func newRevisionReconciler(installerController *InstallerController, log logr.Logger) *revisionReconciler {
	return &revisionReconciler{
		InstallerController: installerController,
		log:                 log,
		gvks:                sets.New[schema.GroupVersionKind](),
		relatedObjects:      sets.New[configv1.ObjectReference](),
	}
}

func (r *revisionReconciler) reconcile(ctx context.Context, revisions []operatorv1alpha1.ClusterAPIInstallerRevision) (*operatorv1alpha1.RevisionName, []string, []error) {
	// Sort revisions descending by revision number (newest first)
	revisions = slices.Clone(revisions)
	slices.SortFunc(revisions, func(a, b operatorv1alpha1.ClusterAPIInstallerRevision) int {
		return cmp.Compare(b.Revision, a.Revision)
	})

	revisionNames := util.SliceMap(revisions, func(rev operatorv1alpha1.ClusterAPIInstallerRevision) string {
		return string(rev.Name)
	})
	r.log.Info("Reconciling revisions", "revisions", strings.Join(revisionNames, ", "))

	isComplete, messages, errs := r.reconcileRevisions(ctx, revisions)
	if isComplete {
		return &revisions[0].Name, messages, errs
	}

	return nil, messages, errs
}

func (r *revisionReconciler) reconcileRevisions(ctx context.Context, revisions []operatorv1alpha1.ClusterAPIInstallerRevision) (bool, []string, []error) {
	if len(revisions) == 0 {
		return true, nil, nil
	}

	head := revisions[0]
	tail := revisions[1:]

	isComplete, messages, err := r.reconcileRevision(ctx, head)

	// If this revision reconciled successfully, we call Teardown instead of Reconcile for older revisions.
	var tailHandler revisionHandler
	if isComplete {
		tailHandler = r.teardownRevisions
	} else {
		tailHandler = r.reconcileRevisions
	}

	return mergeWithTail(ctx, tailHandler, tail)(isComplete, messages, err)
}

// reconcileRevision reconciles a single revision and returns:
// * a summary message
// * a boolean indicating if the revision was reconciled completely
// * an error if any occurred.
func (r *revisionReconciler) reconcileRevision(ctx context.Context, apiRevision operatorv1alpha1.ClusterAPIInstallerRevision) (bool, string, error) {
	revision, err := revisiongenerator.NewInstallerRevisionFromAPI(apiRevision, r.providerProfiles, revisiongenerator.WithObjectCollectors(r.collectObjects))
	if err != nil {
		return false, "", fmt.Errorf("error creating installer revision from API revision %s: %w", apiRevision.Name, reconcile.TerminalError(err))
	}

	bcRevision := toBoxcutterRevision(revision)
	phases := bcRevision.GetPhases()

	totalObjects := 0
	for _, p := range phases {
		totalObjects += len(p.GetObjects())
	}

	r.log.Info("Reconciling revision", "revision", apiRevision.Name, "phases", len(phases), "totalObjects", totalObjects)

	result, err := r.revisionEngine.Reconcile(ctx, bcRevision, boxcutter.WithAggregatePhaseReconcileErrors())
	r.log.Info("Revision reconcile completed", "revision", apiRevision.Name)

	if err != nil {
		// This is a terminal error, same as ActionCollision.
		var collisionErr *machinery.CreateCollisionError
		if errors.As(err, &collisionErr) {
			err = reconcile.TerminalError(fmt.Errorf("%w: %s", errCollision, collisionErr.Error()))
		}

		err = fmt.Errorf("reconciling revision %s: %w", revision.RevisionName(), err)

		return false, err.Error(), err
	}

	if validationError := result.GetValidationError(); validationError != nil {
		err = fmt.Errorf("revision %s has a validation error: %w", revision.RevisionName(), reconcile.TerminalError(validationError))
		return false, validationError.String(), err
	}

	if err := r.handlePhaseResults(apiRevision, result); err != nil {
		return false, err.Error(), err
	}

	if result.IsComplete() {
		return true, fmt.Sprintf("Revision %s: complete", revision.RevisionName()), nil
	}

	var message string

	for _, phase := range result.GetPhases() {
		if !phase.IsComplete() {
			message = fmt.Sprintf("Revision %s: waiting on phase %s", revision.RevisionName(), phase.GetName())
			break
		}
	}

	if message == "" {
		// Probably shouldn't happen?
		message = fmt.Sprintf("Revision %s: waiting for reconciliation", revision.RevisionName())
	}

	return false, message, nil
}

func (r *revisionReconciler) handlePhaseResults(apiRevision operatorv1alpha1.ClusterAPIInstallerRevision, result machinery.RevisionResult) error {
	var collisions []string

	for _, phase := range result.GetPhases() {
		objects := phase.GetObjects()
		actionCounts := map[string]int{}

		var incomplete []string

		for _, obj := range objects {
			action := obj.Action()
			actionCounts[string(action)]++

			if !obj.IsComplete() {
				incomplete = append(incomplete, obj.Object().GetName())
			}

			switch action {
			case machinery.ActionCreated, machinery.ActionUpdated, machinery.ActionRecovered:
				ref := machinerytypes.ToObjectRef(obj.Object())
				r.log.Info("Object "+strings.ToLower(string(action)),
					"revision", apiRevision.Name,
					"phase", phase.GetName(),
					"object", ref.String(),
				)

			case machinery.ActionCollision:
				ref := machinerytypes.ToObjectRef(obj.Object())
				kvs := []any{
					"revision", apiRevision.Name,
					"phase", phase.GetName(),
					"object", ref.String(),
				}

				desc := ref.String()

				if collision, ok := obj.(machinery.ObjectResultCollision); ok {
					if owner, hasOwner := collision.ConflictingOwner(); hasOwner {
						ownerStr := owner.String()
						kvs = append(kvs, "conflictingOwner", ownerStr)
						desc += fmt.Sprintf(" (owned by %s)", ownerStr)
					}
				}

				r.log.Info("Object collision", kvs...)

				collisions = append(collisions, desc)

			case machinery.ActionProgressed, machinery.ActionIdle:
				// Don't log
			}
		}

		r.log.Info("Phase result",
			"revision", apiRevision.Name,
			"phase", phase.GetName(),
			"complete", phase.IsComplete(),
			"objects", len(objects),
			"actions", actionCounts,
			"incomplete", incomplete,
		)
	}

	if len(collisions) == 0 {
		return nil
	}

	return fmt.Errorf("revision %s: %w: %s",
		apiRevision.Name,
		reconcile.TerminalError(errCollision),
		strings.Join(collisions, ", "),
	)
}

func (r *revisionReconciler) teardownRevisions(ctx context.Context, revisions []operatorv1alpha1.ClusterAPIInstallerRevision) (bool, []string, []error) {
	if len(revisions) == 0 {
		return true, nil, nil
	}

	head := revisions[0]
	tail := revisions[1:]

	return mergeWithTail(ctx, r.teardownRevisions, tail)(r.teardownRevision(ctx, head))
}

// teardownRevision tear down a single revision and returns:
// * a summary message
// * a boolean indicating if the revision was torn down completely
// * an error if any occurred.
func (r *revisionReconciler) teardownRevision(ctx context.Context, apiRevision operatorv1alpha1.ClusterAPIInstallerRevision) (bool, string, error) {
	revision, err := revisiongenerator.NewInstallerRevisionFromAPI(apiRevision, r.providerProfiles, revisiongenerator.WithObjectCollectors(r.collectObjects))
	if err != nil {
		// We can't teardown this revision if we can't create it, so we consider it complete.
		return true, "", fmt.Errorf("error creating installer revision from API revision %s: %w", apiRevision.Name, reconcile.TerminalError(err))
	}

	bcRevision := toBoxcutterRevision(revision)
	phases := bcRevision.GetPhases()

	totalObjects := 0
	for _, p := range phases {
		totalObjects += len(p.GetObjects())
	}

	r.log.Info("Tearing down revision", "revision", apiRevision.Name, "phases", len(phases), "totalObjects", totalObjects)

	result, err := r.revisionEngine.Teardown(ctx, bcRevision, boxcutter.WithAggregatePhaseTeardownErrors())
	r.log.Info("Revision teardown completed", "revision", apiRevision.Name)

	if err != nil {
		err = fmt.Errorf("revision %s: %w", revision.RevisionName(), err)
		return false, err.Error(), err
	}

	r.logTeardownPhaseResults(apiRevision, result)

	if result.IsComplete() {
		return true, fmt.Sprintf("Revision %s: torn down", revision.RevisionName()), nil
	}

	var message string
	if activePhaseName, ok := result.GetActivePhaseName(); ok {
		message = fmt.Sprintf("Revision %s: tearing down phase %s", revision.RevisionName(), activePhaseName)
	} else {
		// Probably shouldn't happen?
		message = fmt.Sprintf("Revision %s: waiting for teardown", revision.RevisionName())
	}

	return false, message, nil
}

func (r *revisionReconciler) logTeardownPhaseResults(apiRevision operatorv1alpha1.ClusterAPIInstallerRevision, result machinery.RevisionTeardownResult) {
	for _, phase := range result.GetPhases() {
		gone := phase.Gone()
		waiting := phase.Waiting()

		for _, obj := range gone {
			r.log.Info("Object gone",
				"revision", apiRevision.Name,
				"phase", phase.GetName(),
				"object", obj.String(),
			)
		}

		for _, obj := range waiting {
			r.log.Info("Object waiting",
				"revision", apiRevision.Name,
				"phase", phase.GetName(),
				"object", obj.String(),
			)
		}

		r.log.Info("Phase teardown result",
			"revision", apiRevision.Name,
			"phase", phase.GetName(),
			"complete", phase.IsComplete(),
			"gone", len(gone),
			"waiting", len(waiting),
		)
	}
}

type revisionHandler func(context.Context, []operatorv1alpha1.ClusterAPIInstallerRevision) (bool, []string, []error)

// mergeWithTail merges the results of the head revision with the results of calling the tail handler on the tail revisions.
func mergeWithTail(ctx context.Context, tailHandler revisionHandler, tailRevisions []operatorv1alpha1.ClusterAPIInstallerRevision) func(bool, string, error) (bool, []string, []error) {
	return func(headComplete bool, headMessage string, headErr error) (bool, []string, []error) {
		tailComplete, tailMessages, tailErrs := tailHandler(ctx, tailRevisions)

		messages := append([]string{headMessage}, tailMessages...)
		isComplete := tailComplete && headComplete

		errs := tailErrs
		if headErr != nil {
			errs = append([]error{headErr}, tailErrs...)
		}

		return isComplete, messages, errs
	}
}

func (r *revisionReconciler) collectObjects(obj unstructured.Unstructured) {
	gvk := obj.GroupVersionKind()
	r.gvks.Insert(gvk)

	// In relatedObjects, collect:
	// * namespaces
	// * non-namespaced objects
	//
	// must-gather already fetches all the useful standard namespaced objects,
	// so we don't list those explicitly. kube-apiserver already collects all
	// CRDs, so we don't list those either. Instead, for CRDs we explicitly
	// collect all instance objects.
	switch {
	case gvk.GroupKind() == (schema.GroupKind{Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"}):
		// For CRDs, collect all instance objects rather than the CRD itself.
		crdGroup, _, _ := unstructured.NestedString(obj.Object, "spec", "group")
		crdResource, _, _ := unstructured.NestedString(obj.Object, "spec", "names", "plural")

		if crdGroup != "" && crdResource != "" {
			r.relatedObjects.Insert(configv1.ObjectReference{
				Group:    crdGroup,
				Resource: crdResource,
			})
		}
	case obj.GetNamespace() == "":
		// Collect non-namespaced objects directly (includes Namespaces).
		r.relatedObjects.Insert(configv1.ObjectReference{
			Group:    gvk.Group,
			Resource: strings.ToLower(gvk.Kind) + "s",
			Name:     obj.GetName(),
		})
	}
}

func (r *revisionReconciler) relatedObjectsStatusApplyConfiguration() *configv1apply.ClusterOperatorStatusApplyConfiguration {
	relatedObjects := r.relatedObjects.UnsortedList()

	// Related objects must be in a stable order to avoid continuous reconciles
	slices.SortFunc(relatedObjects, func(a, b configv1.ObjectReference) int {
		if c := cmp.Compare(a.Namespace, b.Namespace); c != 0 {
			return c
		}

		if c := cmp.Compare(a.Group, b.Group); c != 0 {
			return c
		}

		if c := cmp.Compare(a.Resource, b.Resource); c != 0 {
			return c
		}

		return cmp.Compare(a.Name, b.Name)
	})

	// Convert related objects from ObjectReference to ObjectReferenceApplyConfiguration
	objectRefs := util.SliceMap(relatedObjects, func(obj configv1.ObjectReference) *configv1apply.ObjectReferenceApplyConfiguration {
		// group, resource, and name are required, although they can be empty
		ref := configv1apply.ObjectReference().
			WithGroup(obj.Group).
			WithResource(obj.Resource).
			WithName(obj.Name)
		if obj.Namespace != "" {
			ref.WithNamespace(obj.Namespace)
		}

		return ref
	})

	return configv1apply.ClusterOperatorStatus().
		WithRelatedObjects(objectRefs...)
}
