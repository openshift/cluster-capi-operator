package util

import (
	"errors"

	"github.com/go-logr/logr"
)

type NoRequeueErrorWrapper struct {
	error
	Reason string
}

var _ error = &NoRequeueErrorWrapper{}

func (e *NoRequeueErrorWrapper) Unwrap() error {
	return e.error
}

// NoRequeueError is a wrapper for an error that should not cause the
// reconciliation to be requeued. This is for situations when we know the error
// will occur again on the next reconciliation, for example due to a
// misconfiguration.

// An error wrapped with NoRequeueError will be logged, but not returned to
// controller-runtime.
func NoRequeueError(err error, reason string) error {
	return &NoRequeueErrorWrapper{err, reason}
}

// LogNoRequeueError filters out NoRequeueError errors from the error chain.
func LogNoRequeueError(err error, log logr.Logger) error {
	noRequeue := &NoRequeueErrorWrapper{}
	if errors.As(err, &noRequeue) {
		// Note that we log the original error not the wrapped error in case it
		// is itself wrapped or joined.
		log.Error(err, "Not requeuing after error", "reason", noRequeue.Reason)
		return nil
	}
	return err
}

func AsNoRequeueError(err error) *NoRequeueErrorWrapper {
	noRequeue := &NoRequeueErrorWrapper{}
	if errors.As(err, &noRequeue) {
		return noRequeue
	}
	return nil
}
