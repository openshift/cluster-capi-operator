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
package capi2mapi

import "k8s.io/utils/ptr"

// setFailureReason sets *target to a pointer to value. The CAPI error type is
// inferred from the target field, so tests do not import sigs.k8s.io/cluster-api/errors,
// which moved to sigs.k8s.io/cluster-api/api/deprecated/errors in CAPI v1.14.
func setFailureReason[T ~string](target **T, value string) {
	*target = ptr.To(T(value))
}
