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
package providerimages

import (
	"bufio"
	"errors"
	"io"
	"iter"
	"os"
	"strings"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

// Manifests returns an iterator over individual YAML documents in the
// manifests file. Empty documents produced by leading or trailing "---"
// separators are skipped. The file is read lazily; it is opened when
// iteration begins and closed when iteration ends.
func (p *ProviderImageManifests) Manifests() iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		f, err := os.Open(p.ManifestsPath)
		if err != nil {
			yield("", err)
			return
		}
		defer f.Close() //nolint:errcheck

		reader := utilyaml.NewYAMLReader(bufio.NewReader(f))

		for {
			docBytes, err := reader.Read()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}

				yield("", err)

				return
			}

			// YAMLReader includes a leading "---\n" in the first
			// document when the file starts with a document separator.
			// Strip it so callers get only the document content.
			doc := strings.TrimPrefix(string(docBytes), "---\n")
			if !yield(doc, nil) {
				return
			}
		}
	}
}
