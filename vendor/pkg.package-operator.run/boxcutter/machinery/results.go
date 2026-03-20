package machinery

import (
	"fmt"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"

	"pkg.package-operator.run/boxcutter/machinery/types"
)

// ObjectResult is the common Result interface for multiple result types.
type ObjectResult interface {
	// Action taken by the reconcile engine.
	Action() Action
	// Object as last seen on the cluster after creation/update.
	Object() Object
	// Probes returns the results from the given object Probes.
	ProbeResults() types.ProbeResultContainer
	// String returns a human readable description of the Result.
	String() string
	// IsComplete returns true when:
	// - the reconciler has been paused and action is "Idle" or "Progressed"
	// - there has been no collision
	// - the progression probe succeeded
	// it returns false when:
	// - the reconciler has been paused and action is not "Idle" or "Progressed"
	// - there has been a collision
	// - the progression probe failed or returned unknown
	IsComplete() bool
	// IsPaused returns true when the WithPaused option has been set.
	IsPaused() bool
}

// ObjectProbeResult records probe results for the object.
type ObjectProbeResult struct {
	Success  bool
	Messages []string
}

var (
	_ ObjectResult = (*ObjectResultCreated)(nil)
	_ ObjectResult = (*ObjectResultUpdated)(nil)
	_ ObjectResult = (*ObjectResultIdle)(nil)
	_ ObjectResult = (*ObjectResultProgressed)(nil)
	_ ObjectResult = (*ObjectResultRecovered)(nil)
	_ ObjectResult = (*ObjectResultCollision)(nil)
)

// ObjectResultCreated is returned when the Object was just created.
type ObjectResultCreated struct {
	obj          Object
	probeResults types.ProbeResultContainer
	options      types.ObjectReconcileOptions
}

func newObjectResultCreated(
	obj Object,
	options types.ObjectReconcileOptions,
) ObjectResult {
	return ObjectResultCreated{
		obj:          obj,
		probeResults: runProbes(obj, options.Probes),
		options:      options,
	}
}

// Action taken by the reconcile engine.
func (r ObjectResultCreated) Action() Action {
	return ActionCreated
}

// Object as last seen on the cluster after creation/update.
func (r ObjectResultCreated) Object() Object {
	return r.obj
}

// IsPaused returns true when the WithPaused option has been set.
func (r ObjectResultCreated) IsPaused() bool {
	return r.options.Paused
}

// IsComplete returns true when:
// - the reconciler has been paused and action is "Idle" or "Progressed"
// - there has been no collision
// - the progression probe succeeded
// it returns false when:
// - the reconciler has been paused and action is not "Idle" or "Progressed"
// - there has been a collision
// - the progression probe failed or returned unknown.
func (r ObjectResultCreated) IsComplete() bool {
	return isComplete(ActionCreated, r.probeResults, r.options)
}

// ProbeResults returns the results from the given object Probe.
func (r ObjectResultCreated) ProbeResults() types.ProbeResultContainer {
	return r.probeResults
}

// String returns a human readable description of the Result.
func (r ObjectResultCreated) String() string {
	return reportStart(r)
}

// ObjectResultUpdated is returned when the object is updated.
type ObjectResultUpdated struct {
	normalResult
}

func newObjectResultUpdated(
	obj Object,
	diverged CompareResult,
	options types.ObjectReconcileOptions,
) ObjectResult {
	return ObjectResultUpdated{
		normalResult: newNormalObjectResult(ActionUpdated, obj, diverged, options),
	}
}

// ObjectResultProgressed is returned when the object has been progressed to a newer revision.
type ObjectResultProgressed struct {
	normalResult
}

func newObjectResultProgressed(
	obj Object,
	diverged CompareResult,
	options types.ObjectReconcileOptions,
) ObjectResult {
	return ObjectResultProgressed{
		normalResult: newNormalObjectResult(ActionProgressed, obj, diverged, options),
	}
}

// ObjectResultIdle is returned when nothing was done.
type ObjectResultIdle struct {
	normalResult
}

func newObjectResultIdle(
	obj Object,
	diverged CompareResult,
	options types.ObjectReconcileOptions,
) ObjectResult {
	return ObjectResultIdle{
		normalResult: newNormalObjectResult(ActionIdle, obj, diverged, options),
	}
}

// ObjectResultRecovered is returned when the object had to be reset after conflicting with another actor.
type ObjectResultRecovered struct {
	normalResult
}

func newObjectResultRecovered(
	obj Object,
	diverged CompareResult,
	options types.ObjectReconcileOptions,
) ObjectResult {
	return ObjectResultRecovered{
		normalResult: newNormalObjectResult(ActionRecovered, obj, diverged, options),
	}
}

type normalResult struct {
	action        Action
	obj           Object
	probeResults  types.ProbeResultContainer
	compareResult CompareResult
	options       types.ObjectReconcileOptions
}

func newNormalObjectResult(
	action Action,
	obj Object,
	compResult CompareResult,
	options types.ObjectReconcileOptions,
) normalResult {
	if action == ActionCreated {
		panic("use newObjectResultCreated, instead")
	}

	return normalResult{
		obj:           obj,
		action:        action,
		probeResults:  runProbes(obj, options.Probes),
		compareResult: compResult,
		options:       options,
	}
}

// Action taken by the reconcile engine.
func (r normalResult) Action() Action {
	return r.action
}

// Object as last seen on the cluster after creation/update.
func (r normalResult) Object() Object {
	return r.obj
}

// CompareResult returns the results from checking the
// actual object on the cluster against the desired spec.
// Contains informations about differences that had to be reconciled.
func (r normalResult) CompareResult() CompareResult {
	return r.compareResult
}

// ProbeResults returns the results from the given object Probe.
func (r normalResult) ProbeResults() types.ProbeResultContainer {
	return r.probeResults
}

// IsPaused returns true when the WithPaused option has been set.
func (r normalResult) IsPaused() bool {
	return r.options.Paused
}

// IsComplete returns true when:
// - the operation has not been paused
// - there has been no collision
// - the progression probe succeeded.
func (r normalResult) IsComplete() bool {
	return isComplete(r.action, r.probeResults, r.options)
}

// String returns a human readable description of the Result.
func (r normalResult) String() string {
	msg := reportStart(r)

	return msg + r.compareResult.String()
}

// ObjectResultCollision is returned when conflicting with an existing object.
type ObjectResultCollision struct {
	normalResult
	// conflictingOwner is provided when Refusing due to Collision.
	conflictingOwner *metav1.OwnerReference
}

// ConflictingOwner Conflicting owner if Action == RefusingConflict.
func (r ObjectResultCollision) ConflictingOwner() (*metav1.OwnerReference, bool) {
	return r.conflictingOwner, r.conflictingOwner != nil
}

// Success returns true when the operation is considered successful.
// Operations are considered a success, when the object reflects desired state,
// is owned by the right controller and passes the given probe.
func (r ObjectResultCollision) Success() bool {
	return false
}

// String returns a human readable description of the Result.
func (r ObjectResultCollision) String() string {
	msg := r.normalResult.String()
	msg += fmt.Sprintf("Conflicting Owner: %s\n", r.conflictingOwner.String())

	return msg
}

func newObjectResultConflict(
	obj Object,
	diverged CompareResult,
	conflictingOwner *metav1.OwnerReference,
	options types.ObjectReconcileOptions,
) ObjectResult {
	return ObjectResultCollision{
		normalResult: newNormalObjectResult(
			ActionCollision,
			obj, diverged, options,
		),
		conflictingOwner: conflictingOwner,
	}
}

// Action describes the taken reconciliation action.
type Action string

const (
	// ActionCreated indicates that the object has been created to restore desired state.
	ActionCreated Action = "Created"
	// ActionUpdated indicates that the object has been updated to action on a change in desired state.
	ActionUpdated Action = "Updated"
	// ActionRecovered indicates that the object has been updated to recover values to
	// reflect desired state after interference from another actor of the system.
	ActionRecovered Action = "Recovered"
	// ActionProgressed indicates that the object progressed to newer revision.
	ActionProgressed Action = "Progressed"
	// ActionIdle indicates that no action was necessary. -> NoOp.
	ActionIdle Action = "Idle"
	// ActionCollision indicates aking actions was refused due to a collision with an existing object.
	ActionCollision Action = "Collision"
)

func reportStart(or ObjectResult) string {
	obj := or.Object()
	if err := ensureGVKIsSet(obj, scheme.Scheme); err != nil {
		panic(err)
	}

	actionStr := "Action"
	if or.IsPaused() {
		actionStr = "Action (PAUSED)"
	}

	var out strings.Builder

	gvk := obj.GetObjectKind().GroupVersionKind()
	fmt.Fprintf(&out,
		"Object %s.%s %s/%s\n"+
			`%s: %q`+"\n",
		gvk.Kind, gvk.GroupVersion().String(),
		obj.GetNamespace(), obj.GetName(),
		actionStr, or.Action(),
	)

	probes := or.ProbeResults()
	probeTypes := make([]string, 0, len(probes))

	for k := range probes {
		probeTypes = append(probeTypes, k)
	}

	sort.Strings(probeTypes)

	if len(probeTypes) > 0 {
		fmt.Fprintln(&out, "Probes:")
	}

	for _, probeType := range probeTypes {
		probeRes := probes[probeType]
		switch probeRes.Status {
		case types.ProbeStatusTrue:
			fmt.Fprintf(&out, "- %s: Succeeded\n", probeType)
		case types.ProbeStatusFalse:
			fmt.Fprintf(&out, "- %s: Failed\n", probeType)
		case types.ProbeStatusUnknown:
			fmt.Fprintf(&out, "- %s: Unknown\n", probeType)
		}

		for _, m := range probeRes.Messages {
			fmt.Fprintf(&out, "  - %s\n", m)
		}
	}

	return out.String()
}

func runProbes(obj Object, probes map[string]types.Prober) types.ProbeResultContainer {
	results := types.ProbeResultContainer{}

	for t, probe := range probes {
		results[t] = probe.Probe(obj)
	}

	return results
}

// isComplete returns true when:
// - the reconciler has been paused and action is "Idle" or "Progressed"
// - there has been no collision
// - the progression probe succeeded
// it returns false when:
// - the reconciler has been paused and action is not "Idle" or "Progressed"
// - there has been a collision
// - the progression probe failed or returned unknown.
func isComplete(
	action Action,
	probeResults types.ProbeResultContainer,
	options types.ObjectReconcileOptions,
) bool {
	if action == ActionCollision {
		// Collisions always report as incomplete.
		return false
	}

	if options.Paused {
		switch action {
		case ActionIdle, ActionProgressed:
			// Even though we are paused, there the object is ok -> "Idle" or
			// no longer "our problem" -> "Progressed"

		default:
			// If Paused:
			// Action == "Created": the object has NOT been created
			// Action == "Updated": the object has NOT been updated
			// Action == "Recovered": the object has NOT been recovered
			return false
		}
	}

	if options.Probes[types.ProgressProbeType] == nil {
		// no probe defined, skip
		return true
	}

	// Only check the progress probe and only count explicit true for completeness:
	r := probeResults.Type(types.ProgressProbeType)

	return r.Status == types.ProbeStatusTrue
}
