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

// mustBoxcutterRevision calls toBoxcutterRevision and fails the test on error.
func mustBoxcutterRevision(rev revisiongenerator.InstallerRevision, collect func(*unstructured.Unstructured), unmanagedCRDs []string) boxcutter.Revision {
	GinkgoHelper()

	bcRev, err := toBoxcutterRevision(rev, collect, unmanagedCRDs)
	Expect(err).NotTo(HaveOccurred())

	return bcRev
}

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

			bcRev := mustBoxcutterRevision(rev, noopCollector, nil)

			Expect(bcRev.GetName()).To(Equal(string(rev.RevisionName())),
				"returned Revision should carry the same name as the InstallerRevision")
		})
	})

	Describe("GetPhases idempotency", func() {
		DescribeTable("should return stable phases on every call",
			func(providerName string, wantPhaseCount int) {
				rev := installerRevisionFromProfiles(providerName)

				bcRev := mustBoxcutterRevision(rev, noopCollector, nil)

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

			phases := mustBoxcutterRevision(rev, noopCollector, nil).GetPhases()
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

			phases := mustBoxcutterRevision(rev, noopCollector, nil).GetPhases()
			Expect(phases).To(HaveLen(1))
			Expect(phases[0].GetName()).To(Equal(providerCore))
			Expect(objectKinds(phases[0].GetObjects())).To(ConsistOf("ConfigMap"))
		})

		It("does not create a plain objects phase for a component with only CRDs", func() {
			rev := installerRevisionFromProfiles(providerCRD)

			phases := mustBoxcutterRevision(rev, noopCollector, nil).GetPhases()
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

				_, err := toBoxcutterRevision(rev, func(obj *unstructured.Unstructured) {
					collectedRefs = append(collectedRefs, objectRef(obj.GetKind(), obj.GetName()))
				}, nil)
				Expect(err).NotTo(HaveOccurred())

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

	Describe("unmanaged CRDs", func() {
		testWidgetCRDName := fmt.Sprintf("testwidgets.%s", testCRDGVK.Group)
		testGadgetCRDName := fmt.Sprintf("testgadgets.%s", mixedCRDGVK.Group)

		It("should produce a compatibility phase as the first phase", func() {
			rev := installerRevisionFromProfiles(providerCRD)
			bcRev := mustBoxcutterRevision(rev, noopCollector, []string{testWidgetCRDName})

			phases := bcRev.GetPhases()
			Expect(phases).To(HaveLen(1))
			Expect(phases[0].GetName()).To(Equal("compatibility-requirements"))
			Expect(phases[0].GetObjects()).To(HaveLen(1))
			Expect(phases[0].GetObjects()[0].GetName()).To(Equal("ccapio-" + testWidgetCRDName))
		})

		It("should filter the unmanaged CRD from the component CRD phase", func() {
			rev := installerRevisionFromProfiles(providerMixed)
			bcRev := mustBoxcutterRevision(rev, noopCollector, []string{testGadgetCRDName})

			phases := bcRev.GetPhases()
			// compatibility-requirements + mixed (ConfigMap only, no CRD phase since CRD was unmanaged)
			Expect(phases).To(HaveLen(2))
			Expect(phases[0].GetName()).To(Equal("compatibility-requirements"))
			Expect(phases[1].GetName()).To(Equal(providerMixed))
			Expect(objectKinds(phases[1].GetObjects())).To(ConsistOf("ConfigMap"))
		})

		It("should collect multiple unmanaged CRDs from different components into a single compatibility phase", func() {
			rev := installerRevisionFromProfiles(providerCRD, providerMixed)
			bcRev := mustBoxcutterRevision(rev, noopCollector, []string{testWidgetCRDName, testGadgetCRDName})

			phases := bcRev.GetPhases()
			compatPhase := findPhase(phases, "compatibility-requirements")
			Expect(compatPhase.GetObjects()).To(HaveLen(2))
		})

		It("should return an error when an unmanaged CRD is not found in any component", func() {
			rev := installerRevisionFromProfiles(providerCore)
			_, err := toBoxcutterRevision(rev, noopCollector, []string{"nonexistent.example.com"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})

		It("should set the managed label on CompatibilityRequirement objects", func() {
			rev := installerRevisionFromProfiles(providerCRD)
			bcRev := mustBoxcutterRevision(rev, noopCollector, []string{testWidgetCRDName})

			crObj := bcRev.GetPhases()[0].GetObjects()[0]
			Expect(crObj.GetLabels()).To(HaveKeyWithValue(revisiongenerator.ManagedLabelKey, "compatibility-requirements"))
		})

		It("should call collectObjects for CompatibilityRequirement objects", func() {
			rev := installerRevisionFromProfiles(providerCRD)

			var collectedRefs []string
			mustBoxcutterRevision(rev, func(obj *unstructured.Unstructured) {
				collectedRefs = append(collectedRefs, objectRef(obj.GetKind(), obj.GetName()))
			}, []string{testWidgetCRDName})

			Expect(collectedRefs).To(ContainElement(objectRef("CompatibilityRequirement", "ccapio-"+testWidgetCRDName)))
		})
	})
})
