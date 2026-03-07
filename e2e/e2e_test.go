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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/ginkgo/v2/reporters"
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cluster API Suite")
}

var _ = BeforeSuite(func() {
	InitCommonVariables()
})

var _ = ReportAfterEach(func(report SpecReport) {
	if report.Failed() {
		diag := collectTrackedResourceDiagnostics()
		specDiagnostics[report.LeafNodeLocation.String()] = diag
	}

	resourcesUnderTest = nil
})

// ReportAfterSuite generates a JUnit XML report with tracked resource
// diagnostics appended to the failure description. This replaces the
// --junit-report ginkgo flag so that Spyglass renders diagnostics inline
// with the failure instead of hiding them behind "open stderr".
var _ = ReportAfterSuite("junit with diagnostics", func(report Report) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "WARNING: ReportAfterSuite panicked: %v\n", r)
		}
	}()

	artifactDir := os.Getenv("ARTIFACT_DIR")
	if artifactDir == "" {
		return
	}

	for i := range report.SpecReports {
		sr := &report.SpecReports[i]
		if !sr.Failed() {
			continue
		}

		if diag, ok := specDiagnostics[sr.LeafNodeLocation.String()]; ok {
			sr.Failure.Message += "\n\n" + diag
		}
	}

	dst := filepath.Join(artifactDir, "junit_cluster_capi_operator.xml")
	if err := reporters.GenerateJUnitReport(report, dst); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: failed to write JUnit report to %s: %v\n", dst, err)
	}
})
