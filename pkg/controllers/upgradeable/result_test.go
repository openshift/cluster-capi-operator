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
	"strings"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Condition message truncation", func() {
	It("should truncate at a rune boundary", func() {
		truncated := truncateConditionMessage(strings.Repeat("é", maxConditionMessageLength))

		Expect(utf8.ValidString(truncated)).To(BeTrue())
		Expect(len(truncated)).To(BeNumerically("<=", maxConditionMessageLength))
		Expect(truncated).To(HaveSuffix("..."))
		// "é" is two bytes, so a whole number of them fits within the bound.
		Expect(truncated).To(Equal(strings.Repeat("é", (maxConditionMessageLength-3)/2) + "..."))
	})

	It("should leave a message within the bound unchanged", func() {
		message := strings.Repeat("é", maxConditionMessageLength/2)

		Expect(truncateConditionMessage(message)).To(Equal(message))
	})
})
