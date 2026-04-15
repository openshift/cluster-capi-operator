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

package commoncmdoptions_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Set the absolute path of the test binary (os.Args[0]). This is needed when
// the working directory might differ between parent and child processes.
func init() {
	if filepath.IsAbs(os.Args[0]) {
		return
	}

	abs, err := filepath.Abs(os.Args[0])
	if err == nil {
		os.Args[0] = abs
	}
}

func writeKubeconfig(kubeconfigBytes []byte) (string, error) {
	tmpFile, err := os.CreateTemp("", "commoncmdoptions-test-kubeconfig-*.yaml")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}

	if _, err := tmpFile.Write(kubeconfigBytes); err != nil {
		err = errors.Join(err,
			tmpFile.Close(),
			os.Remove(tmpFile.Name()),
		)

		return "", fmt.Errorf("writing kubeconfig: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		err = errors.Join(err,
			os.Remove(tmpFile.Name()),
		)

		return "", fmt.Errorf("closing kubeconfig: %w", err)
	}

	return tmpFile.Name(), nil
}

// filterEnv returns a subset of env containing only the named variables.
func filterEnv(env []string, keep ...string) []string {
	keepSet := make(map[string]bool, len(keep))
	for _, k := range keep {
		keepSet[k] = true
	}

	var filtered []string

	for _, e := range env {
		for k := range keepSet {
			if len(e) > len(k) && e[:len(k)+1] == k+"=" {
				filtered = append(filtered, e)
				break
			}
		}
	}

	return filtered
}

func assertEqual[T comparable](t *testing.T, field string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s: got %v, want %v", field, got, want)
	}
}
