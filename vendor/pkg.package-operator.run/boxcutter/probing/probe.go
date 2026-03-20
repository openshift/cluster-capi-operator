package probing

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"pkg.package-operator.run/boxcutter/machinery/types"
)

// Prober needs to be implemented by any probing implementation.
type Prober = types.Prober

// Status may be "True", "False" or "Unknown".
type Status = types.ProbeStatus

const (
	// StatusTrue means the probe has succeeded.
	StatusTrue Status = types.ProbeStatusTrue
	// StatusFalse means the probe has failed.
	StatusFalse Status = types.ProbeStatusFalse
	// StatusUnknown means the probe was unable to determine the state.
	StatusUnknown Status = types.ProbeStatusUnknown
)

// Result combines a probe status with human readable messages describing how the state happened.
type Result = types.ProbeResult

// And combines multiple Prober.
// The returned status is:
// - True if all ProbeResults are True
// - False if at least one ProbeResult is False and none are Unknown
// - Unknown if at least one ProbeResult is Unknown
// Messages of the same Status will be combined.
type And []Prober

var _ Prober = (And)(nil)

// Probe runs probes against the given object and returns the result.
func (p And) Probe(obj client.Object) Result {
	var unknownMsgs, trueMsgs, falseMsgs []string

	var statusUnknown, statusFalse bool

	for _, probe := range p {
		r := probe.Probe(obj)
		switch r.Status {
		case StatusTrue:
			trueMsgs = append(trueMsgs, r.Messages...)
		case StatusFalse:
			statusFalse = true

			falseMsgs = append(falseMsgs, r.Messages...)
		case StatusUnknown:
			statusUnknown = true

			unknownMsgs = append(unknownMsgs, r.Messages...)
		}
	}

	if statusUnknown {
		return Result{
			Status:   StatusUnknown,
			Messages: unknownMsgs,
		}
	}

	if statusFalse {
		return Result{
			Status:   StatusFalse,
			Messages: falseMsgs,
		}
	}

	return Result{
		Status:   StatusTrue,
		Messages: trueMsgs,
	}
}

// TrueResult is a helper returning a True ProbeResult with the given messages.
func TrueResult(msgs ...string) Result {
	return Result{Status: StatusTrue, Messages: msgs}
}

// FalseResult is a helper returning a False ProbeResult with the given messages.
func FalseResult(msgs ...string) Result {
	return Result{Status: StatusFalse, Messages: msgs}
}

// UnknownResult is a helper returning a Unknown ProbeResult with the given messages.
func UnknownResult(msgs ...string) Result {
	return Result{Status: StatusUnknown, Messages: msgs}
}

func toUnstructured(obj client.Object) *unstructured.Unstructured {
	unstr, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		panic(fmt.Sprintf("can't convert to unstructured: %v", err))
	}

	return &unstructured.Unstructured{Object: unstr}
}

func probeUnstructuredSingleMsg(
	obj client.Object,
	probe func(obj *unstructured.Unstructured) Result,
) Result {
	unst := toUnstructured(obj)

	return probe(unst)
}
