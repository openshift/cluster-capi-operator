package types

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RevisionReconcileOptions holds configuration options changing revision reconciliation.
type RevisionReconcileOptions struct {
	// DefaultObjectOptions applying to all phases in the revision.
	DefaultPhaseOptions []PhaseReconcileOption
	// PhaseOptions maps PhaseOptions for specific phases.
	PhaseOptions map[string][]PhaseReconcileOption
}

// ForPhase returns the options for a given phase.
func (rropts RevisionReconcileOptions) ForPhase(phaseName string) []PhaseReconcileOption {
	opts := make([]PhaseReconcileOption, 0, len(rropts.DefaultPhaseOptions)+len(rropts.PhaseOptions[phaseName]))
	opts = append(opts, rropts.DefaultPhaseOptions...)
	opts = append(opts, rropts.PhaseOptions[phaseName]...)

	return opts
}

func (rropts RevisionReconcileOptions) GetOwner() client.Object {
	var phaseOptions PhaseReconcileOptions
	for _, opt := range rropts.DefaultPhaseOptions {
		opt.ApplyToPhaseReconcileOptions(&phaseOptions)
	}

	var objectOptions ObjectReconcileOptions
	for _, opt := range phaseOptions.DefaultObjectOptions {
		opt.ApplyToObjectReconcileOptions(&objectOptions)
	}

	return objectOptions.Owner
}

// RevisionReconcileOption is the common interface for revision reconciliation options.
type RevisionReconcileOption interface {
	ApplyToRevisionReconcileOptions(opts *RevisionReconcileOptions)
}

// RevisionTeardownOptions holds configuration options changing revision teardown.
type RevisionTeardownOptions struct {
	// DefaultObjectOptions applying to all phases in the revision.
	DefaultPhaseOptions []PhaseTeardownOption
	// PhaseOptions maps PhaseOptions for specific phases.
	PhaseOptions map[string][]PhaseTeardownOption
}

// ForPhase returns the options for a given phase.
func (rtopts RevisionTeardownOptions) ForPhase(phaseName string) []PhaseTeardownOption {
	opts := make([]PhaseTeardownOption, 0, len(rtopts.DefaultPhaseOptions)+len(rtopts.PhaseOptions[phaseName]))
	opts = append(opts, rtopts.DefaultPhaseOptions...)
	opts = append(opts, rtopts.PhaseOptions[phaseName]...)

	return opts
}

// RevisionTeardownOption is the common interface for revision teardown options.
type RevisionTeardownOption interface {
	ApplyToRevisionTeardownOptions(opts *RevisionTeardownOptions)
}

// PhaseReconcileOptions holds configuration options changing phase reconciliation.
type PhaseReconcileOptions struct {
	// DefaultObjectOptions applying to all objects in the phase.
	DefaultObjectOptions []ObjectReconcileOption
	// ObjectOptions maps ObjectOptions for specific objects.
	ObjectOptions map[ObjectRef][]ObjectReconcileOption
	// AggregateErrors aggregates all object errors from the phase and returns them as a single error.
	AggregateErrors bool
}

// ForObject returns the options for the given object.
func (pro PhaseReconcileOptions) ForObject(obj client.Object) []ObjectReconcileOption {
	objRef := ToObjectRef(obj)

	opts := make([]ObjectReconcileOption, 0, len(pro.DefaultObjectOptions)+len(pro.ObjectOptions[objRef]))
	opts = append(opts, pro.DefaultObjectOptions...)
	opts = append(opts, pro.ObjectOptions[objRef]...)

	return opts
}

// PhaseReconcileOption is the common interface for phase reconciliation options.
type PhaseReconcileOption interface {
	ApplyToPhaseReconcileOptions(opts *PhaseReconcileOptions)
	RevisionReconcileOption
}

// PhaseTeardownOptions holds configuration options changing phase teardown.
type PhaseTeardownOptions struct {
	// DefaultObjectOptions applying to all objects in the phase.
	DefaultObjectOptions []ObjectTeardownOption
	// ObjectOptions maps ObjectOptions for specific objects.
	ObjectOptions map[ObjectRef][]ObjectTeardownOption
	// AggregateErrors aggregates all object errors from the phase and returns them as a single error.
	AggregateErrors bool
}

// ForObject returns the options for the given object.
func (pto PhaseTeardownOptions) ForObject(obj client.Object) []ObjectTeardownOption {
	objRef := ToObjectRef(obj)

	opts := make([]ObjectTeardownOption, 0, len(pto.DefaultObjectOptions)+len(pto.ObjectOptions[objRef]))
	opts = append(opts, pto.DefaultObjectOptions...)
	opts = append(opts, pto.ObjectOptions[objRef]...)

	return opts
}

// PhaseTeardownOption is the common interface for phase teardown options.
type PhaseTeardownOption interface {
	ApplyToPhaseTeardownOptions(opts *PhaseTeardownOptions)
	RevisionTeardownOption
}

// ObjectReconcileOptions holds configuration options changing object reconciliation.
type ObjectReconcileOptions struct {
	CollisionProtection CollisionProtection
	PreviousOwners      []client.Object
	Owner               client.Object
	OwnerStrategy       OwnerStrategy
	Paused              bool
	Probes              map[string]Prober
}

// Default sets empty Option fields to their default value.
func (opts *ObjectReconcileOptions) Default() {
	if opts.Owner != nil && opts.OwnerStrategy == nil {
		panic("Owner without ownerStrategy set")
	}

	if len(opts.CollisionProtection) == 0 {
		opts.CollisionProtection = CollisionProtectionPrevent
	}
}

// ObjectReconcileOption is the common interface for object reconciliation options.
type ObjectReconcileOption interface {
	ApplyToObjectReconcileOptions(opts *ObjectReconcileOptions)
	PhaseReconcileOption
}

var (
	_ ObjectReconcileOption = (WithCollisionProtection)("")
	_ ObjectReconcileOption = (WithPaused{})
	_ ObjectReconcileOption = (WithPreviousOwners{})
	_ ObjectReconcileOption = (WithProbe("", nil))
	_ ObjectTeardownOption  = (WithTeardownWriter(nil))
)

// ObjectTeardownOptions holds configuration options changing object teardown.
type ObjectTeardownOptions struct {
	Orphan         bool
	TeardownWriter client.Writer
	Owner          client.Object
	OwnerStrategy  OwnerStrategy
}

// Default sets empty Option fields to their default value.
func (opts *ObjectTeardownOptions) Default() {
	if opts.Owner != nil && opts.OwnerStrategy == nil {
		panic("Owner without ownerStrategy set")
	}
}

// ObjectTeardownOption is the common interface for object teardown options.
type ObjectTeardownOption interface {
	ApplyToObjectTeardownOptions(opts *ObjectTeardownOptions)
	PhaseTeardownOption
}

// CollisionProtection specifies how collision with existing objects and
// other controllers should be handled.
type CollisionProtection string

const (
	// CollisionProtectionPrevent prevents owner collisions entirely
	// by not allowing to work with objects already present on the cluster.
	CollisionProtectionPrevent CollisionProtection = "Prevent"
	// CollisionProtectionIfNoController allows to patch and override
	// objects already present if they are not owned by another controller.
	CollisionProtectionIfNoController CollisionProtection = "IfNoController"
	// CollisionProtectionNone allows to patch and override objects already
	// present and owned by other controllers.
	//
	// Be careful!
	// This setting may cause multiple controllers to fight over a resource,
	// causing load on the Kubernetes API server and etcd.
	CollisionProtectionNone CollisionProtection = "None"
)

// WithCollisionProtection instructs the given CollisionProtection setting to be used.
type WithCollisionProtection CollisionProtection

// ApplyToObjectReconcileOptions implements ObjectReconcileOption.
func (p WithCollisionProtection) ApplyToObjectReconcileOptions(opts *ObjectReconcileOptions) {
	opts.CollisionProtection = CollisionProtection(p)
}

// ApplyToPhaseReconcileOptions implements PhaseOption.
func (p WithCollisionProtection) ApplyToPhaseReconcileOptions(opts *PhaseReconcileOptions) {
	opts.DefaultObjectOptions = append(opts.DefaultObjectOptions, p)
}

// ApplyToRevisionReconcileOptions implements RevisionReconcileOptions.
func (p WithCollisionProtection) ApplyToRevisionReconcileOptions(opts *RevisionReconcileOptions) {
	opts.DefaultPhaseOptions = append(opts.DefaultPhaseOptions, p)
}

// WithPreviousOwners is a list of known objects allowed to take ownership from.
// Objects from this list will not trigger collision detection and prevention.
type WithPreviousOwners []client.Object

// ApplyToObjectReconcileOptions implements ObjectReconcileOption.
func (p WithPreviousOwners) ApplyToObjectReconcileOptions(opts *ObjectReconcileOptions) {
	opts.PreviousOwners = p
}

// ApplyToPhaseReconcileOptions implements PhaseOption.
func (p WithPreviousOwners) ApplyToPhaseReconcileOptions(opts *PhaseReconcileOptions) {
	opts.DefaultObjectOptions = append(opts.DefaultObjectOptions, p)
}

// ApplyToRevisionReconcileOptions implements RevisionReconcileOptions.
func (p WithPreviousOwners) ApplyToRevisionReconcileOptions(opts *RevisionReconcileOptions) {
	opts.DefaultPhaseOptions = append(opts.DefaultPhaseOptions, p)
}

// WithPaused skips reconciliation and just reports status information.
// Can also be described as dry-run, as no modification will occur.
type WithPaused struct{}

// ApplyToObjectReconcileOptions implements ObjectReconcileOption.
func (p WithPaused) ApplyToObjectReconcileOptions(opts *ObjectReconcileOptions) {
	opts.Paused = true
}

// ApplyToPhaseReconcileOptions implements PhaseOption.
func (p WithPaused) ApplyToPhaseReconcileOptions(opts *PhaseReconcileOptions) {
	opts.DefaultObjectOptions = append(opts.DefaultObjectOptions, p)
}

// ApplyToRevisionReconcileOptions implements RevisionReconcileOptions.
func (p WithPaused) ApplyToRevisionReconcileOptions(opts *RevisionReconcileOptions) {
	opts.DefaultPhaseOptions = append(opts.DefaultPhaseOptions, p)
}

// WithProbe registers the given probe to evaluate state of objects.
func WithProbe(t string, probe Prober) ObjectReconcileOption {
	return &optionFn{
		fn: func(opts *ObjectReconcileOptions) {
			if opts.Probes == nil {
				opts.Probes = map[string]Prober{}
			}

			opts.Probes[t] = probe
		},
	}
}

// WithOrphan exclude objects from Teardown.
// use it as WithObjectTeardownOptions(obj, WithOrphan()) to exclude individual objects or
// use it as WithPhaseTeardownOptions("my-phase", WithOrphan()) to exclude a whole phase.
func WithOrphan() ObjectTeardownOption {
	return &teardownOptionFn{
		fn: func(opts *ObjectTeardownOptions) {
			opts.Orphan = true
		},
	}
}

// WithAggregatePhaseReconcileErrors causes phase reconciliation to aggregate all object
// errors as a single error instead of returning on the first error.
func WithAggregatePhaseReconcileErrors() PhaseReconcileOption {
	return phaseReconcileOptionFn(func(opts *PhaseReconcileOptions) {
		opts.AggregateErrors = true
	})
}

// WithAggregatePhaseTeardownErrors causes phase teardown to aggregate all object
// errors as a single error instead of returning on the first error.
func WithAggregatePhaseTeardownErrors() PhaseTeardownOption {
	return phaseTeardownOptionFn(func(opts *PhaseTeardownOptions) {
		opts.AggregateErrors = true
	})
}

// WithTeardownWriter tears down the revision with the given writer.
func WithTeardownWriter(writer client.Writer) ObjectTeardownOption {
	return &teardownOptionFn{
		fn: func(opts *ObjectTeardownOptions) {
			opts.TeardownWriter = writer
		},
	}
}

type withObjectReconcileOptions struct {
	obj  ObjectRef
	opts []ObjectReconcileOption
}

// WithObjectReconcileOptions applies the given options only to the given object.
func WithObjectReconcileOptions(obj client.Object, opts ...ObjectReconcileOption) PhaseReconcileOption {
	return &withObjectReconcileOptions{
		obj:  ToObjectRef(obj),
		opts: opts,
	}
}

// ApplyToPhaseReconcileOptions implements PhaseOption.
func (p *withObjectReconcileOptions) ApplyToPhaseReconcileOptions(opts *PhaseReconcileOptions) {
	if opts.ObjectOptions == nil {
		opts.ObjectOptions = map[ObjectRef][]ObjectReconcileOption{}
	}

	opts.ObjectOptions[p.obj] = p.opts
}

// ApplyToRevisionReconcileOptions implements RevisionReconcileOptions.
func (p *withObjectReconcileOptions) ApplyToRevisionReconcileOptions(opts *RevisionReconcileOptions) {
	opts.DefaultPhaseOptions = append(opts.DefaultPhaseOptions, p)
}

type withObjectTeardownOptions struct {
	obj  ObjectRef
	opts []ObjectTeardownOption
}

// WithObjectTeardownOptions applies the given options only to the given object.
func WithObjectTeardownOptions(obj client.Object, opts ...ObjectTeardownOption) PhaseTeardownOption {
	return &withObjectTeardownOptions{
		obj:  ToObjectRef(obj),
		opts: opts,
	}
}

// ApplyToPhaseTeardownOptions implements PhaseOption.
func (p *withObjectTeardownOptions) ApplyToPhaseTeardownOptions(opts *PhaseTeardownOptions) {
	if opts.ObjectOptions == nil {
		opts.ObjectOptions = map[ObjectRef][]ObjectTeardownOption{}
	}

	opts.ObjectOptions[p.obj] = p.opts
}

// ApplyToRevisionTeardownOptions implements RevisionTeardownOptions.
func (p *withObjectTeardownOptions) ApplyToRevisionTeardownOptions(opts *RevisionTeardownOptions) {
	opts.DefaultPhaseOptions = append(opts.DefaultPhaseOptions, p)
}

type withPhaseReconcileOptions struct {
	phaseName string
	opts      []PhaseReconcileOption
}

// WithPhaseReconcileOptions applies the given options only to the given phase.
func WithPhaseReconcileOptions(phaseName string, opts ...PhaseReconcileOption) RevisionReconcileOption {
	return &withPhaseReconcileOptions{
		phaseName: phaseName,
		opts:      opts,
	}
}

// ApplyToRevisionReconcileOptions implements RevisionReconcileOptions.
func (p *withPhaseReconcileOptions) ApplyToRevisionReconcileOptions(opts *RevisionReconcileOptions) {
	if opts.PhaseOptions == nil {
		opts.PhaseOptions = map[string][]PhaseReconcileOption{}
	}

	opts.PhaseOptions[p.phaseName] = p.opts
}

type withPhaseTeardownOptions struct {
	phaseName string
	opts      []PhaseTeardownOption
}

// WithPhaseTeardownOptions applies the given options only to the given phase.
func WithPhaseTeardownOptions(phaseName string, opts ...PhaseTeardownOption) RevisionTeardownOption {
	return &withPhaseTeardownOptions{
		phaseName: phaseName,
		opts:      opts,
	}
}

// ApplyToRevisionTeardownOptions implements RevisionTeardownOptions.
func (p *withPhaseTeardownOptions) ApplyToRevisionTeardownOptions(opts *RevisionTeardownOptions) {
	if opts.PhaseOptions == nil {
		opts.PhaseOptions = map[string][]PhaseTeardownOption{}
	}

	opts.PhaseOptions[p.phaseName] = p.opts
}

// OwnerStrategy interface needed for RevisionEngine.
type OwnerStrategy interface {
	SetControllerReference(owner, obj metav1.Object) error
	GetController(obj metav1.Object) (metav1.OwnerReference, bool)
	IsController(owner, obj metav1.Object) bool
	CopyOwnerReferences(objA, objB metav1.Object)
	ReleaseController(obj metav1.Object)
	RemoveOwner(owner, obj metav1.Object)
}

// WithOwner sets an owning object and the strategy to use with it.
// Ensures controller-refs are set to track the owner and
// enables handover between owners.
func WithOwner(obj client.Object, start OwnerStrategy) interface {
	ComparatorOption
	ObjectReconcileOption
	ObjectTeardownOption
} {
	if len(obj.GetUID()) == 0 {
		panic("owner must be persisted to cluster, empty UID")
	}

	return &combinedOpts{
		fn: func(opts *ComparatorOptions) {
			opts.Owner = obj
			opts.OwnerStrategy = start
		},
		optionFn: optionFn{
			fn: func(opts *ObjectReconcileOptions) {
				opts.Owner = obj
				opts.OwnerStrategy = start
			},
		},
		teardownOptionFn: teardownOptionFn{
			fn: func(opts *ObjectTeardownOptions) {
				opts.Owner = obj
				opts.OwnerStrategy = start
			},
		},
	}
}

type combinedOpts struct {
	optionFn
	teardownOptionFn
	fn func(opts *ComparatorOptions)
}

func (copt *combinedOpts) ApplyToComparatorOptions(opts *ComparatorOptions) {
	copt.fn(opts)
}

type ComparatorOptions struct {
	Owner         client.Object
	OwnerStrategy OwnerStrategy
}

type ComparatorOption interface {
	ApplyToComparatorOptions(opts *ComparatorOptions)
}

type optionFn struct {
	fn func(opts *ObjectReconcileOptions)
}

// ApplyToObjectReconcileOptions implements ObjectReconcileOption.
func (p *optionFn) ApplyToObjectReconcileOptions(opts *ObjectReconcileOptions) {
	p.fn(opts)
}

// ApplyToPhaseReconcileOptions implements PhaseOption.
func (p *optionFn) ApplyToPhaseReconcileOptions(opts *PhaseReconcileOptions) {
	opts.DefaultObjectOptions = append(opts.DefaultObjectOptions, p)
}

// ApplyToRevisionReconcileOptions implements RevisionReconcileOptions.
func (p *optionFn) ApplyToRevisionReconcileOptions(opts *RevisionReconcileOptions) {
	opts.DefaultPhaseOptions = append(opts.DefaultPhaseOptions, p)
}

type phaseReconcileOptionFn func(opts *PhaseReconcileOptions)

// ApplyToPhaseReconcileOptions implements PhaseOption.
func (p phaseReconcileOptionFn) ApplyToPhaseReconcileOptions(opts *PhaseReconcileOptions) {
	p(opts)
}

// ApplyToRevisionReconcileOptions implements RevisionReconcileOptions.
func (p phaseReconcileOptionFn) ApplyToRevisionReconcileOptions(opts *RevisionReconcileOptions) {
	opts.DefaultPhaseOptions = append(opts.DefaultPhaseOptions, p)
}

type phaseTeardownOptionFn func(opts *PhaseTeardownOptions)

// ApplyToPhaseTeardownOptions implements PhaseOption.
func (p phaseTeardownOptionFn) ApplyToPhaseTeardownOptions(opts *PhaseTeardownOptions) {
	p(opts)
}

// ApplyToRevisionTeardownOptions implements RevisionTeardownOptions.
func (p phaseTeardownOptionFn) ApplyToRevisionTeardownOptions(opts *RevisionTeardownOptions) {
	opts.DefaultPhaseOptions = append(opts.DefaultPhaseOptions, p)
}

type teardownOptionFn struct {
	fn func(opts *ObjectTeardownOptions)
}

// ApplyToObjectTeardownOptions implements ObjectTeardownOption.
func (p *teardownOptionFn) ApplyToObjectTeardownOptions(opts *ObjectTeardownOptions) {
	p.fn(opts)
}

// ApplyToPhaseTeardownOptions implements PhaseOption.
func (p *teardownOptionFn) ApplyToPhaseTeardownOptions(opts *PhaseTeardownOptions) {
	opts.DefaultObjectOptions = append(opts.DefaultObjectOptions, p)
}

// ApplyToRevisionTeardownOptions implements RevisionTeardownOptions.
func (p *teardownOptionFn) ApplyToRevisionTeardownOptions(opts *RevisionTeardownOptions) {
	opts.DefaultPhaseOptions = append(opts.DefaultPhaseOptions, p)
}
