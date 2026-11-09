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
package upgradeable

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestUpgradeable(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Upgradeable Controller Suite")
}

var _ = Describe("resolveTarget", func() {
	It("selects the newest next-minor release and handles prereleases", func() {
		cv := &configv1.ClusterVersion{}
		cv.Status.Desired.Version = "4.16.0"
		cv.Status.AvailableUpdates = []configv1.Release{{Version: "4.17.0-rc.1"}, {Version: "4.17.0"}, {Version: "4.18.0"}, {Version: "4.17.1"}}
		target, err := resolveTarget(cv)
		Expect(err).NotTo(HaveOccurred())
		Expect(target.Version).To(Equal("4.17.0"))
	})
	It("prefers available updates on ties and uses eligible conditional updates", func() {
		cv := &configv1.ClusterVersion{}
		cv.Status.Desired.Version = "4.16.0"
		available := configv1.Release{Version: "4.17.0", Image: "available"}
		cv.Status.AvailableUpdates = []configv1.Release{available}
		cv.Status.ConditionalUpdates = []configv1.ConditionalUpdate{{Release: configv1.Release{Version: "4.17.0", Image: "conditional"}}}
		target, err := resolveTarget(cv)
		Expect(err).NotTo(HaveOccurred())
		Expect(target.Image).To(Equal("available"))
	})
	It("returns nil when no eligible target and errors on invalid current version", func() {
		cv := &configv1.ClusterVersion{}
		cv.Status.Desired.Version = "4.16.0"
		cv.Status.AvailableUpdates = []configv1.Release{{Version: "4.18.0"}, {Version: "invalid"}}
		target, err := resolveTarget(cv)
		Expect(err).NotTo(HaveOccurred())
		Expect(target).To(BeNil())

		cv.Status.Desired.Version = "invalid"
		_, err = resolveTarget(cv)
		Expect(err).To(HaveOccurred())
	})
	It("should include demoted conditional updates and order prereleases semantically", func() {
		cv := &configv1.ClusterVersion{}
		cv.Status.Desired.Version = "4.16.0"
		cv.Status.AvailableUpdates = []configv1.Release{{Version: "4.17.0-0.nightly-2026-01-01"}, {Version: "4.17.0-ec.4"}}
		cv.Status.ConditionalUpdates = []configv1.ConditionalUpdate{{
			Release:    configv1.Release{Version: "4.17.0-ec.10", Image: "demoted"},
			Conditions: []metav1.Condition{{Type: "Recommended", Status: metav1.ConditionFalse}},
		}}
		target, err := resolveTarget(cv)
		Expect(err).NotTo(HaveOccurred())
		Expect(target.Image).To(Equal("demoted"))
	})
	It("should reconcile when the current version changes without an update list delta", func() {
		old := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: clusterVersionName}}
		old.Status.Desired.Version = "4.16.0"
		updated := old.DeepCopy()
		updated.Status.Desired.Version = "4.17.0"
		Expect(clusterVersionUpdatesChanged().Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated})).To(BeTrue())
		Expect(clusterVersionUpdatesChanged().Update(event.UpdateEvent{ObjectOld: old, ObjectNew: old.DeepCopy()})).To(BeFalse())
	})
})
