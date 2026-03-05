// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/ginkgo/v2/reporters"
)

const diagnosticsEntryName = "diagnostics"

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cluster API Suite")
}

var _ = BeforeSuite(func() {
	InitCommonVariables()
})

// JustAfterEach runs before AfterEach/DeferCleanup, so tracked resources are
// still present when we collect diagnostics for spec-body failures.
var _ = JustAfterEach(func(ctx SpecContext) {
	if CurrentSpecReport().Failed() {
		AddReportEntry(diagnosticsEntryName,
			collectTrackedResourceDiagnostics(ctx),
			ReportEntryVisibilityNever)
	}
}, NodeTimeout(5*time.Minute))

// ReportAfterEach catches cleanup flakes (e.g. a rogue finalizer preventing
// deletion). Tracked resources may still exist since cleanup failed to remove
// them.
var _ = ReportAfterEach(func(ctx SpecContext, report SpecReport) {
	if report.Failed() {
		if _, found := diagnosticsFromReport(report); !found {
			AddReportEntry(diagnosticsEntryName,
				collectTrackedResourceDiagnostics(ctx),
				ReportEntryVisibilityNever)
		}
	}

	resourcesUnderTest = nil
}, NodeTimeout(5*time.Minute))

// ReportAfterSuite generates a JUnit XML report with tracked resource
// diagnostics appended to the failure description. This replaces the
// --junit-report ginkgo flag so that Spyglass renders diagnostics inline
// with the failure instead of hiding them behind "open stderr".
var _ = ReportAfterSuite("junit with diagnostics", func(report Report) {
	artifactDir := os.Getenv("ARTIFACT_DIR")
	if artifactDir == "" {
		return
	}

	for i := range report.SpecReports {
		sr := &report.SpecReports[i]
		if !sr.Failed() {
			continue
		}

		if diag, found := diagnosticsFromReport(*sr); found {
			sr.Failure.Message += "\n\n" + diag
		}
	}

	dst := filepath.Join(artifactDir, "junit_cluster_capi_operator.xml")
	cfg := reporters.JunitReportConfig{OmitFailureMessageAttr: true}
	if err := reporters.GenerateJUnitReportWithConfig(report, dst, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: failed to write JUnit report to %s: %v\n", dst, err)
	}
})

// diagnosticsFromReport extracts the diagnostics string from a SpecReport's
// report entries. Returns the diagnostics and true if found.
func diagnosticsFromReport(sr SpecReport) (string, bool) {
	for _, entry := range sr.ReportEntries {
		if entry.Name == diagnosticsEntryName {
			return entry.StringRepresentation(), true
		}
	}

	return "", false
}
