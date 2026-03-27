package machinery

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"pkg.package-operator.run/boxcutter/machinery/types"
	"pkg.package-operator.run/boxcutter/validation"
)

// RevisionEngine manages rollout and teardown of multiple phases.
type RevisionEngine struct {
	phaseEngine       phaseEngine
	revisionValidator revisionValidator
	writer            client.Writer
}

// NewRevisionEngine returns a new RevisionEngine instance.
func NewRevisionEngine(
	phaseEngine phaseEngine,
	revisionValidator revisionValidator,
	client client.Writer,
) *RevisionEngine {
	return &RevisionEngine{
		phaseEngine:       phaseEngine,
		revisionValidator: revisionValidator,
		writer:            client,
	}
}

type revisionValidator interface {
	Validate(_ context.Context, rev types.Revision) error
}

type phaseEngine interface {
	Reconcile(
		ctx context.Context,
		revision int64,
		phase types.Phase,
		opts ...types.PhaseReconcileOption,
	) (PhaseResult, error)
	Teardown(
		ctx context.Context,
		revision int64,
		phase types.Phase,
		opts ...types.PhaseTeardownOption,
	) (PhaseTeardownResult, error)
}

// RevisionResult holds details about the revision reconciliation run.
type RevisionResult interface {
	// GetValidationError returns the preflight validation error, if one was encountered.
	// Revision preflight checks are not as extensive as phase-preflight checks.
	GetValidationError() *validation.RevisionValidationError
	// GetPhases returns results for individual phases.
	GetPhases() []PhaseResult
	// InTransition returns true if the Phase has not yet fully rolled out,
	// if the phase has objects progressed to a new revision or
	// if objects have unresolved conflicts.
	InTransition() bool
	// IsComplete returns true when all objects have
	// successfully been reconciled and pass their probes.
	IsComplete() bool
	// HasProgressed returns true when all phases have been progressed to a newer revision.
	HasProgressed() bool
	String() string
}

type revisionResult struct {
	phases          []string
	phasesResults   []PhaseResult
	validationError *validation.RevisionValidationError
}

// GetValidationError returns the preflight validation
// error, if one was encountered.
func (r *revisionResult) GetValidationError() *validation.RevisionValidationError {
	return r.validationError
}

// InTransition returns true if the Phase has not yet fully rolled out,
// if the phase has objects progressed to a new revision or
// if objects have unresolved conflicts.
func (r *revisionResult) InTransition() bool {
	for _, p := range r.phasesResults {
		if p.InTransition() {
			return true
		}
	}

	if r.validationError != nil {
		return false
	}

	if len(r.phasesResults) < len(r.phases) {
		// not all phases have been acted on.
		return true
	}

	return false
}

// HasProgressed returns true when all phases have been progressed to a newer revision.
func (r *revisionResult) HasProgressed() bool {
	var numProgressed int

	for _, p := range r.phasesResults {
		if p.HasProgressed() {
			numProgressed++
		}
	}

	return numProgressed == len(r.phases)
}

// IsComplete returns true when all phases have
// successfully been reconciled and pass their probes.
func (r *revisionResult) IsComplete() bool {
	if len(r.phasesResults) < len(r.phases) {
		// not all phases have been acted on.
		return false
	}

	for _, p := range r.phasesResults {
		if !p.IsComplete() {
			return false
		}
	}

	return true
}

// GetPhases returns results for individual phases.
func (r *revisionResult) GetPhases() []PhaseResult {
	return r.phasesResults
}

func (r *revisionResult) String() string {
	var out strings.Builder
	fmt.Fprintf(&out,
		"Revision\nComplete: %t\nIn Transition: %t\n",
		r.IsComplete(), r.InTransition(),
	)

	if err := r.GetValidationError(); err != nil {
		fmt.Fprintln(&out, "Validation Errors:")

		for _, err := range err.Unwrap() {
			fmt.Fprintf(&out, "- %s\n", err.Error())
		}
	}

	phasesWithResults := map[string]struct{}{}

	fmt.Fprintln(&out, "Phases:")

	for _, ores := range r.phasesResults {
		phasesWithResults[ores.GetName()] = struct{}{}
		fmt.Fprintf(&out, "- %s\n", strings.TrimSpace(strings.ReplaceAll(ores.String(), "\n", "\n  ")))
	}

	for _, p := range r.phases {
		if _, ok := phasesWithResults[p]; ok {
			continue
		}

		fmt.Fprintf(&out, "- Phase %q (Pending)\n", p)
	}

	return out.String()
}

// Reconcile runs actions to bring actual state closer to desired.
func (re *RevisionEngine) Reconcile(
	ctx context.Context, rev types.Revision,
	opts ...types.RevisionReconcileOption,
) (RevisionResult, error) {
	var options types.RevisionReconcileOptions
	for _, opt := range append(opts, rev.GetReconcileOptions()...) {
		opt.ApplyToRevisionReconcileOptions(&options)
	}

	rres := &revisionResult{}
	for _, p := range rev.GetPhases() {
		rres.phases = append(rres.phases, p.GetName())
	}

	// Preflight
	if err := re.revisionValidator.Validate(ctx, rev); err != nil {
		var rerr *validation.RevisionValidationError
		if errors.As(err, &rerr) {
			rres.validationError = rerr

			return rres, nil
		}

		return rres, fmt.Errorf("validating: %w", err)
	}

	// Reconcile
	for _, phase := range rev.GetPhases() {
		pres, err := re.phaseEngine.Reconcile(
			ctx, rev.GetRevisionNumber(),
			phase, options.ForPhase(phase.GetName())...)
		if err != nil {
			return rres, fmt.Errorf("reconciling object: %w", err)
		}

		rres.phasesResults = append(rres.phasesResults, pres)
		if !pres.IsComplete() {
			// Wait
			return rres, nil
		}
	}

	return rres, nil
}

// RevisionTeardownResult holds the results of a Teardown operation.
type RevisionTeardownResult interface {
	// GetPhases returns results for individual phases.
	GetPhases() []PhaseTeardownResult
	// IsComplete returns true when all objects have been deleted,
	// finalizers have been processes and the objects are longer
	// present on the kube-apiserver.
	IsComplete() bool
	// GetWaitingPhaseNames returns a list of phase names waiting
	// to be torn down.
	GetWaitingPhaseNames() []string
	// GetActivePhaseName returns the name of the phase that is
	// currently being torn down (e.g. waiting on finalizers).
	// Second return is false when no phase is active.
	GetActivePhaseName() (string, bool)
	// GetGonePhaseNames returns a list of phase names already processed.
	GetGonePhaseNames() []string
	// String returns a human readable report.
	String() string
}

type revisionTeardownResult struct {
	phases  []PhaseTeardownResult
	active  string
	waiting []string
	gone    []string
}

// GetPhases returns results for individual phases.
func (r *revisionTeardownResult) GetPhases() []PhaseTeardownResult {
	return r.phases
}

// IsComplete returns true when all objects have been deleted,
// finalizers have been processes and the objects are longer
// present on the kube-apiserver.
func (r *revisionTeardownResult) IsComplete() bool {
	return len(r.waiting) == 0 && len(r.active) == 0
}

// GetWaitingPhaseNames returns a list of phase names waiting
// to be torn down.
func (r *revisionTeardownResult) GetWaitingPhaseNames() []string {
	return r.waiting
}

// GetActivePhaseName returns the name of the phase that is
// currently being torn down (e.g. waiting on finalizers).
// Second return is false when no phase is active.
func (r *revisionTeardownResult) GetActivePhaseName() (string, bool) {
	return r.active, len(r.active) > 0
}

// GetGonePhaseNames returns a list of phase names already processed.
func (r *revisionTeardownResult) GetGonePhaseNames() []string {
	return r.gone
}

func (r *revisionTeardownResult) String() string {
	var out strings.Builder
	fmt.Fprintln(&out, "Revision Teardown")

	if len(r.active) > 0 {
		fmt.Fprintf(&out, "Active: %s\n", r.active)
	}

	if len(r.waiting) > 0 {
		fmt.Fprintln(&out, "Waiting Phases:")

		for _, waiting := range r.waiting {
			fmt.Fprintf(&out, "- %s\n", waiting)
		}
	}

	if len(r.gone) > 0 {
		fmt.Fprintln(&out, "Gone Phases:")

		for _, gone := range r.gone {
			fmt.Fprintf(&out, "- %s\n", gone)
		}
	}

	phasesWithResults := map[string]struct{}{}

	fmt.Fprintln(&out, "Phases:")

	for _, ores := range r.phases {
		phasesWithResults[ores.GetName()] = struct{}{}
		fmt.Fprintf(&out, "- %s\n", strings.TrimSpace(strings.ReplaceAll(ores.String(), "\n", "\n  ")))
	}

	return out.String()
}

// Teardown ensures the given revision is safely removed from the cluster.
func (re *RevisionEngine) Teardown(
	ctx context.Context, rev types.Revision,
	opts ...types.RevisionTeardownOption,
) (RevisionTeardownResult, error) {
	var options types.RevisionTeardownOptions
	for _, opt := range append(opts, rev.GetTeardownOptions()...) {
		opt.ApplyToRevisionTeardownOptions(&options)
	}

	res := &revisionTeardownResult{}

	waiting := map[string]struct{}{}
	for _, p := range rev.GetPhases() {
		waiting[p.GetName()] = struct{}{}
	}

	// Phases should be torn down in reverse.
	reversedPhases := slices.Clone(rev.GetPhases())
	slices.Reverse(reversedPhases)

	for _, p := range reversedPhases {
		// Phase is no longer waiting.
		delete(waiting, p.GetName())
		res.active = p.GetName()

		pres, err := re.phaseEngine.Teardown(
			ctx, rev.GetRevisionNumber(),
			p, options.ForPhase(p.GetName())...)
		if err != nil {
			return nil, fmt.Errorf("teardown phase: %w", err)
		}

		res.phases = append(res.phases, pres)
		if pres.IsComplete() {
			res.gone = append(res.gone, p.GetName())

			continue
		}

		// record other phases as waiting in normal order.
		for _, p := range rev.GetPhases() {
			if _, ok := waiting[p.GetName()]; ok {
				res.waiting = append(res.waiting, p.GetName())
			}
		}

		slices.Reverse(res.gone)

		return res, nil
	}

	slices.Reverse(res.gone)
	res.active = ""

	return res, nil
}
