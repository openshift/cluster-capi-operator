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

package controllertest

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

// ReconcileFunc executes a single reconcile invocation for test assertions.
type ReconcileFunc func() (ctrl.Result, error)

// SynchronizedCondition returns a Machine API synchronized condition with the
// provided status.
func SynchronizedCondition(status corev1.ConditionStatus) mapiv1beta1.Condition {
	return mapiv1beta1.Condition{
		Type:               consts.SynchronizedCondition,
		Status:             status,
		LastTransitionTime: metav1.Now(),
	}
}

// MAPIPausedCondition returns a Machine API paused condition with the provided
// status.
func MAPIPausedCondition(status corev1.ConditionStatus) mapiv1beta1.Condition {
	return mapiv1beta1.Condition{
		Type:               "Paused",
		Status:             status,
		LastTransitionTime: metav1.Now(),
	}
}

// CAPIPausedCondition returns a Cluster API paused condition with the provided
// status.
func CAPIPausedCondition(status metav1.ConditionStatus) metav1.Condition {
	return metav1.Condition{
		Type:               clusterv1.PausedCondition,
		Status:             status,
		LastTransitionTime: metav1.Now(),
	}
}

// ExpectSuccessfulReconcile asserts that a reconcile call succeeds without
// requeueing.
func ExpectSuccessfulReconcile(reconcile ReconcileFunc) {
	GinkgoHelper()

	result, err := reconcile()
	Expect(err).NotTo(HaveOccurred(), "reconcile should succeed")
	Expect(result).To(Equal(ctrl.Result{}), "reconcile should not requeue")
}

// ExpectSyncStatusReset asserts that migration status has switched authority and
// reset the synchronization bookkeeping.
func ExpectSyncStatusReset(k komega.Komega, obj client.Object, authority mapiv1beta1.MachineAuthority) {
	GinkgoHelper()

	Eventually(k.Object(obj)).Should(SatisfyAll(
		HaveField("Status.AuthoritativeAPI", Equal(authority)),
		HaveField("Status.SynchronizedGeneration", BeZero()),
		HaveField("Status.Conditions", ContainElement(SatisfyAll(
			HaveField("Type", Equal(consts.SynchronizedCondition)),
			HaveField("Status", Equal(corev1.ConditionUnknown)),
			HaveField("Reason", Equal(consts.ReasonAuthoritativeAPIChanged)),
			HaveField("Message", Equal("Waiting for resync after change of AuthoritativeAPI")),
			HaveField("Severity", Equal(mapiv1beta1.ConditionSeverityInfo)),
		))),
	))
}
