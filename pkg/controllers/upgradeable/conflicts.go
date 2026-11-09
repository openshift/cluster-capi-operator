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
package upgradeable

import (
	"fmt"
	"strings"
)

type crdConflictReason string

const (
	foreignCRDReason               crdConflictReason = "ForeignCRD"
	unmanagedCRDNotFoundReason     crdConflictReason = "UnmanagedCRDNotFound"
	unmanagedCRDIncompatibleReason crdConflictReason = "UnmanagedCRDIncompatible"
)

type crdConflictError struct {
	CRDName              string
	Reason               crdConflictReason
	Message              string
	CompatibilityReason  string
	CompatibilityMessage string
	Remediation          string
}

func (e *crdConflictError) Error() string {
	return fmt.Sprintf("%s [%s]: %s\n  CompatibilityRequirement: %s: %s\n  Remediation: %s",
		e.CRDName, e.Reason, e.Message, e.CompatibilityReason,
		strings.ReplaceAll(e.CompatibilityMessage, "\n", "\n  "), e.Remediation)
}
