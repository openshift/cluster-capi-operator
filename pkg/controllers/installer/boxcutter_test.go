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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"pkg.package-operator.run/boxcutter"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/manifesttransformer"
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

// stubTransformer is a test double for manifesttransformer.ManifestTransformer.
type stubTransformer struct {
	opts        []boxcutter.ObjectReconcileOption
	err         error
	validateErr error
}

func (s *stubTransformer) TransformObject(_ context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, []boxcutter.ObjectReconcileOption, error) {
	return obj, s.opts, s.err
}

func (s *stubTransformer) Validate(_ *unstructured.Unstructured) error {
	return s.validateErr
}

func (s *stubTransformer) WithRevision(_ context.Context, _ revisiongenerator.ParsedRevision) manifesttransformer.ManifestTransformer {
	return s
}

func (s *stubTransformer) WithComponent(_ context.Context, _ revisiongenerator.ParsedComponent) manifesttransformer.ManifestTransformer {
	return s
}

var _ manifesttransformer.ManifestTransformer = &stubTransformer{}

// fnTransformer adapts a plain function to the manifesttransformer.ManifestTransformer
// interface, letting tests express skip/mutate/observe behaviour inline.
type fnTransformer struct {
	fn func(obj *unstructured.Unstructured) (*unstructured.Unstructured, []boxcutter.ObjectReconcileOption, error)
}

func (f *fnTransformer) TransformObject(_ context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, []boxcutter.ObjectReconcileOption, error) {
	return f.fn(obj)
}

func (f *fnTransformer) Validate(_ *unstructured.Unstructured) error {
	return nil
}

func (f *fnTransformer) WithRevision(_ context.Context, _ revisiongenerator.ParsedRevision) manifesttransformer.ManifestTransformer {
	return f
}

func (f *fnTransformer) WithComponent(_ context.Context, _ revisiongenerator.ParsedComponent) manifesttransformer.ManifestTransformer {
	return f
}

var _ manifesttransformer.ManifestTransformer = &fnTransformer{}

// installerRevisionFromProfiles builds a bare InstallerRevision from the named
// provider profiles without writing anything to the cluster.
func installerRevisionFromProfiles(names ...string) revisiongenerator.InstallerRevision {
	GinkgoHelper()

	profiles := lookupProfiles(names...)
	parsed, err := revisiongenerator.NewParsedRevision(profiles)
	Expect(err).NotTo(HaveOccurred(), "NewParsedRevision should not fail for valid profiles")

	rev, err := parsed.ForInstall("4.18.0-test", 1)
	Expect(err).NotTo(HaveOccurred(), "ForInstall should not fail for a valid parsed revision")

	return rev
}

var _ = Describe("toBoxcutterRevision", func() {
	Describe("construction", func() {
		It("should return a Revision with the name of the InstallerRevision", func() {
			rev := installerRevisionFromProfiles(providerCore)

			bcRev, err := toBoxcutterRevision(context.Background(), rev, nil, noopCollector)
			Expect(err).NotTo(HaveOccurred())

			Expect(bcRev.GetName()).To(Equal(string(rev.RevisionName())),
				"returned Revision should carry the same name as the InstallerRevision")
		})
	})

	Describe("GetPhases idempotency", func() {
		DescribeTable("should return stable phases on every call",
			func(providerName string, wantPhaseCount int) {
				rev := installerRevisionFromProfiles(providerName)

				bcRev, err := toBoxcutterRevision(context.Background(), rev, nil, noopCollector)
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

	Describe("CRD splitting", func() {
		It("splits a component with CRDs and objects into a '-crds' phase and an objects phase", func() {
			rev := installerRevisionFromProfiles(providerMixed)

			bcRev, err := toBoxcutterRevision(context.Background(), rev, nil, noopCollector)
			Expect(err).NotTo(HaveOccurred())

			phases := bcRev.GetPhases()
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

			bcRev, err := toBoxcutterRevision(context.Background(), rev, nil, noopCollector)
			Expect(err).NotTo(HaveOccurred())

			phases := bcRev.GetPhases()
			Expect(phases).To(HaveLen(1))
			Expect(phases[0].GetName()).To(Equal(providerCore))
			Expect(objectKinds(phases[0].GetObjects())).To(ConsistOf("ConfigMap"))
		})

		It("does not create a plain objects phase for a component with only CRDs", func() {
			rev := installerRevisionFromProfiles(providerCRD)

			bcRev, err := toBoxcutterRevision(context.Background(), rev, nil, noopCollector)
			Expect(err).NotTo(HaveOccurred())

			phases := bcRev.GetPhases()
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

				collectObjects := func(obj *unstructured.Unstructured) {
					collectedRefs = append(collectedRefs, objectRef(obj.GetKind(), obj.GetName()))
				}
				_, err := toBoxcutterRevision(context.Background(), rev, nil, collectObjects)
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

	Describe("transformer integration", func() {
		It("should return an error when a transformer fails", func() {
			stub := &stubTransformer{err: errors.New("transform failed")}
			rev := installerRevisionFromProfiles(providerCore)

			_, err := toBoxcutterRevision(context.Background(), rev, []manifesttransformer.ManifestTransformer{stub}, nil)

			Expect(err).To(MatchError(ContainSubstring("transform failed")))
		})

		It("should include options returned by transformers in phase reconcile options", func() {
			rev := installerRevisionFromProfiles(providerCore)

			base, err := toBoxcutterRevision(context.Background(), rev, nil, nil)
			Expect(err).NotTo(HaveOccurred())

			baseOptCount := len(base.GetPhases()[0].GetReconcileOptions())

			stub := &stubTransformer{opts: []boxcutter.ObjectReconcileOption{
				boxcutter.WithCollisionProtection(boxcutter.CollisionProtectionNone),
			}}
			withTfm, err := toBoxcutterRevision(context.Background(), rev, []manifesttransformer.ManifestTransformer{stub}, nil)
			Expect(err).NotTo(HaveOccurred())

			Expect(len(withTfm.GetPhases()[0].GetReconcileOptions())).To(
				BeNumerically(">", baseOptCount),
				"transformer options should augment the phase reconcile options",
			)
		})

		It("should omit an object from its phase when a transformer returns a nil object", func() {
			rev := installerRevisionFromProfiles(providerMixed)

			skipConfigMaps := &fnTransformer{fn: func(obj *unstructured.Unstructured) (*unstructured.Unstructured, []boxcutter.ObjectReconcileOption, error) {
				if obj.GetKind() == "ConfigMap" {
					return nil, nil, nil
				}

				return obj, nil, nil
			}}

			bcRev, err := toBoxcutterRevision(context.Background(), rev, []manifesttransformer.ManifestTransformer{skipConfigMaps}, nil)
			Expect(err).NotTo(HaveOccurred())

			phases := bcRev.GetPhases()

			crdPhase := findPhase(phases, providerMixed+"-crds")
			Expect(objectKinds(crdPhase.GetObjects())).To(ConsistOf("CustomResourceDefinition"),
				"objects not matched by the skip transformer should be unaffected")

			objectsPhase := findPhase(phases, providerMixed)
			Expect(objectsPhase.GetObjects()).To(BeEmpty(),
				"an object skipped by a transformer (nil return) must not appear in any phase")
		})

		It("should not invoke later transformers for an object a prior transformer already skipped", func() {
			rev := installerRevisionFromProfiles(providerCore)

			skip := &fnTransformer{fn: func(*unstructured.Unstructured) (*unstructured.Unstructured, []boxcutter.ObjectReconcileOption, error) {
				return nil, nil, nil
			}}

			var secondCalled bool

			recordCall := &fnTransformer{fn: func(obj *unstructured.Unstructured) (*unstructured.Unstructured, []boxcutter.ObjectReconcileOption, error) {
				secondCalled = true
				return obj, nil, nil
			}}

			_, err := toBoxcutterRevision(context.Background(), rev, []manifesttransformer.ManifestTransformer{skip, recordCall}, nil)
			Expect(err).NotTo(HaveOccurred())

			Expect(secondCalled).To(BeFalse(),
				"a transformer must not run on an object a prior transformer already skipped")
		})

		It("should pass the output of one transformer as the input to the next", func() {
			rev := installerRevisionFromProfiles(providerCore)

			const renamedTo = "renamed-by-first-transformer"

			rename := &fnTransformer{fn: func(obj *unstructured.Unstructured) (*unstructured.Unstructured, []boxcutter.ObjectReconcileOption, error) {
				renamed := obj.DeepCopy()
				renamed.SetName(renamedTo)

				return renamed, nil, nil
			}}

			var sawName string

			record := &fnTransformer{fn: func(obj *unstructured.Unstructured) (*unstructured.Unstructured, []boxcutter.ObjectReconcileOption, error) {
				sawName = obj.GetName()
				return obj, nil, nil
			}}

			bcRev, err := toBoxcutterRevision(context.Background(), rev, []manifesttransformer.ManifestTransformer{rename, record}, nil)
			Expect(err).NotTo(HaveOccurred())

			Expect(sawName).To(Equal(renamedTo),
				"the second transformer should observe the first transformer's output, not the original object")
			Expect(bcRev.GetPhases()[0].GetObjects()[0].GetName()).To(Equal(renamedTo),
				"the final phase should contain the transformed object, not the original")
		})

		It("should apply transformers to CRDs as well as plain objects", func() {
			rev := installerRevisionFromProfiles(providerCRD)

			var sawKind string

			record := &fnTransformer{fn: func(obj *unstructured.Unstructured) (*unstructured.Unstructured, []boxcutter.ObjectReconcileOption, error) {
				sawKind = obj.GetKind()
				return obj, nil, nil
			}}

			_, err := toBoxcutterRevision(context.Background(), rev, []manifesttransformer.ManifestTransformer{record}, nil)
			Expect(err).NotTo(HaveOccurred())

			Expect(sawKind).To(Equal("CustomResourceDefinition"), "transformers must also run against CRD objects")
		})

		It("should include the failing object's identity in the returned error", func() {
			rev := installerRevisionFromProfiles(providerCore)
			stub := &stubTransformer{err: errors.New("boom")}

			_, err := toBoxcutterRevision(context.Background(), rev, []manifesttransformer.ManifestTransformer{stub}, nil)

			Expect(err).To(MatchError(ContainSubstring(coreCMName)),
				"the error should identify which object failed transformation")
		})

		It("should invoke transformers exactly once per object, even if GetPhases is called multiple times", func() {
			rev := installerRevisionFromProfiles(providerCore)

			var callCount int

			counting := &fnTransformer{fn: func(obj *unstructured.Unstructured) (*unstructured.Unstructured, []boxcutter.ObjectReconcileOption, error) {
				callCount++
				return obj, nil, nil
			}}

			bcRev, err := toBoxcutterRevision(context.Background(), rev, []manifesttransformer.ManifestTransformer{counting}, nil)
			Expect(err).NotTo(HaveOccurred())

			_ = bcRev.GetPhases()
			_ = bcRev.GetPhases()

			Expect(callCount).To(Equal(1),
				"transformation happens once during construction, not on every GetPhases call")
		})
	})

	Describe("error aggregation", func() {
		It("aggregates errors from multiple objects in the same phase", func() {
			// providerManyClusterScoped has ten ClusterRoles and no CRDs, so all
			// of them are built into a single objects phase for the component.
			rev := installerRevisionFromProfiles(providerManyClusterScoped)
			stub := &stubTransformer{err: errors.New("boom")}

			_, err := toBoxcutterRevision(context.Background(), rev, []manifesttransformer.ManifestTransformer{stub}, nil)

			Expect(err).To(MatchError(SatisfyAll(
				ContainSubstring("/test-cr-1"),
				ContainSubstring("/test-cr-2"),
			)), "expected failures from multiple objects within the same phase")
		})

		It("aggregates errors from multiple transformers on the same object", func() {
			rev := installerRevisionFromProfiles(providerCore)
			stubA := &stubTransformer{err: errors.New("first failure")}
			stubB := &stubTransformer{err: errors.New("second failure")}

			_, err := toBoxcutterRevision(context.Background(), rev,
				[]manifesttransformer.ManifestTransformer{stubA, stubB}, nil)

			Expect(err).To(MatchError(SatisfyAll(
				ContainSubstring("first failure"),
				ContainSubstring("second failure"),
			)), "expected both transformer failures for the object in a single joined error")
		})

		It("aggregates errors across the CRD and non-CRD phases of the same component", func() {
			// providerMixed has one CRD and one ConfigMap, in the same component,
			// but built into two separate phases (crds, objects).
			rev := installerRevisionFromProfiles(providerMixed)
			stub := &stubTransformer{err: errors.New("boom")}

			_, err := toBoxcutterRevision(context.Background(), rev, []manifesttransformer.ManifestTransformer{stub}, nil)

			Expect(err).To(MatchError(SatisfyAll(
				ContainSubstring("/testgadgets.test.example.com"),
				ContainSubstring("default/test-cm-mixed"),
			)), "expected failures from both the CRD phase and the objects phase")
		})

		It("aggregates errors across multiple components rather than stopping at the first", func() {
			rev := installerRevisionFromProfiles(providerCore, providerInfra)
			stub := &stubTransformer{err: errors.New("boom")}

			_, err := toBoxcutterRevision(context.Background(), rev, []manifesttransformer.ManifestTransformer{stub}, nil)

			Expect(err).To(MatchError(SatisfyAll(
				ContainSubstring("default/test-cm-core"),
				ContainSubstring("default/test-cm-infra"),
			)), "expected failures from both components, not just the first one processed")
		})

		It("does not build a Revision when any object fails transformation", func() {
			rev := installerRevisionFromProfiles(providerCore, providerInfra)
			stub := &stubTransformer{err: errors.New("boom")}

			bcRev, err := toBoxcutterRevision(context.Background(), rev, []manifesttransformer.ManifestTransformer{stub}, nil)

			Expect(err).To(HaveOccurred())
			Expect(bcRev).To(BeNil())
		})
	})
})
