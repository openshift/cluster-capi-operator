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

package migrationcommon

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	capiv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta2"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("Paused annotation helpers", func() {
	var (
		k             komega.Komega
		capiNamespace string
		machine       *clusterv1.Machine
	)

	BeforeEach(func() {
		namespace := corev1resourcebuilder.Namespace().
			WithGenerateName("migrationcommon-pause-").Build()
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed(), "CAPI namespace should be created")
		capiNamespace = namespace.GetName()

		machine = capiv1resourcebuilder.Machine().
			WithNamespace(capiNamespace).
			WithName("foo").
			Build()
		Expect(k8sClient.Create(ctx, machine)).To(Succeed(), "CAPI machine should be created")

		k = komega.New(k8sClient)
	})

	AfterEach(func() {
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, capiNamespace,
			&clusterv1.Machine{},
		)
	})

	Describe("AddPausedAnnotation", func() {
		Context("when the object has changed since it was read", func() {
			It("should fail with a conflict", func() {
				staleCopy := &clusterv1.Machine{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(machine), staleCopy)).To(Succeed(), "stale copy should be fetched")

				liveMachine := &clusterv1.Machine{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(machine), liveMachine)).To(Succeed(), "live machine should be fetched")

				if liveMachine.Annotations == nil {
					liveMachine.Annotations = map[string]string{}
				}

				liveMachine.Annotations["test.openshift.io/stale"] = "true"
				Expect(k8sClient.Update(ctx, liveMachine)).To(Succeed(), "live machine should be updated to make the stale copy outdated")

				changed, err := AddPausedAnnotation(ctx, k8sClient, staleCopy)
				Expect(changed).To(BeFalse(), "stale writes should not report a successful change")
				Expect(err).To(HaveOccurred(), "stale writes should fail")
				Expect(apierrors.IsConflict(err)).To(BeTrue(), "expected stale patch to fail with a conflict")
			})
		})
	})

	Describe("RemovePausedAnnotation", func() {
		Context("when the paused annotation is present", func() {
			BeforeEach(func() {
				changed, err := AddPausedAnnotation(ctx, k8sClient, machine)
				Expect(err).NotTo(HaveOccurred())
				Expect(changed).To(BeTrue(), "expected setup to add the paused annotation")
			})

			It("should remove the paused annotation", func() {
				changed, err := RemovePausedAnnotation(ctx, k8sClient, machine)
				Expect(err).NotTo(HaveOccurred())
				Expect(changed).To(BeTrue(), "expected the helper to report that it removed the paused annotation")

				Eventually(k.Object(machine)).ShouldNot(HaveField("Annotations", HaveKey(clusterv1.PausedAnnotation)))
			})

			It("should fail with a conflict when the object has changed since it was read", func() {
				staleCopy := &clusterv1.Machine{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(machine), staleCopy)).To(Succeed(), "stale copy should be fetched")

				liveMachine := &clusterv1.Machine{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(machine), liveMachine)).To(Succeed(), "live machine should be fetched")

				if liveMachine.Annotations == nil {
					liveMachine.Annotations = map[string]string{}
				}

				liveMachine.Annotations["test.openshift.io/stale"] = "true"
				Expect(k8sClient.Update(ctx, liveMachine)).To(Succeed(), "live machine should be updated to make the stale copy outdated")

				changed, err := RemovePausedAnnotation(ctx, k8sClient, staleCopy)
				Expect(changed).To(BeFalse(), "stale writes should not report a successful change")
				Expect(err).To(HaveOccurred(), "stale writes should fail")
				Expect(apierrors.IsConflict(err)).To(BeTrue(), "expected stale patch to fail with a conflict")
			})
		})
	})
})
