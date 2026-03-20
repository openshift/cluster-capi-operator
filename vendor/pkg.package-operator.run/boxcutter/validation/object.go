package validation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bctypes "pkg.package-operator.run/boxcutter/machinery/types"
)

type restMapper interface {
	RESTMapping(gk schema.GroupKind, versions ...string) (
		*meta.RESTMapping, error)
}

// ObjectValidator validates objects for structural,
// validation or permission scope issues.
type ObjectValidator struct {
	restMapper restMapper
	writer     client.Writer

	// Allows creating objects in namespaces different to Owner.
	allowNamespaceEscalation bool
}

// NewClusterObjectValidator returns an ObjectValidator for cross-cluster deployments.
func NewClusterObjectValidator(
	restMapper restMapper,
	writer client.Writer,
) *ObjectValidator {
	return &ObjectValidator{
		restMapper: restMapper,
		writer:     writer,

		allowNamespaceEscalation: true,
	}
}

// NewNamespacedObjectValidator returns an ObjecctValidator for single-namespace deployments.
func NewNamespacedObjectValidator(
	restMapper restMapper,
	writer client.Writer,
) *ObjectValidator {
	return &ObjectValidator{
		restMapper: restMapper,
		writer:     writer,
	}
}

// Validate validates the given object.
// The function returns nil, if no validation errors where found.
// It returns an ObjectValidationError when it was successfully able to validate the Object.
// It returns a different error when unable to validate the object.
func (d *ObjectValidator) Validate(
	ctx context.Context,
	obj client.Object,
	opts ...bctypes.ObjectReconcileOption,
) error {
	var options bctypes.ObjectReconcileOptions
	for _, opt := range opts {
		opt.ApplyToObjectReconcileOptions(&options)
	}

	// Static metadata validation.
	errs := validateObjectMetadata(obj)

	if options.Owner != nil && !d.allowNamespaceEscalation {
		// Ensure we are not leaving the namespace we are operating in.
		if err := validateNamespace(
			d.restMapper, options.Owner.GetNamespace(), obj,
		); err != nil {
			errs = append(errs, err)
			// we don't want to do a dry-run when this already fails.
			return NewObjectValidationError(bctypes.ToObjectRef(obj), errs...)
		}
	}

	// Dry run against API server to catch any other surprises.
	err := validateDryRun(ctx, d.writer, obj)
	drve := DryRunValidationError{}

	if errors.As(err, &drve) {
		errs = append(errs, drve)

		return NewObjectValidationError(bctypes.ToObjectRef(obj), errs...)
	}

	return err
}

// MustBeNamespaceScopedResourceError is returned when a cluster-scoped
// resource is used in a namespaced context.
type MustBeNamespaceScopedResourceError struct{}

// Error implements the error interface.
func (e MustBeNamespaceScopedResourceError) Error() string {
	return "object must be namespace-scoped"
}

// MustBeInNamespaceError is returned when an object is in the wrong namespace.
type MustBeInNamespaceError struct {
	ExpectedNamespace, ActualNamespace string
}

// Error implements the error interface.
func (e MustBeInNamespaceError) Error() string {
	return fmt.Sprintf("object must be in namespace %q, actual %q", e.ExpectedNamespace, e.ActualNamespace)
}

// validates the given object is placed in the given namespace.
func validateNamespace(
	restMapper restMapper,
	namespace string,
	obj client.Object,
) error {
	// shortcut if Namespaces are not limited.
	if len(namespace) == 0 {
		return nil
	}

	gvk := obj.GetObjectKind().GroupVersionKind()

	mapping, err := restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if meta.IsNoMatchError(err) {
		// API does not exist in the cluster.
		return err
	}

	switch mapping.Scope {
	case meta.RESTScopeRoot:
		return MustBeNamespaceScopedResourceError{}

	case meta.RESTScopeNamespace:
		if obj.GetNamespace() == namespace {
			return nil
		}

		return MustBeInNamespaceError{
			ExpectedNamespace: namespace,
			ActualNamespace:   obj.GetNamespace(),
		}
	}

	panic(fmt.Sprintf("unexpected REST Mapping Scope %q", mapping.Scope))
}

// DryRunValidationError is returned for APIStatus codes indicating an issue with the object.
type DryRunValidationError struct {
	err error
}

// Error implements the error interface.
func (e DryRunValidationError) Error() string {
	return e.err.Error()
}

// Unwrap implements the Unwrap interface for errors.As and errors.Is.
func (e DryRunValidationError) Unwrap() error {
	return e.err
}

func validateDryRun(
	ctx context.Context,
	w client.Writer,
	obj client.Object,
) error {
	objectPatch, mErr := json.Marshal(obj)
	if mErr != nil {
		return mErr
	}

	patch := client.RawPatch(types.ApplyPatchType, objectPatch)
	dst := obj.DeepCopyObject().(client.Object)
	err := w.Patch(ctx, dst, patch, client.FieldOwner("dummy"), client.ForceOwnership, client.DryRunAll)

	if apimachineryerrors.IsNotFound(err) {
		err = w.Create(ctx, obj.DeepCopyObject().(client.Object), client.DryRunAll)
	}

	var apiErr *apimachineryerrors.StatusError

	switch {
	case err == nil:
		return nil

	case errors.As(err, &apiErr):
		switch apiErr.Status().Reason {
		case metav1.StatusReasonUnauthorized,
			metav1.StatusReasonForbidden,
			metav1.StatusReasonAlreadyExists,
			metav1.StatusReasonConflict,
			metav1.StatusReasonInvalid,
			metav1.StatusReasonBadRequest,
			metav1.StatusReasonMethodNotAllowed,
			metav1.StatusReasonRequestEntityTooLarge,
			metav1.StatusReasonUnsupportedMediaType,
			metav1.StatusReasonNotAcceptable,
			metav1.StatusReasonNotFound:
			return DryRunValidationError{err: apiErr}
		case "":
			logr.FromContextOrDiscard(ctx).Info("API status error with empty reason string", "err", apiErr.Status())

			if strings.Contains(
				apiErr.Status().Message,
				"failed to create typed patch object",
			) {
				return DryRunValidationError{err: err}
			}
		}
	}

	return err
}
