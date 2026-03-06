package e2e

import (
	"path/filepath"
	"runtime"
)

// FixturePath returns the absolute path to a fixture file.
// It uses runtime.Caller to determine the location of the test/e2e directory.
func FixturePath(elem ...string) string {
	_, filename, _, _ := runtime.Caller(0)
	testE2EDir := filepath.Dir(filename)

	// Build path: test/e2e/fixtures/{elem...}
	parts := append([]string{testE2EDir, "fixtures"}, elem...)
	return filepath.Join(parts...)
}
