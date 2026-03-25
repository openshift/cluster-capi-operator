package validation

import (
	"context"
	"errors"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"pkg.package-operator.run/boxcutter/machinery/types"
)

// PhaseValidator valiates a phase with all contained objects.
// Intended as a preflight check be ensure a higher success chance when
// rolling out the phase and prevent partial application of phases.
type PhaseValidator struct {
	*ObjectValidator
}

// NewClusterPhaseValidator returns an PhaseValidator for cross-cluster deployments.
func NewClusterPhaseValidator(
	restMapper restMapper,
	writer client.Writer,
) *PhaseValidator {
	return &PhaseValidator{
		ObjectValidator: NewClusterObjectValidator(restMapper, writer),
	}
}

// NewNamespacedPhaseValidator returns an ObjecctValidator for single-namespace deployments.
func NewNamespacedPhaseValidator(
	restMapper restMapper,
	writer client.Writer,
) *PhaseValidator {
	return &PhaseValidator{
		ObjectValidator: NewNamespacedObjectValidator(restMapper, writer),
	}
}

// Validate runs validation of the phase and its objects.
func (v *PhaseValidator) Validate(
	ctx context.Context, phase types.Phase, opts ...types.PhaseReconcileOption,
) error {
	var options types.PhaseReconcileOptions
	for _, opt := range opts {
		opt.ApplyToPhaseReconcileOptions(&options)
	}

	phaseError := validatePhaseName(phase)

	var (
		objectErrors []ObjectValidationError
		errs         []error
	)

	for _, obj := range phase.GetObjects() {
		err := v.ObjectValidator.Validate(ctx, obj, options.ForObject(obj)...)
		if err == nil {
			continue
		}

		var oerr ObjectValidationError
		if errors.As(err, &oerr) {
			objectErrors = append(objectErrors, oerr)
		} else {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		// unknown errors duing ObjectValidation
		return errors.Join(errs...)
	}

	objectErrors = append(objectErrors, checkForObjectDuplicates(phase)...)

	return NewPhaseValidationError(
		phase.GetName(), phaseError, compactObjectViolations(objectErrors)...)
}

// PhaseNameInvalidError is returned when the phase name does not validate.
type PhaseNameInvalidError struct {
	// Name of the phase.
	PhaseName string
	// Error messages describing why the phase name is invalid.
	ErrorMessages []string
}

// Error implements the error interface.
func (e PhaseNameInvalidError) Error() string {
	return "phase name invalid: " + strings.Join(e.ErrorMessages, ", ")
}

func validatePhaseName(phase types.Phase) error {
	if errMsgs := phaseNameValid(phase.GetName()); len(errMsgs) > 0 {
		return PhaseNameInvalidError{
			PhaseName:     phase.GetName(),
			ErrorMessages: errMsgs,
		}
	}

	return nil
}

func phaseNameValid(phaseName string) (errs []string) {
	return validation.IsDNS1035Label(phaseName)
}

// PhaseObjectDuplicationError is returned when an object is present
// multiple times in a phase or across multiple phases.
type PhaseObjectDuplicationError struct {
	PhaseNames []string
}

func (e PhaseObjectDuplicationError) Error() string {
	return "duplicate object found in phases: " + strings.Join(e.PhaseNames, ", ")
}

func checkForObjectDuplicates(phases ...types.Phase) []ObjectValidationError {
	// remember unique objects and the phase we found them first in.
	uniqueObjectsInPhase := map[types.ObjectRef]string{}
	conflicts := map[types.ObjectRef]map[string]struct{}{}

	for _, phase := range phases {
		for _, obj := range phase.GetObjects() {
			ref := types.ToObjectRef(obj)

			otherPhase, ok := uniqueObjectsInPhase[ref]
			if !ok {
				uniqueObjectsInPhase[ref] = phase.GetName()

				continue
			}

			// Conflict!
			if _, ok := conflicts[ref]; !ok {
				conflicts[ref] = map[string]struct{}{
					otherPhase: {},
				}
			}

			conflicts[ref][phase.GetName()] = struct{}{}
		}
	}

	ovs := make([]ObjectValidationError, 0, len(conflicts))

	for objRef, phasesMap := range conflicts {
		phases := make([]string, 0, len(phasesMap))
		for p := range phasesMap {
			phases = append(phases, p)
		}

		slices.Sort(phases)
		ov := ObjectValidationError{
			ObjectRef: objRef,
			Errors: []error{
				PhaseObjectDuplicationError{
					PhaseNames: phases,
				},
			},
		}
		ovs = append(ovs, ov)
	}

	return ovs
}

// merges ObjectViolations targeting the same object.
func compactObjectViolations(ovs []ObjectValidationError) []ObjectValidationError {
	uniqueOVs := map[types.ObjectRef][]error{}
	for _, ov := range ovs {
		uniqueOVs[ov.ObjectRef] = append(
			uniqueOVs[ov.ObjectRef], ov.Errors...)
	}

	out := make([]ObjectValidationError, 0, len(uniqueOVs))
	for oref, errs := range uniqueOVs {
		out = append(out, ObjectValidationError{
			ObjectRef: oref, Errors: errs,
		})
	}

	return out
}
