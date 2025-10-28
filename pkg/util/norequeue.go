/*
Copyright 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package util

import (
	"errors"

	"github.com/go-logr/logr"
)

type noRequeueError struct {
	error
	Reason string
}

var _ error = &noRequeueError{}

func (e *noRequeueError) Unwrap() error {
	return e.error
}

// NoRequeueError returns an error that will not be returned to
// controller-runtime to trigger a requeue.
func NoRequeueError(err error, reason string) error {
	return &noRequeueError{err, reason}
}

// LogNoRequeueError ensures that its error argument is logged but not returned if it is a NoRequeueError.
func LogNoRequeueError(err error, log logr.Logger) error {
	noRequeue := &noRequeueError{}
	if errors.As(err, &noRequeue) {
		// Note that we log the original error not the wrapped error in case it
		// is itself wrapped or joined.
		log.Error(err, "Not requeuing after error", "reason", noRequeue.Reason)
		return nil
	}

	return err
}

// AsNoRequeueError returns its argument as a NoRequeueError if it is one, otherwise nil.
func AsNoRequeueError(err error) *noRequeueError {
	noRequeue := &noRequeueError{}
	if errors.As(err, &noRequeue) {
		return noRequeue
	}

	return nil
}
