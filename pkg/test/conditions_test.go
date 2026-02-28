/*
Copyright 2025 Red Hat, Inc.

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

package test

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	configv1apply "github.com/openshift/client-go/config/applyconfigurations/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHaveCondition_FindsByType(t *testing.T) {
	g := NewWithT(t)

	conditions := []configv1.ClusterOperatorStatusCondition{
		{
			Type:   "Progressing",
			Status: configv1.ConditionTrue,
			Reason: "Working",
		},
		{
			Type:   "Degraded",
			Status: configv1.ConditionFalse,
			Reason: "AllGood",
		},
	}

	g.Expect(conditions).To(HaveCondition("Progressing"))
	g.Expect(conditions).To(HaveCondition("Degraded"))
	g.Expect(conditions).ToNot(HaveCondition("Available"))
}

func TestHaveCondition_WithStatus(t *testing.T) {
	g := NewWithT(t)

	conditions := []configv1.ClusterOperatorStatusCondition{
		{
			Type:   "Progressing",
			Status: configv1.ConditionTrue,
			Reason: "Working",
		},
	}

	g.Expect(conditions).To(HaveCondition("Progressing").WithStatus(configv1.ConditionTrue))
	g.Expect(conditions).ToNot(HaveCondition("Progressing").WithStatus(configv1.ConditionFalse))
}

func TestHaveCondition_WithReason(t *testing.T) {
	g := NewWithT(t)

	conditions := []configv1.ClusterOperatorStatusCondition{
		{
			Type:   "Progressing",
			Status: configv1.ConditionTrue,
			Reason: "EphemeralError",
		},
	}

	g.Expect(conditions).To(HaveCondition("Progressing").WithReason("EphemeralError"))
	g.Expect(conditions).ToNot(HaveCondition("Progressing").WithReason("Success"))
}

func TestHaveCondition_WithMessage(t *testing.T) {
	g := NewWithT(t)

	conditions := []configv1.ClusterOperatorStatusCondition{
		{
			Type:    "Progressing",
			Status:  configv1.ConditionTrue,
			Reason:  "Working",
			Message: "Processing revision 1",
		},
	}

	g.Expect(conditions).To(HaveCondition("Progressing").WithMessage("Processing revision 1"))
	g.Expect(conditions).To(HaveCondition("Progressing").WithMessage(ContainSubstring("revision")))
	g.Expect(conditions).ToNot(HaveCondition("Progressing").WithMessage("Something else"))
}

func TestHaveCondition_WithLastTransitionTime(t *testing.T) {
	g := NewWithT(t)

	now := metav1.Now()
	conditions := []configv1.ClusterOperatorStatusCondition{
		{
			Type:               "Progressing",
			Status:             configv1.ConditionTrue,
			Reason:             "Working",
			LastTransitionTime: now,
		},
	}

	// Exact match using Equal
	g.Expect(conditions).To(HaveCondition("Progressing").WithLastTransitionTime(Equal(now)))

	// For BeTemporally, we need to extract the Time field - use a custom transform
	g.Expect(conditions).To(HaveCondition("Progressing").
		WithLastTransitionTime(WithTransform(func(t metav1.Time) time.Time { return t.Time }, BeTemporally("~", now.Time, time.Second))))
}

func TestHaveCondition_Chained(t *testing.T) {
	g := NewWithT(t)

	conditions := []configv1.ClusterOperatorStatusCondition{
		{
			Type:    "Progressing",
			Status:  configv1.ConditionTrue,
			Reason:  "EphemeralError",
			Message: "connection refused",
		},
		{
			Type:   "Degraded",
			Status: configv1.ConditionFalse,
			Reason: "AllGood",
		},
	}

	// All fields match
	g.Expect(conditions).To(HaveCondition("Progressing").
		WithStatus(configv1.ConditionTrue).
		WithReason("EphemeralError").
		WithMessage(ContainSubstring("connection")))

	// Status mismatch
	g.Expect(conditions).ToNot(HaveCondition("Progressing").
		WithStatus(configv1.ConditionFalse).
		WithReason("EphemeralError"))

	// Reason mismatch
	g.Expect(conditions).ToNot(HaveCondition("Progressing").
		WithStatus(configv1.ConditionTrue).
		WithReason("Success"))
}

func TestHaveCondition_FailureMessages(t *testing.T) {
	g := NewWithT(t)

	conditions := []configv1.ClusterOperatorStatusCondition{
		{
			Type:   "Progressing",
			Status: configv1.ConditionTrue,
			Reason: "Working",
		},
	}

	// Type not found
	matcher := HaveCondition("NotFound")
	success, err := matcher.Match(conditions)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(success).To(BeFalse())
	g.Expect(matcher.FailureMessage(conditions)).To(ContainSubstring("NotFound"))
	g.Expect(matcher.FailureMessage(conditions)).To(ContainSubstring("not found"))

	// Status mismatch
	matcher = HaveCondition("Progressing").WithStatus(configv1.ConditionFalse)
	success, err = matcher.Match(conditions)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(success).To(BeFalse())
	g.Expect(matcher.FailureMessage(conditions)).To(ContainSubstring("Status"))
}

func TestHaveCondition_WrongType(t *testing.T) {
	g := NewWithT(t)

	matcher := HaveCondition("Progressing")
	_, err := matcher.Match("not a slice")
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("expects"))
}

// Tests for metav1.Condition to verify reflection-based matching works with different types.
func TestHaveCondition_Metav1Condition(t *testing.T) {
	g := NewWithT(t)

	conditions := []metav1.Condition{
		{
			Type:    "Ready",
			Status:  metav1.ConditionTrue,
			Reason:  "AllReady",
			Message: "All components are ready",
		},
		{
			Type:    "Progressing",
			Status:  metav1.ConditionFalse,
			Reason:  "Stable",
			Message: "No changes in progress",
		},
	}

	// Find by type
	g.Expect(conditions).To(HaveCondition("Ready"))
	g.Expect(conditions).To(HaveCondition("Progressing"))
	g.Expect(conditions).ToNot(HaveCondition("Degraded"))

	// With status - note: metav1.ConditionStatus is a string type
	g.Expect(conditions).To(HaveCondition("Ready").WithStatus(metav1.ConditionTrue))
	g.Expect(conditions).ToNot(HaveCondition("Ready").WithStatus(metav1.ConditionFalse))

	// With reason
	g.Expect(conditions).To(HaveCondition("Ready").WithReason("AllReady"))

	// With message
	g.Expect(conditions).To(HaveCondition("Ready").WithMessage(ContainSubstring("ready")))

	// Chained matchers
	g.Expect(conditions).To(HaveCondition("Ready").
		WithStatus(metav1.ConditionTrue).
		WithReason("AllReady").
		WithMessage("All components are ready"))
}

func TestHaveCondition_Metav1Condition_WithLastTransitionTime(t *testing.T) {
	g := NewWithT(t)

	now := metav1.Now()
	conditions := []metav1.Condition{
		{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "AllReady",
			Message:            "All components are ready",
			LastTransitionTime: now,
		},
	}

	// Exact match using Equal
	g.Expect(conditions).To(HaveCondition("Ready").WithLastTransitionTime(Equal(now)))

	// With transform for BeTemporally
	g.Expect(conditions).To(HaveCondition("Ready").
		WithLastTransitionTime(WithTransform(func(t metav1.Time) time.Time { return t.Time }, BeTemporally("~", now.Time, time.Second))))
}

// Tests for ClusterOperatorStatusConditionApplyConfiguration to verify matching
// works with pointer slice elements and pointer struct fields.
func TestHaveCondition_ApplyConfiguration(t *testing.T) {
	g := NewWithT(t)

	now := metav1.Now()
	conditions := []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{
		configv1apply.ClusterOperatorStatusCondition().
			WithType("Progressing").
			WithStatus(configv1.ConditionTrue).
			WithReason("EphemeralError").
			WithMessage("connection refused").
			WithLastTransitionTime(now),
		configv1apply.ClusterOperatorStatusCondition().
			WithType("Degraded").
			WithStatus(configv1.ConditionFalse).
			WithReason("AsExpected").
			WithMessage("Success"),
	}

	// Find by type
	g.Expect(conditions).To(HaveCondition("Progressing"))
	g.Expect(conditions).To(HaveCondition("Degraded"))
	g.Expect(conditions).ToNot(HaveCondition("Available"))

	// With status
	g.Expect(conditions).To(HaveCondition("Progressing").WithStatus(configv1.ConditionTrue))
	g.Expect(conditions).ToNot(HaveCondition("Progressing").WithStatus(configv1.ConditionFalse))

	// With reason
	g.Expect(conditions).To(HaveCondition("Progressing").WithReason("EphemeralError"))

	// With message
	g.Expect(conditions).To(HaveCondition("Progressing").WithMessage(ContainSubstring("connection")))

	// With LastTransitionTime
	g.Expect(conditions).To(HaveCondition("Progressing").WithLastTransitionTime(Equal(now)))

	// Chained matchers
	g.Expect(conditions).To(HaveCondition("Progressing").
		WithStatus(configv1.ConditionTrue).
		WithReason("EphemeralError").
		WithMessage("connection refused"))

	g.Expect(conditions).To(HaveCondition("Degraded").
		WithStatus(configv1.ConditionFalse).
		WithReason("AsExpected").
		WithMessage("Success"))
}

func TestBeCondition_SingleElement(t *testing.T) {
	g := NewWithT(t)

	cond := configv1.ClusterOperatorStatusCondition{
		Type:    "Progressing",
		Status:  configv1.ConditionTrue,
		Reason:  "EphemeralError",
		Message: "connection refused",
	}

	// Match by type
	g.Expect(cond).To(BeCondition("Progressing"))
	g.Expect(cond).ToNot(BeCondition("Degraded"))

	// With status
	g.Expect(cond).To(BeCondition("Progressing").WithStatus(configv1.ConditionTrue))
	g.Expect(cond).ToNot(BeCondition("Progressing").WithStatus(configv1.ConditionFalse))

	// With reason
	g.Expect(cond).To(BeCondition("Progressing").WithReason("EphemeralError"))
	g.Expect(cond).ToNot(BeCondition("Progressing").WithReason("AsExpected"))

	// With message
	g.Expect(cond).To(BeCondition("Progressing").WithMessage("connection refused"))
	g.Expect(cond).To(BeCondition("Progressing").WithMessage(ContainSubstring("connection")))

	// Chained
	g.Expect(cond).To(BeCondition("Progressing").
		WithStatus(configv1.ConditionTrue).
		WithReason("EphemeralError").
		WithMessage("connection refused"))
}

func TestBeCondition_ApplyConfiguration(t *testing.T) {
	g := NewWithT(t)

	now := metav1.Now()
	cond := configv1apply.ClusterOperatorStatusCondition().
		WithType("Degraded").
		WithStatus(configv1.ConditionFalse).
		WithReason("AsExpected").
		WithMessage("Success").
		WithLastTransitionTime(now)

	g.Expect(cond).To(BeCondition("Degraded").
		WithStatus(configv1.ConditionFalse).
		WithReason("AsExpected").
		WithMessage("Success").
		WithLastTransitionTime(Equal(now)))

	g.Expect(cond).ToNot(BeCondition("Progressing"))
}

func TestBeCondition_FailureMessages(t *testing.T) {
	g := NewWithT(t)

	cond := configv1.ClusterOperatorStatusCondition{
		Type:   "Progressing",
		Status: configv1.ConditionTrue,
		Reason: "Working",
	}

	// Type mismatch
	matcher := BeCondition("Degraded")
	success, err := matcher.Match(cond)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(success).To(BeFalse())
	g.Expect(matcher.FailureMessage(cond)).To(ContainSubstring("Expected condition to have Type"))
	g.Expect(matcher.FailureMessage(cond)).To(ContainSubstring("Degraded"))

	// Status mismatch
	matcher = BeCondition("Progressing").WithStatus(configv1.ConditionFalse)
	success, err = matcher.Match(cond)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(success).To(BeFalse())
	g.Expect(matcher.FailureMessage(cond)).To(ContainSubstring("Status"))
}

func TestBeCondition_WrongType(t *testing.T) {
	g := NewWithT(t)

	matcher := BeCondition("Progressing")
	_, err := matcher.Match("not a struct")
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("BeCondition expects"))
}
