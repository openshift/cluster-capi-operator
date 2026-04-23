//go:build !e2e

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
	"context"
	"encoding/json"
	"testing"

	"github.com/onsi/ginkgo/v2/types"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestDiagnostics exercises the diagnostics-to-JUnit reporting logic
// without a live cluster. Run independently via:
//
//	go test -run TestDiagnostics ./e2e/...
//
// BeforeSuite (which requires a cluster) does not fire because RunSpecs
// is not called.
func TestDiagnostics(t *testing.T) {
	t.Run("live resource appears in output", func(t *testing.T) {
		g := gomega.NewWithT(t)
		withFakeClient(t, func() {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cm", Namespace: "default"},
				Data:       map[string]string{"key": "value"},
			}
			g.Expect(cl.Create(ctx, cm)).To(gomega.Succeed())

			resourcesUnderTest = []client.Object{cm}

			diag := collectTrackedResourceDiagnostics(ctx)
			g.Expect(diag).To(gomega.ContainSubstring("test-cm"))
			g.Expect(diag).To(gomega.ContainSubstring("Test Failure Diagnostics"))
		})
	})

	t.Run("deleted resource shows not found", func(t *testing.T) {
		g := gomega.NewWithT(t)
		withFakeClient(t, func() {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "gone-cm", Namespace: "default"},
			}
			resourcesUnderTest = []client.Object{cm}

			diag := collectTrackedResourceDiagnostics(ctx)
			g.Expect(diag).To(gomega.ContainSubstring("not found (deleted)"))
			g.Expect(diag).To(gomega.ContainSubstring("gone-cm"))
		})
	})

	t.Run("diagnosticsFromReport extracts entry", func(t *testing.T) {
		g := gomega.NewWithT(t)

		diagText := "\n=== Test Failure Diagnostics ===\nsome output\n=== End Test Failure Diagnostics ===\n"
		sr := types.SpecReport{
			ReportEntries: types.ReportEntries{
				{Name: diagnosticsEntryName, Value: types.WrapEntryValue(diagText)},
			},
		}

		result, found := diagnosticsFromReport(sr)
		g.Expect(found).To(gomega.BeTrue())
		g.Expect(result).To(gomega.Equal(diagText))
	})

	t.Run("diagnosticsFromReport returns false when absent", func(t *testing.T) {
		g := gomega.NewWithT(t)

		sr := types.SpecReport{}

		_, found := diagnosticsFromReport(sr)
		g.Expect(found).To(gomega.BeFalse())
	})

	t.Run("diagnostics survive JSON round-trip", func(t *testing.T) {
		g := gomega.NewWithT(t)

		diagText := "\n=== Test Failure Diagnostics ===\nresource YAML here\n=== End Test Failure Diagnostics ===\n"
		sr := types.SpecReport{
			ReportEntries: types.ReportEntries{
				{Name: diagnosticsEntryName, Value: types.WrapEntryValue(diagText)},
			},
		}

		data, err := json.Marshal(sr)
		g.Expect(err).NotTo(gomega.HaveOccurred())

		var deserialized types.SpecReport
		g.Expect(json.Unmarshal(data, &deserialized)).To(gomega.Succeed())

		result, found := diagnosticsFromReport(deserialized)
		g.Expect(found).To(gomega.BeTrue())
		g.Expect(result).To(gomega.Equal(diagText))
	})
}

// withFakeClient swaps the package-level cl, ctx, and test state to a fake
// client for the duration of fn, restoring originals via t.Cleanup.
func withFakeClient(t *testing.T, fn func()) {
	t.Helper()

	origCl, origCtx := cl, ctx
	origResources, origPlatform := resourcesUnderTest, platform

	cl = fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	ctx = context.Background()
	resourcesUnderTest = nil
	platform = ""

	t.Cleanup(func() {
		cl, ctx = origCl, origCtx
		resourcesUnderTest, platform = origResources, origPlatform
	})

	fn()
}
