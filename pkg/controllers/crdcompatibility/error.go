package crdcompatibility

import (
	"errors"

	"github.com/go-logr/logr"
)

type noRequeueErrorWrapper struct {
	error
	reason string
}

var _ error = &noRequeueErrorWrapper{}

func (e *noRequeueErrorWrapper) Unwrap() error {
	return e.error
}

// noRequeueError is a wrapper for an error that should not cause the
// reconciliation to be requeued. This is for situations when we know the error
// will occur again on the next reconciliation, for example due to a
// misconfiguration.

// An error wrapped with noRequeueError will be logged, but not returned to controller-runtime.
func noRequeueError(err error, reason string) error {
	return &noRequeueErrorWrapper{err, reason}
}

// logNoRequeueError filters out noRequeueError errors from the error chain.
func logNoRequeueError(err error, log logr.Logger) error {
	noRequeue := &noRequeueErrorWrapper{}
	if errors.As(err, noRequeue) {
		log.Error(err, "Not requeuing after error", "reason", noRequeue.reason)
		return nil
	}
	return err
}
