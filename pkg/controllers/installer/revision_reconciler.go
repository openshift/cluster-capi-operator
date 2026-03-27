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
	"k8s.io/apimachinery/pkg/api/meta"
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

/*
 * The entry point to this file is the revisionReconciler.reconcile() function.
 * revisionReconciler.reconcile() reconciles a list of revisions.
 *
 * Revisions are created by the revision controller. They represent all the
 * manifests from the current release which are relevant to the current cluster.
 * The revision controller will add a new revision with a higher revision number
 * whenever there is any change in the manifests to be applied.
 *
 * The goal of the installer controller is to ensure all manifests specified by
 * the most recent revision are applied to the cluster, and that objects which
 * are no longer required are removed.
 *
 * The installer controller manages objects with Boxcutter. Boxcutter defines 2
 * operations on a revision:
 * * reconcile (create)
 * * teardown (remove)
 * We want to 'reconcile' the newest revision, and 'teardown' any older
 * revisions. Note that a newer revision will typically be similar to an older
 * revision, so an object will usually be in multiple revisions. Boxcutter
 * stores metadata on objects it installs to track the most recent revision
 * which was successfully applied the object. During teardown, it won't remove
 * an object if it's managed by a newer revision. Therefore it's crtically
 * important that we successfully reconcile the new revision before tearing down
 * any older revisions.
 *
 * An additional goal is that if the newest revision can't be applied for any
 * reason, for example because its CRD Requirements are not compatible with the
 * cluster, we should continue to reconcile the next most recently applied
 * revision.
 *
 * The algorithm here is to try to reconcile the newest revision. If it succeeds
 * we teardown all older revisions. If it does not succeed, either because of
 * failure or just because it's incomplete, we try to reconcile the next
 * revision according to the same algorithm. This maps nicely to tail recursion
 * because at any point in the revision list we are, we are always in one of
 * two modes:
 * * reconcile the head and teardown the tail, or
 * * teardown everything
 */

// errCollision is returned when boxcutter refuses to manage an object due to
// a collision with an existing object on the cluster.
var errCollision = errors.New("collision with existing objects")

// convertedRevision holds a pre-converted InstallerRevision.
// If conversion fails, revision is nil and conversionErr is set.
// We need this because conversion errors are handled differently depending on
// whether the revision is being reconciled or torn down, and we don't know
// which in advance.
type convertedRevision struct {
	revision      revisiongenerator.InstallerRevision
	conversionErr error
}

// collectedObjectRef holds intermediate object reference data collected during
// revision rendering. The resource name is resolved later.
type collectedObjectRef struct {
	gvk  schema.GroupVersionKind
	name string
}

type revisionReconciler struct {
	*InstallerController
	log                   logr.Logger
	gvks                  sets.Set[schema.GroupVersionKind]
	collectedNonNSObjects sets.Set[collectedObjectRef]       // intermediate storage
	crdGKResourceMapping  map[schema.GroupKind]string        // CRD GK → resource
	relatedObjects        sets.Set[configv1.ObjectReference] // kept for backward compatibility
}

func newRevisionReconciler(installerController *InstallerController, log logr.Logger) *revisionReconciler {
	return &revisionReconciler{
		InstallerController:   installerController,
		log:                   log,
		gvks:                  sets.New[schema.GroupVersionKind](),
		collectedNonNSObjects: sets.New[collectedObjectRef](),
		crdGKResourceMapping:  make(map[schema.GroupKind]string),
		relatedObjects:        sets.New[configv1.ObjectReference](),
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

	// Convert all API revisions upfront so that collectObjects (and thus
	// relatedObjects) is fully populated before reconciliation begins.
	converted := util.SliceMap(revisions, func(apiRev operatorv1alpha1.ClusterAPIInstallerRevision) convertedRevision {
		rev, err := revisiongenerator.NewInstallerRevisionFromAPI(apiRev, r.providerProfiles, revisiongenerator.WithObjectCollectors(r.collectObjects))
		if err != nil {
			err = fmt.Errorf("error creating installer revision from API revision %s: %w", apiRev.Name, reconcile.TerminalError(err))
		}

		return convertedRevision{
			revision:      rev,
			conversionErr: err,
		}
	})

	// Resolve collected objects to final ObjectReferences with correct plurals
	if err := r.resolveCollectedObjects(); err != nil {
		return nil, nil, []error{err}
	}

	isComplete, messages, errs := r.reconcileRevisions(ctx, converted)
	if isComplete {
		name := converted[0].revision.RevisionName()
		return &name, messages, errs
	}

	return nil, messages, errs
}

func (r *revisionReconciler) reconcileRevisions(ctx context.Context, revisions []convertedRevision) (bool, []string, []error) {
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
func (r *revisionReconciler) reconcileRevision(ctx context.Context, conv convertedRevision) (bool, string, error) {
	if conv.conversionErr != nil {
		return false, "", conv.conversionErr
	}

	revision := conv.revision
	bcRevision := toBoxcutterRevision(revision)
	phases := bcRevision.GetPhases()

	totalObjects := 0
	for _, p := range phases {
		totalObjects += len(p.GetObjects())
	}

	revisionName := revision.RevisionName()
	r.log.Info("Reconciling revision", "revision", revisionName, "phases", len(phases), "totalObjects", totalObjects)

	result, err := r.revisionEngine.Reconcile(ctx, bcRevision, boxcutter.WithAggregatePhaseReconcileErrors())
	r.log.Info("Revision reconcile completed", "revision", revisionName)

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

	if err := r.handlePhaseResults(revisionName, result); err != nil {
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

func (r *revisionReconciler) handlePhaseResults(revisionName operatorv1alpha1.RevisionName, result machinery.RevisionResult) error {
	log := r.log.WithValues("revision", revisionName)

	var collisions []string

	for _, phase := range result.GetPhases() {
		log := log.WithValues("phase", phase.GetName())
		objects := phase.GetObjects()
		actionCounts := map[string]int{}

		var incomplete []string

		for _, obj := range objects {
			result := handlePhaseObject(log, obj, actionCounts)
			if result.incomplete {
				incomplete = append(incomplete, obj.Object().GetName())
			}

			if result.collision != "" {
				collisions = append(collisions, result.collision)
			}
		}

		r.log.Info("Phase result",
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
		revisionName,
		reconcile.TerminalError(errCollision),
		strings.Join(collisions, ", "),
	)
}

type phaseObjectResult struct {
	incomplete bool
	collision  string
}

func handlePhaseObject(log logr.Logger, obj machinery.ObjectResult, actionCounts map[string]int) phaseObjectResult {
	result := phaseObjectResult{}

	objRef := machinerytypes.ToObjectRef(obj.Object())
	log = log.WithValues("object", objRef.String())
	action := obj.Action()
	actionCounts[string(action)]++

	if !obj.IsComplete() {
		result.incomplete = true
	}

	switch action {
	case machinery.ActionCreated, machinery.ActionUpdated, machinery.ActionRecovered:
		log.Info("Object " + strings.ToLower(string(action)))

	case machinery.ActionCollision:
		desc := objRef.String()

		var kvs []any

		if collision, ok := obj.(machinery.ObjectResultCollision); ok {
			if owner, hasOwner := collision.ConflictingOwner(); hasOwner {
				ownerStr := owner.String()
				kvs = append(kvs, "conflictingOwner", ownerStr)
				desc += fmt.Sprintf(" (owned by %s)", ownerStr)
			}
		}

		log.Info("Object collision", kvs...)

		result.collision = desc

	case machinery.ActionProgressed, machinery.ActionIdle:
		// Don't log
	}

	return result
}

func (r *revisionReconciler) teardownRevisions(ctx context.Context, revisions []convertedRevision) (bool, []string, []error) {
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
func (r *revisionReconciler) teardownRevision(ctx context.Context, conv convertedRevision) (bool, string, error) {
	if conv.conversionErr != nil {
		// We can't teardown this revision if we can't create it, so we consider it complete.
		return true, "", conv.conversionErr
	}

	revision := conv.revision
	revisionName := revision.RevisionName()
	bcRevision := toBoxcutterRevision(revision)
	phases := bcRevision.GetPhases()

	totalObjects := 0
	for _, p := range phases {
		totalObjects += len(p.GetObjects())
	}

	r.log.Info("Tearing down revision", "revision", revisionName, "phases", len(phases), "totalObjects", totalObjects)

	result, err := r.revisionEngine.Teardown(ctx, bcRevision, boxcutter.WithAggregatePhaseTeardownErrors())
	r.log.Info("Revision teardown completed", "revision", revisionName)

	if err != nil {
		err = fmt.Errorf("revision %s: %w", revisionName, err)
		return false, err.Error(), err
	}

	r.logTeardownPhaseResults(revisionName, result)

	if result.IsComplete() {
		return true, fmt.Sprintf("Revision %s: torn down", revisionName), nil
	}

	var message string
	if activePhaseName, ok := result.GetActivePhaseName(); ok {
		message = fmt.Sprintf("Revision %s: tearing down phase %s", revisionName, activePhaseName)
	} else {
		// Probably shouldn't happen?
		message = fmt.Sprintf("Revision %s: waiting for teardown", revisionName)
	}

	return false, message, nil
}

func (r *revisionReconciler) logTeardownPhaseResults(revisionName operatorv1alpha1.RevisionName, result machinery.RevisionTeardownResult) {
	for _, phase := range result.GetPhases() {
		gone := phase.Gone()
		waiting := phase.Waiting()

		// Logging gone objects is currently problematic due to
		// https://github.com/package-operator/boxcutter/issues/497
		// For now we just don't log it because it's misleading.
		/*
			for _, obj := range gone {
				r.log.Info("Object gone",
					"revision", revisionName,
					"phase", phase.GetName(),
					"object", obj.String(),
				)
			}
		*/

		for _, obj := range waiting {
			r.log.Info("Object waiting",
				"revision", revisionName,
				"phase", phase.GetName(),
				"object", obj.String(),
			)
		}

		r.log.Info("Phase teardown result",
			"revision", revisionName,
			"phase", phase.GetName(),
			"complete", phase.IsComplete(),
			"gone", len(gone),
			"waiting", len(waiting),
		)
	}
}

type revisionHandler func(context.Context, []convertedRevision) (bool, []string, []error)

// mergeWithTail merges the results of the head revision with the results of calling the tail handler on the tail revisions.
func mergeWithTail(ctx context.Context, tailHandler revisionHandler, tailRevisions []convertedRevision) func(bool, string, error) (bool, []string, []error) {
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
		// For CRDs, collect the plural mapping for later resolution
		crdGroup, _, _ := unstructured.NestedString(obj.Object, "spec", "group")
		crdKind, _, _ := unstructured.NestedString(obj.Object, "spec", "names", "kind")
		crdResource, _, _ := unstructured.NestedString(obj.Object, "spec", "names", "plural")

		if crdGroup != "" && crdKind != "" && crdResource != "" {
			// Store the CRD's plural for resolution
			r.crdGKResourceMapping[schema.GroupKind{Group: crdGroup, Kind: crdKind}] = crdResource

			// Also collect the CRD instances for must-gather
			r.relatedObjects.Insert(configv1.ObjectReference{
				Group:    crdGroup,
				Resource: crdResource,
			})
		}

	case obj.GetNamespace() == "":
		// Collect intermediate data for non-namespaced objects.
		// Plural resolution happens later using RESTMapper or CRD plurals.
		r.collectedNonNSObjects.Insert(collectedObjectRef{
			gvk:  gvk,
			name: obj.GetName(),
		})
	}
}

// resolveCollectedObjects converts collected intermediate object references to
// final ObjectReferences by resolving correct resource names.
// Resolution strategy:
//  1. If the GVK matches a CRD we're installing, use the plural from the CRD spec
//  2. Otherwise, use RESTMapper to look it up
//  3. If RESTMapper returns NoMatchError for a non-CRD resource, this is a terminal
//     error (manifest references a non-existent resource type)
//
// Returns rest mapper errors. Non-transient rest mapper failure is wrapped as a
// terminal error.
func (r *revisionReconciler) resolveCollectedObjects() error {
	for collectedRef := range r.collectedNonNSObjects {
		var resource string

		// First, check if this is a CRD we're installing
		if crdResource, isCRD := r.crdGKResourceMapping[collectedRef.gvk.GroupKind()]; isCRD {
			// Use the plural from the CRD spec directly. This means we don't
			// need to worry if the CRD is not yet installed.
			resource = crdResource
		} else {
			// Not a CRD from our manifests - look it up with RESTMapper
			mapping, err := r.restMapper.RESTMapping(
				collectedRef.gvk.GroupKind(),
				collectedRef.gvk.Version,
			)
			if err != nil {
				if meta.IsNoMatchError(err) {
					// Resource type doesn't exist - terminal error
					return fmt.Errorf(
						"manifest references non-existent resource type %s (not a CRD in manifests and not found in cluster): %w",
						collectedRef.gvk.String(), reconcile.TerminalError(err))
				}

				// Transient errors are non-terminal
				return fmt.Errorf("failed to resolve plural for %s: %w", collectedRef.gvk.String(), err)
			}

			resource = mapping.Resource.Resource
		}

		// Insert ObjectReference with correct plural
		r.relatedObjects.Insert(configv1.ObjectReference{
			Group:    collectedRef.gvk.Group,
			Resource: resource,
			Name:     collectedRef.name,
		})
	}

	return nil
}

// dynamicRelatedObjects returns the deduped, sorted list of relatedObjects
// collected during revision conversion.
func (r *revisionReconciler) dynamicRelatedObjects() []configv1.ObjectReference {
	objs := r.relatedObjects.UnsortedList()
	slices.SortFunc(objs, compareObjectReference)

	return objs
}
