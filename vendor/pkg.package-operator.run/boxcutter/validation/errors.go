package validation

import (
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"

	"pkg.package-operator.run/boxcutter/machinery/types"
)

// ObjectValidationError gathers multiple errors related to an object.
type ObjectValidationError struct {
	// ObjectRef references the object causing the error.
	ObjectRef types.ObjectRef
	// Errors is a list of errors ocurring in the context of the object.
	Errors []error
}

// NewObjectValidationError returns a new ObjectValidationError.
// Returns nil when no errors are given.
func NewObjectValidationError(
	objRef types.ObjectRef,
	errs ...error,
) error {
	if len(errs) == 0 {
		return nil
	}

	return &ObjectValidationError{
		ObjectRef: objRef,
		Errors:    errs,
	}
}

func (e ObjectValidationError) stringStructure() any {
	msgs := make([]string, len(e.Errors))
	for i, err := range e.Errors {
		msgs[i] = err.Error()
	}

	return map[string][]string{
		e.ObjectRef.String(): msgs,
	}
}

// String returns a human readable report of all error messages
// and the context the error was encountered in, if available.
func (e ObjectValidationError) String() string {
	b, _ := yaml.Marshal(e.stringStructure())

	return string(b)
}

// Error implements the error interface.
func (e ObjectValidationError) Error() string {
	msgs := make([]string, 0, len(e.Errors))
	for _, e := range e.Errors {
		msgs = append(msgs, e.Error())
	}

	return e.ObjectRef.String() + ": " + strings.Join(msgs, ", ")
}

// Unwrap implements the errors unwrap interface for errors.Is and errors.As.
func (e ObjectValidationError) Unwrap() []error {
	return e.Errors
}

// PhaseValidationError gathers multiple validation errors from all objects within a phase.
type PhaseValidationError struct {
	// Name of the Phase the validation error was raised for.
	PhaseName string
	// Validation error relating to the phase itself.
	PhaseError error
	// Object-scoped errors
	Objects []ObjectValidationError
}

// NewPhaseValidationError returns a new PhaseValidationError.
// Returns nil when no phase and object errors are given.
func NewPhaseValidationError(
	phaseName string, phaseErr error, oErrs ...ObjectValidationError,
) error {
	if phaseErr == nil && len(oErrs) == 0 {
		return nil
	}

	return &PhaseValidationError{
		PhaseName:  phaseName,
		PhaseError: phaseErr,
		Objects:    oErrs,
	}
}

func (e PhaseValidationError) stringStructure() any {
	msgsL := len(e.Objects)
	if e.PhaseError != nil {
		msgsL++
	}

	msgs := make([]any, 0, msgsL)
	if e.PhaseError != nil {
		msgs = append(msgs, e.PhaseError.Error())
	}

	for _, obj := range e.Objects {
		msgs = append(msgs, obj.stringStructure())
	}

	return map[string][]any{
		e.PhaseName: msgs,
	}
}

// String returns a human readable report of all error messages
// and the context the error was encountered in, if available.
func (e PhaseValidationError) String() string {
	b, _ := yaml.Marshal(e.stringStructure())

	return string(b)
}

// Error implements the error interface.
func (e PhaseValidationError) Error() string {
	msg := fmt.Sprintf("phase %q: ", e.PhaseName)
	if e.PhaseError != nil {
		msg += e.PhaseError.Error()
	}

	objmsgs := make([]string, 0, len(e.Objects))
	for _, e := range e.Objects {
		objmsgs = append(objmsgs, e.Error())
	}

	if len(e.Objects) > 0 {
		if e.PhaseError != nil {
			msg += ", "
		}

		return msg + strings.Join(objmsgs, ", ")
	}

	return msg
}

// Unwrap implements the errors unwrap interface for errors.Is and errors.As.
func (e PhaseValidationError) Unwrap() []error {
	l := len(e.Objects)
	if e.PhaseError != nil {
		l++
	}

	errs := make([]error, 0, l)
	if e.PhaseError != nil {
		errs = append(errs, e.PhaseError)
	}

	for _, o := range e.Objects {
		errs = append(errs, o)
	}

	return errs
}

// RevisionValidationError gathers all validation errors across multiple phases.
type RevisionValidationError struct {
	RevisionName   string
	RevisionNumber int64
	Phases         []PhaseValidationError
}

// NewRevisionValidationError returns a new RevisionValidationError.
// Returns nil when no phase errors are given.
func NewRevisionValidationError(
	revisionName string, revisionNumber int64,
	phaseErrs ...PhaseValidationError,
) error {
	if len(phaseErrs) == 0 {
		return nil
	}

	return &RevisionValidationError{
		RevisionName:   revisionName,
		RevisionNumber: revisionNumber,
		Phases:         phaseErrs,
	}
}

func (e RevisionValidationError) stringStructure() any {
	msgs := make([]any, 0, len(e.Phases))
	for _, obj := range e.Phases {
		msgs = append(msgs, obj.stringStructure())
	}

	return map[string][]any{
		fmt.Sprintf("%s (%d)", e.RevisionName, e.RevisionNumber): msgs,
	}
}

// String returns a human readable report of all error messages
// and the context the error was encountered in, if available.
func (e RevisionValidationError) String() string {
	b, _ := yaml.Marshal(e.stringStructure())

	return string(b)
}

// Error implements the error interface.
func (e RevisionValidationError) Error() string {
	msg := fmt.Sprintf("revision %q (%d): ", e.RevisionName, e.RevisionNumber)

	pmsgs := make([]string, 0, len(e.Phases))
	for _, e := range e.Phases {
		pmsgs = append(pmsgs, e.Error())
	}

	return msg + strings.Join(pmsgs, ", ")
}

// Unwrap implements the errors unwrap interface for errors.Is and errors.As.
func (e RevisionValidationError) Unwrap() []error {
	errs := make([]error, 0, len(e.Phases))
	for _, p := range e.Phases {
		errs = append(errs, p)
	}

	return errs
}
