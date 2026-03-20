// Package types contains common type definitions for boxcutter machinery.
package types

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ObjectRef holds information to identify an object.
type ObjectRef struct {
	schema.GroupVersionKind
	client.ObjectKey
}

// ToObjectRef returns an ObjectRef object from unstructured.
func ToObjectRef(obj client.Object) ObjectRef {
	if obj == nil {
		return ObjectRef{}
	}

	return ObjectRef{
		GroupVersionKind: obj.GetObjectKind().GroupVersionKind(),
		ObjectKey:        client.ObjectKeyFromObject(obj),
	}
}

// String returns a string representation.
func (oid ObjectRef) String() string {
	return fmt.Sprintf("%s %s", oid.GroupVersionKind, oid.ObjectKey)
}

// Phase represents a named collection of objects.
type Phase interface {
	// GetName returns the name of the phase.
	GetName() string
	// GetObjects returns the objects contained in the phase.
	GetObjects() []client.Object
	// GetReconcileOptions returns options for reconciling this phase.
	GetReconcileOptions() []PhaseReconcileOption
	// GetTeardownOptions returns options for tearing down this phase.
	GetTeardownOptions() []PhaseTeardownOption
}

// PhaseBuilder is a Phase with methods to attach options.
type PhaseBuilder interface {
	Phase
	// WithReconcileOptions sets PhaseReconcileOptions on this phase.
	WithReconcileOptions(opts ...PhaseReconcileOption) PhaseBuilder
	// WithTeardownOptions sets PhaseTeardownOption on this phase.
	WithTeardownOptions(opts ...PhaseTeardownOption) PhaseBuilder
}

// NewPhase creates a new PhaseBuilder with the given name and objects.
func NewPhase(name string, objects []client.Object) PhaseBuilder {
	return &phase{
		Name:    name,
		Objects: objects,
	}
}

// NewPhaseWithOwner creates a new PhaseBuilder with the given name, objects and owner.
func NewPhaseWithOwner(
	name string, objects []client.Object,
	owner client.Object, ownerStrat OwnerStrategy,
) PhaseBuilder {
	oo := WithOwner(owner, ownerStrat)
	p := &phase{
		Name:    name,
		Objects: objects,
	}

	return p.WithReconcileOptions(oo).WithTeardownOptions(oo)
}

// phase represents a named collection of objects.
type phase struct {
	// Name of the Phase.
	Name string
	// Objects contained in the phase.
	Objects []client.Object

	ReconcileOptions []PhaseReconcileOption
	TeardownOptions  []PhaseTeardownOption
}

// GetName returns the name of the phase.
func (p *phase) GetName() string {
	return p.Name
}

// GetObjects returns the objects contained in the phase.
func (p *phase) GetObjects() []client.Object {
	return p.Objects
}

// GetReconcileOptions returns options for reconciling this phase.
func (p *phase) GetReconcileOptions() []PhaseReconcileOption {
	return p.ReconcileOptions
}

// GetTeardownOptions returns options for tearing down this phase.
func (p *phase) GetTeardownOptions() []PhaseTeardownOption {
	return p.TeardownOptions
}

// WithReconcileOptions sets PhaseReconcileOptions on this phase.
func (p *phase) WithReconcileOptions(opts ...PhaseReconcileOption) PhaseBuilder {
	p.ReconcileOptions = append(p.ReconcileOptions, opts...)

	return p
}

// WithTeardownOptions sets PhaseTeardownOption on this phase.
func (p *phase) WithTeardownOptions(opts ...PhaseTeardownOption) PhaseBuilder {
	p.TeardownOptions = append(p.TeardownOptions, opts...)

	return p
}

// Revision represents the version of a content collection consisting of phases.
type Revision interface {
	// GetName returns the name of the revision.
	GetName() string
	// GetRevisionNumber returns the current revision number.
	GetRevisionNumber() int64
	// GetPhases returns the phases a revision is made up of.
	GetPhases() []Phase
	// GetReconcileOptions returns options for reconciling this revision.
	GetReconcileOptions() []RevisionReconcileOption
	// GetTeardownOptions returns options for tearing down this revision.
	GetTeardownOptions() []RevisionTeardownOption
}

// RevisionBuilder is a Revision with methods to attach options.
type RevisionBuilder interface {
	Revision
	// WithReconcileOptions sets RevisionReconcileOptions on this revision.
	WithReconcileOptions(opts ...RevisionReconcileOption) RevisionBuilder
	// WithTeardownOptions sets RevisionTeardownOption on this revision.
	WithTeardownOptions(opts ...RevisionTeardownOption) RevisionBuilder
}

// NewRevision creates a new RevisionBuilder with
// the given name, rev and phases.
func NewRevision(
	name string,
	revNumber int64,
	phases []Phase,
) RevisionBuilder {
	return &revision{
		Name:     name,
		Revision: revNumber,
		Phases:   phases,
	}
}

// NewRevisionWithOwner creates a new RevisionBuilder
// with the given name, rev, phases and owner.
func NewRevisionWithOwner(
	name string,
	revNumber int64,
	phases []Phase,
	owner client.Object, ownerStrat OwnerStrategy,
) RevisionBuilder {
	oo := WithOwner(owner, ownerStrat)
	r := &revision{
		Name:     name,
		Revision: revNumber,
		Phases:   phases,
	}

	return r.WithReconcileOptions(oo).WithTeardownOptions(oo)
}

// revision represents the version of a content collection consisting of phases.
type revision struct {
	// Name of the Revision.
	Name string
	// Revision number.
	Revision int64
	// Ordered list of phases.
	Phases []Phase

	ReconcileOptions []RevisionReconcileOption
	TeardownOptions  []RevisionTeardownOption
}

// GetName returns the name of the revision.
func (r *revision) GetName() string {
	return r.Name
}

// GetRevisionNumber returns the current revision number.
func (r *revision) GetRevisionNumber() int64 {
	return r.Revision
}

// GetPhases returns the phases a revision is made up of.
func (r *revision) GetPhases() []Phase {
	return r.Phases
}

// GetReconcileOptions returns options for reconciling this revision.
func (r *revision) GetReconcileOptions() []RevisionReconcileOption {
	return r.ReconcileOptions
}

// GetTeardownOptions returns options for tearing down this revision.
func (r *revision) GetTeardownOptions() []RevisionTeardownOption {
	return r.TeardownOptions
}

// WithReconcileOptions sets RevisionReconcileOptions on this revision.
func (r *revision) WithReconcileOptions(opts ...RevisionReconcileOption) RevisionBuilder {
	r.ReconcileOptions = opts

	return r
}

// WithTeardownOptions sets RevisionTeardownOption on this revision.
func (r *revision) WithTeardownOptions(opts ...RevisionTeardownOption) RevisionBuilder {
	r.TeardownOptions = opts

	return r
}
