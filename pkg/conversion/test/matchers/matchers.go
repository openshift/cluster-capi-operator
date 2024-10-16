/*
Copyright 2024 Red Hat, Inc.

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
package matchers

import (
	. "github.com/onsi/gomega"

	"github.com/onsi/gomega/types"
)

// ConsistOfSubstrings takes a slice of substrings as input and returns a GomegaMatcher
// that matches the slice of substrings against a slice of strings, checking they all have a match.
func ConsistOfSubstrings(ss []string) types.GomegaMatcher {
	if len(ss) == 0 {
		return BeEmpty()
	}

	interfaceMatchers := make([]interface{}, len(ss))
	for i, m := range ss {
		interfaceMatchers[i] = ContainSubstring(m)
	}

	return ConsistOf(interfaceMatchers...)
}

// ConsistOfMatchErrorSubstrings takes a slice of substrings as input and returns a GomegaMatcher
// that matches the slice of substrings against a slice of errors (with strings error messages),
// checking that all the substrings have a matching error string.
func ConsistOfMatchErrorSubstrings(ss []string) types.GomegaMatcher {
	if len(ss) == 0 {
		return BeNil()
	}

	interfaceMatchers := make([]interface{}, len(ss))
	for i, m := range ss {
		interfaceMatchers[i] = MatchError(ContainSubstring(m))
	}

	return ConsistOf(interfaceMatchers...)
}
