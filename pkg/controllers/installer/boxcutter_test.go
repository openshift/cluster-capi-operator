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

package installer

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"pkg.package-operator.run/boxcutter"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/runtimetransformer"
)

// stubTransformer is a test double for runtimetransformer.RuntimeTransformer.
type stubTransformer struct {
	opts []boxcutter.PhaseReconcileOption
	err  error
}

func (s *stubTransformer) TransformObject(_ context.Context, _ client.Object) ([]boxcutter.PhaseReconcileOption, error) {
	return s.opts, s.err
}

// installerRevisionFromProfiles builds a bare InstallerRevision from the named
// provider profiles without writing anything to the cluster.
func installerRevisionFromProfiles(names ...string) revisiongenerator.InstallerRevision {
	GinkgoHelper()

	profiles := lookupProfiles(names...)
	rendered, err := revisiongenerator.NewRenderedRevision(profiles)
	Expect(err).NotTo(HaveOccurred(), "NewRenderedRevision should not fail for valid profiles")

	rev, err := rendered.ForInstall("4.18.0-test", 1)
	Expect(err).NotTo(HaveOccurred(), "ForInstall should not fail for a valid rendered revision")

	return rev
}

var _ = Describe("toBoxcutterRevision", func() {
	Describe("construction", func() {
		It("should return a Revision with the name of the InstallerRevision", func() {
			rev := installerRevisionFromProfiles(providerCore)

			bcRev, err := toBoxcutterRevision(context.Background(), rev, nil)
			Expect(err).NotTo(HaveOccurred())

			Expect(bcRev.GetName()).To(Equal(string(rev.RevisionName())),
				"returned Revision should carry the same name as the InstallerRevision")
		})
	})

	Describe("GetPhases idempotency", func() {
		DescribeTable("should return stable phases on every call",
			func(providerName string, wantPhaseCount int) {
				rev := installerRevisionFromProfiles(providerName)

				bcRev, err := toBoxcutterRevision(context.Background(), rev, nil)
				Expect(err).NotTo(HaveOccurred())

				first := bcRev.GetPhases()
				second := bcRev.GetPhases()

				Expect(second).To(HaveLen(wantPhaseCount),
					"expected %d phase(s) for provider %q", wantPhaseCount, providerName)

				for i := range first {
					Expect(second[i].GetName()).To(Equal(first[i].GetName()),
						"phase[%d] name must be stable across GetPhases calls", i)
					Expect(second[i].GetObjects()).To(HaveLen(len(first[i].GetObjects())),
						"phase[%d] object count must be stable across GetPhases calls", i)
				}
			},
			Entry("objects only — one objects phase", providerCore, 1),
			Entry("CRDs only — one CRD phase", providerCRD, 1),
			Entry("CRDs and objects — two phases", providerMixed, 2),
			Entry("adopt-existing annotation is stable across calls", providerAdoptExisting, 1),
		)
	})

	Describe("transformer integration", func() {
		It("should return an error when a transformer fails", func() {
			stub := &stubTransformer{err: errors.New("transform failed")}
			rev := installerRevisionFromProfiles(providerCore)

			_, err := toBoxcutterRevision(context.Background(), rev, []runtimetransformer.RuntimeTransformer{stub})

			Expect(err).To(MatchError(ContainSubstring("transform failed")))
		})

		It("should include options returned by transformers in phase reconcile options", func() {
			rev := installerRevisionFromProfiles(providerCore)

			base, err := toBoxcutterRevision(context.Background(), rev, nil)
			Expect(err).NotTo(HaveOccurred())

			baseOptCount := len(base.GetPhases()[0].GetReconcileOptions())

			stub := &stubTransformer{opts: []boxcutter.PhaseReconcileOption{
				boxcutter.WithCollisionProtection(boxcutter.CollisionProtectionNone),
			}}
			withTfm, err := toBoxcutterRevision(context.Background(), rev, []runtimetransformer.RuntimeTransformer{stub})
			Expect(err).NotTo(HaveOccurred())

			Expect(len(withTfm.GetPhases()[0].GetReconcileOptions())).To(
				BeNumerically(">", baseOptCount),
				"transformer options should augment the phase reconcile options",
			)
		})
	})
})
