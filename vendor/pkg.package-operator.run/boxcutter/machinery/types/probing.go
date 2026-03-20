package types

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ProgressProbeType is a well-known probe type used to guard phase progression.
const ProgressProbeType = "Progress"

// Prober needs to be implemented by any probing implementation.
type Prober interface {
	Probe(obj client.Object) ProbeResult
}

// ProbeStatus may be "True", "False" or "Unknown".
type ProbeStatus string

const (
	// ProbeStatusTrue means the probe has succeeded.
	ProbeStatusTrue ProbeStatus = "True"
	// ProbeStatusFalse means the probe has failed.
	ProbeStatusFalse ProbeStatus = "False"
	// ProbeStatusUnknown means the probe was unable to determine the state.
	ProbeStatusUnknown ProbeStatus = "Unknown"
)

// ProbeResult combines a ProbeState with human readable messages describing how the state happened.
type ProbeResult struct {
	// Status of the probe result, one of True, False, Unknown.
	Status ProbeStatus
	// Messages are human readable status descriptions containing details about the reported state.
	Messages []string
}

// ProbeResultContainer holds results from multiple probes.
type ProbeResultContainer map[string]ProbeResult

// Type returns the probe result for the given probe type.
func (c ProbeResultContainer) Type(t string) ProbeResult {
	if r, ok := c[t]; ok {
		return r
	}

	return ProbeResult{
		Status: ProbeStatusUnknown,
		Messages: []string{
			fmt.Sprintf("no such probe %q", t),
		},
	}
}

// ProbeFunc wraps the given function to work with the Prober interface.
func ProbeFunc(fn func(obj client.Object) ProbeResult) Prober {
	return &probeFn{Fn: fn}
}

type probeFn struct {
	Fn func(obj client.Object) ProbeResult
}

func (p *probeFn) Probe(obj client.Object) ProbeResult {
	return p.Fn(obj)
}
