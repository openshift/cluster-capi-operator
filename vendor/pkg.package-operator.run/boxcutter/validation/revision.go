package validation

import (
	"context"
	"errors"
	"slices"

	"pkg.package-operator.run/boxcutter/machinery/types"
)

// RevisionValidator runs basic validation against
// all phases and objects making up a revision.
//
// It performes less detailed checks than ObjectValidator or PhaseValidator
// as detailed checks (using e.g. dry run) should only be run right before
// a phase is installed to prevent false positives.
type RevisionValidator struct{}

// NewRevisionValidator returns a new RevisionValidator instance.
func NewRevisionValidator() *RevisionValidator {
	return &RevisionValidator{}
}

// Validate a revision compromising of multiple phases.
func (v *RevisionValidator) Validate(_ context.Context, rev types.Revision) error {
	pvs := staticValidateMultiplePhases(rev.GetPhases()...)

	return NewRevisionValidationError(
		rev.GetName(), rev.GetRevisionNumber(),
		pvs...,
	)
}

func staticValidateMultiplePhases(phases ...types.Phase) []PhaseValidationError {
	dups := checkForObjectDuplicates(phases...)

	var phaseErrors []PhaseValidationError

	for _, phase := range phases {
		var objectErrors []ObjectValidationError

		for _, obj := range phase.GetObjects() {
			oe := NewObjectValidationError(
				types.ToObjectRef(obj),
				validateObjectMetadata(obj)...,
			)
			if oe == nil {
				continue
			}

			//nolint:errorlint
			objectErrors = append(objectErrors, *oe.(*ObjectValidationError))
		}

		for _, dup := range dups {
			var de PhaseObjectDuplicationError
			if errors.As(dup, &de) && slices.Contains(de.PhaseNames, phase.GetName()) {
				objectErrors = append(objectErrors, dup)
			}
		}

		pe := NewPhaseValidationError(
			phase.GetName(), validatePhaseName(phase), objectErrors...)
		if pe == nil {
			continue
		}

		// NewPhaseValidationError only returns *PhaseValidationError, but it has to return error
		// due to golangs interface comparison where (*PhaseValidation)(nil) != (error)(nil)
		//nolint:errorlint
		phaseErrors = append(phaseErrors, *pe.(*PhaseValidationError))
	}

	return phaseErrors
}
