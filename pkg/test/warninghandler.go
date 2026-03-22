/*
Copyright 2026 Red Hat, Inc.

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
package test

import (
	"bytes"
	"context"
	"strings"

	"k8s.io/client-go/rest"
)

// WarningHandler is a test implementation of the rest.WarningHandler interface.
type WarningHandler struct {
	inner rest.WarningHandler

	buf *bytes.Buffer
}

// NewTestWarningHandler creates a new WarningHandler.
func NewTestWarningHandler() *WarningHandler {
	var buf bytes.Buffer

	return &WarningHandler{
		inner: rest.NewWarningWriter(&buf, rest.WarningWriterOptions{}),
		buf:   &buf,
	}
}

// HandleWarningHeader is a test implementation of the rest.WarningHandler.HandleWarningHeader method.
func (w *WarningHandler) HandleWarningHeader(code int, agent string, text string) {
	w.inner.HandleWarningHeader(code, agent, text)
}

// HandleWarningHeaderWithContext is a test implementation of the rest.WarningHandler.HandleWarningHeaderWithContext method.
func (w *WarningHandler) HandleWarningHeaderWithContext(_ context.Context, code int, agent string, text string) {
	w.inner.HandleWarningHeader(code, agent, text)
}

// Messages returns the messages from the warning handler.
func (w *WarningHandler) Messages() []string {
	if w.buf.Len() == 0 {
		return nil
	}

	return strings.Split(strings.TrimSuffix(w.buf.String(), "\n"), "\n")
}
