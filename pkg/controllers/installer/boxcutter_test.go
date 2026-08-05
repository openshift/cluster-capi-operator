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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"pkg.package-operator.run/boxcutter"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

// noopCollector is a collectObjects callback that does nothing, for tests that
// don't care about collected objects.
func noopCollector(*unstructured.Unstructured) {}

// objectRef returns a stable identifier for an object, for asserting object
// identity without depending on object ordering.
func objectRef(kind, name string) string {
	return kind + "/" + name
}

// findPhase returns the phase with the given name, failing the test if none is found.
func findPhase(phases []boxcutter.Phase, name string) boxcutter.Phase {
	GinkgoHelper()

	for _, phase := range phases {
		if phase.GetName() == name {
			return phase
		}
	}

	Fail(fmt.Sprintf("phase %q not found", name))

	return nil
}

// objectKinds returns the Kind of each object, for asserting phase contents
// without depending on object ordering.
func objectKinds(objs []client.Object) []string {
	return util.SliceMap(objs, func(obj client.Object) string {
		return obj.GetObjectKind().GroupVersionKind().Kind
	})
}

// installerRevisionFromProfiles builds an InstallerRevision from named provider profiles
// already registered in providersByName. Delegates to the package-level lookupProfiles helper
// so the heavy profile setup stays in BeforeSuite.
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

			bcRev := toBoxcutterRevision(rev, noopCollector)

			Expect(bcRev.GetName()).To(Equal(string(rev.RevisionName())),
				"returned Revision should carry the same name as the InstallerRevision")
		})
	})

	Describe("GetPhases idempotency", func() {
		DescribeTable("should return stable phases on every call",
			func(providerName string, wantPhaseCount int) {
				rev := installerRevisionFromProfiles(providerName)

				bcRev := toBoxcutterRevision(rev, noopCollector)

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

	Describe("CRD splitting", func() {
		It("splits a component with CRDs and objects into a '-crds' phase and an objects phase", func() {
			rev := installerRevisionFromProfiles(providerMixed)

			phases := toBoxcutterRevision(rev, noopCollector).GetPhases()
			Expect(phases).To(HaveLen(2))

			crdPhase := findPhase(phases, providerMixed+"-crds")
			Expect(objectKinds(crdPhase.GetObjects())).To(ConsistOf("CustomResourceDefinition"),
				"the '-crds' phase should contain only CRDs")

			objectsPhase := findPhase(phases, providerMixed)
			Expect(objectKinds(objectsPhase.GetObjects())).To(ConsistOf("ConfigMap"),
				"the base phase should contain only non-CRD objects")
		})

		It("does not create a '-crds' phase for a component with no CRDs", func() {
			rev := installerRevisionFromProfiles(providerCore)

			phases := toBoxcutterRevision(rev, noopCollector).GetPhases()
			Expect(phases).To(HaveLen(1))
			Expect(phases[0].GetName()).To(Equal(providerCore))
			Expect(objectKinds(phases[0].GetObjects())).To(ConsistOf("ConfigMap"))
		})

		It("does not create a plain objects phase for a component with only CRDs", func() {
			rev := installerRevisionFromProfiles(providerCRD)

			phases := toBoxcutterRevision(rev, noopCollector).GetPhases()
			Expect(phases).To(HaveLen(1))
			Expect(phases[0].GetName()).To(Equal(providerCRD + "-crds"))
			Expect(objectKinds(phases[0].GetObjects())).To(ConsistOf("CustomResourceDefinition"))
		})
	})

	Describe("collectObjects callback", func() {
		testWidgetCRDName := fmt.Sprintf("testwidgets.%s", testCRDGVK.Group)
		testGadgetCRDName := fmt.Sprintf("testgadgets.%s", mixedCRDGVK.Group)

		DescribeTable("is called once for every object in every component",
			func(wantRefs []string, providerNames ...string) {
				rev := installerRevisionFromProfiles(providerNames...)

				var collectedRefs []string

				toBoxcutterRevision(rev, func(obj *unstructured.Unstructured) {
					collectedRefs = append(collectedRefs, objectRef(obj.GetKind(), obj.GetName()))
				})

				Expect(collectedRefs).To(ConsistOf(wantRefs),
					"collectObjects should be called exactly once for every object that ends up in a phase")
			},
			Entry("objects only",
				[]string{objectRef("ConfigMap", coreCMName)},
				providerCore),
			Entry("CRDs only",
				[]string{objectRef("CustomResourceDefinition", testWidgetCRDName)},
				providerCRD),
			Entry("CRDs and objects in the same component",
				[]string{
					objectRef("CustomResourceDefinition", testGadgetCRDName),
					objectRef("ConfigMap", mixedCMName),
				},
				providerMixed),
			Entry("multiple components",
				[]string{
					objectRef("ConfigMap", coreCMName),
					objectRef("CustomResourceDefinition", testGadgetCRDName),
					objectRef("ConfigMap", mixedCMName),
					objectRef("CustomResourceDefinition", testWidgetCRDName),
				},
				providerCore, providerMixed, providerCRD),
		)
	})
})
