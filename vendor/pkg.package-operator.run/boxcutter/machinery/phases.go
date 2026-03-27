package machinery

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"pkg.package-operator.run/boxcutter/machinery/types"
	"pkg.package-operator.run/boxcutter/validation"
)

// PhaseEngine groups reconciliation of a list of objects,
// after all of them have passed preflight checks and
// performs probing after the objects have been reconciled.
type PhaseEngine struct {
	objectEngine   objectEngine
	phaseValidator phaseValidator
}

// NewPhaseEngine returns a new PhaseEngine instance.
func NewPhaseEngine(
	objectEngine objectEngine,
	phaseValidator phaseValidator,
) *PhaseEngine {
	return &PhaseEngine{
		objectEngine:   objectEngine,
		phaseValidator: phaseValidator,
	}
}

type phaseValidator interface {
	Validate(
		ctx context.Context,
		phase types.Phase,
		opts ...types.PhaseReconcileOption,
	) error
}

type objectEngine interface {
	Reconcile(
		ctx context.Context,
		revision int64, // Revision number, must start at 1.
		desiredObject Object,
		opts ...types.ObjectReconcileOption,
	) (ObjectResult, error)
	Teardown(
		ctx context.Context,
		revision int64, // Revision number, must start at 1.
		desiredObject Object,
		opts ...types.ObjectTeardownOption,
	) (objectGone bool, err error)
}

// PhaseObject represents an object and it's options.
type PhaseObject struct {
	Object *unstructured.Unstructured
	Opts   []types.ObjectReconcileOption
}

// PhaseTeardownResult interface to access results of phase teardown.
type PhaseTeardownResult interface {
	GetName() string
	// IsComplete returns true when all objects have been deleted,
	// finalizers have been processes and the objects are longer
	// present on the kube-apiserver.
	IsComplete() bool
	// Gone returns a list of objects that have been confirmed
	// to be gone from the kube-apiserver.
	Gone() []types.ObjectRef
	// Waiting returns a list of objects that have yet to be
	// cleaned up on the kube-apiserver.
	Waiting() []types.ObjectRef

	String() string
}

type phaseTeardownResult struct {
	name    string
	gone    []types.ObjectRef
	waiting []types.ObjectRef
}

func (r *phaseTeardownResult) String() string {
	var out strings.Builder
	fmt.Fprintf(&out, "Phase %q\n", r.name)

	if len(r.gone) > 0 {
		fmt.Fprintln(&out, "Gone Objects:")

		for _, gone := range r.gone {
			fmt.Fprintf(&out, "- %s\n", gone.String())
		}
	}

	if len(r.waiting) > 0 {
		fmt.Fprintln(&out, "Waiting Objects:")

		for _, waiting := range r.waiting {
			fmt.Fprintf(&out, "- %s\n", waiting.String())
		}
	}

	return out.String()
}

func (r *phaseTeardownResult) GetName() string {
	return r.name
}

// IsComplete returns true when all objects have been deleted,
// finalizers have been processes and the objects are longer
// present on the kube-apiserver.
func (r *phaseTeardownResult) IsComplete() bool {
	return len(r.waiting) == 0
}

// Gone returns a list of objects that have been confirmed
// to be gone from the kube-apiserver.
func (r *phaseTeardownResult) Gone() []types.ObjectRef {
	return r.gone
}

// Waiting returns a list of objects that have yet to be
// cleaned up on the kube-apiserver.
func (r *phaseTeardownResult) Waiting() []types.ObjectRef {
	return r.waiting
}

// Teardown ensures the given phase is safely removed from the cluster.
func (e *PhaseEngine) Teardown(
	ctx context.Context,
	revision int64,
	phase types.Phase,
	opts ...types.PhaseTeardownOption,
) (PhaseTeardownResult, error) {
	opts = append(opts, phase.GetTeardownOptions()...)

	var options types.PhaseTeardownOptions
	for _, opt := range opts {
		opt.ApplyToPhaseTeardownOptions(&options)
	}

	res := &phaseTeardownResult{name: phase.GetName()}

	objects := phase.GetObjects()
	errs := make([]error, 0, len(objects))

	for _, obj := range objects {
		gone, err := e.objectEngine.Teardown(
			ctx, revision, obj, options.ForObject(obj)...)
		if err != nil {
			err = fmt.Errorf("teardown %s: %w", types.ToObjectRef(obj), err)
			if options.AggregateErrors {
				errs = append(errs, err)
				if apierrors.IsTooManyRequests(err) {
					return res, errors.Join(errs...)
				}

				continue
			} else {
				return res, err
			}
		}

		if gone {
			res.gone = append(res.gone, types.ToObjectRef(obj))
		} else {
			res.waiting = append(res.waiting, types.ToObjectRef(obj))
		}
	}

	return res, errors.Join(errs...)
}

// Reconcile runs actions to bring actual state closer to desired.
func (e *PhaseEngine) Reconcile(
	ctx context.Context,
	revision int64,
	phase types.Phase,
	opts ...types.PhaseReconcileOption,
) (PhaseResult, error) {
	opts = append(opts, phase.GetReconcileOptions()...)

	var options types.PhaseReconcileOptions
	for _, opt := range opts {
		opt.ApplyToPhaseReconcileOptions(&options)
	}

	pres := &phaseResult{
		name: phase.GetName(),
	}

	// Preflight
	err := e.phaseValidator.Validate(ctx, phase, opts...)
	if err != nil {
		var perr validation.PhaseValidationError
		if errors.As(err, &perr) {
			pres.validationError = &perr

			return pres, nil
		}

		return pres, fmt.Errorf("validating: %w", err)
	}

	// Reconcile
	objects := phase.GetObjects()
	errs := make([]error, 0, len(objects))

	for _, obj := range objects {
		ores, err := e.objectEngine.Reconcile(
			ctx, revision, obj, options.ForObject(obj)...)
		if err != nil {
			err = fmt.Errorf("reconciling %s: %w", types.ToObjectRef(obj), err)
			if options.AggregateErrors {
				errs = append(errs, err)
				if apierrors.IsTooManyRequests(err) {
					return pres, errors.Join(errs...)
				}

				continue
			} else {
				return pres, err
			}
		}

		pres.objects = append(pres.objects, ores)
	}

	return pres, errors.Join(errs...)
}

// PhaseResult interface to access results of phase reconcile.
type PhaseResult interface {
	// GetName returns the name of the phase.
	GetName() string
	// GetValidationError returns the preflight validation
	// error, if one was encountered.
	GetValidationError() *validation.PhaseValidationError
	// GetObjects returns results for individual objects.
	GetObjects() []ObjectResult
	// InTransition returns true if the Phase has not yet fully rolled out,
	// if the phase has objects progressed to a new revision or
	// if objects have unresolved conflicts.
	InTransition() bool
	// IsComplete returns true when all objects have
	// successfully been reconciled and pass their probes.
	IsComplete() bool
	// HasProgressed returns true when all objects have been progressed to a newer revision.
	HasProgressed() bool
	String() string
}

// phaseResult contains information of the state of a reconcile operation.
type phaseResult struct {
	name            string
	validationError *validation.PhaseValidationError
	objects         []ObjectResult
}

// GetName returns the name of the phase.
func (r *phaseResult) GetName() string {
	return r.name
}

// GetValidationError returns the preflight validation
// error, if one was encountered.
func (r *phaseResult) GetValidationError() *validation.PhaseValidationError {
	return r.validationError
}

// GetObjects returns results for individual objects.
func (r *phaseResult) GetObjects() []ObjectResult {
	return r.objects
}

// InTransition returns true if the Phase has not yet fully rolled out,
// if the phase has some objects progressed to a new revision or
// if objects have unresolved conflicts.
func (r *phaseResult) InTransition() bool {
	if err := r.GetValidationError(); err != nil {
		return false
	}

	if r.HasProgressed() {
		// If all objects have progressed, we are done transitioning.
		return false
	}

	for _, o := range r.objects {
		switch o.Action() {
		case ActionCollision, ActionProgressed:
			return true
		}
	}

	return false
}

// HasProgressed returns true when all objects have been progressed to a newer revision.
func (r *phaseResult) HasProgressed() bool {
	var numProgressed int

	for _, o := range r.objects {
		if o.Action() == ActionProgressed {
			numProgressed++
		}
	}

	return numProgressed == len(r.objects)
}

// IsComplete returns true when all objects have
// successfully been reconciled and pass their progress probes.
func (r *phaseResult) IsComplete() bool {
	if err := r.GetValidationError(); err != nil {
		return false
	}

	for _, o := range r.objects {
		if !o.IsComplete() {
			return false
		}
	}

	return true
}

func (r *phaseResult) String() string {
	var out strings.Builder
	fmt.Fprintf(&out,
		"Phase %q\nComplete: %t\nIn Transition: %t\n",
		r.name, r.IsComplete(), r.InTransition(),
	)

	if err := r.GetValidationError(); err != nil {
		fmt.Fprintln(&out, "Validation Errors:")

		for _, err := range err.Unwrap() {
			fmt.Fprintf(&out, "- %s\n", err.Error())
		}
	}

	fmt.Fprintln(&out, "Objects:")

	for _, ores := range r.objects {
		fmt.Fprintf(&out, "- %s\n", strings.ReplaceAll(strings.TrimSpace(ores.String()), "\n", "\n  "))
	}

	return out.String()
}
