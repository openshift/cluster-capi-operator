package machinery

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateCollisionError is returned when boxcutter tries to create an object,
// but it already exists. \
// This happens when another actor has created the object and caches are slow,
// or the colliding object is excluded via cache selectors.
type CreateCollisionError struct {
	object client.Object
	msg    string
}

// NewCreateCollisionError creates a new CreateCollisionError.
func NewCreateCollisionError(obj client.Object, msg string) *CreateCollisionError {
	return &CreateCollisionError{
		object: obj,
		msg:    msg,
	}
}

// Object is the object reference that caused the error.
func (e CreateCollisionError) Object() client.Object {
	return e.object
}

// Error implements golangs error interface.
func (e CreateCollisionError) Error() string {
	return fmt.Sprintf("%s: %s", e.object, e.msg)
}

// UnsupportedApplyConfigurationError is raised when the given object is typed but not a ApplyConfiguration.
// The way Go treats default values makes it impossible to decide between default values and explicitly not supplied fields.
// Either use unstructured.Unstructured or ApplyConfiguration to get around the problem.
type UnsupportedApplyConfigurationError struct {
	object Object
}

// NewUnsupportedApplyConfigurationError creates a new UnsupportedApplyConfigurationError.
func NewUnsupportedApplyConfigurationError(obj Object) *UnsupportedApplyConfigurationError {
	return &UnsupportedApplyConfigurationError{
		object: obj,
	}
}

// Error implements golangs error interface.
func (e UnsupportedApplyConfigurationError) Error() string {
	return fmt.Sprintf("does not support ApplyConfiguration: %T", e.object)
}
