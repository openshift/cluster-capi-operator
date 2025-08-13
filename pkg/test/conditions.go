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
	g "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func WithConditionReason(conditionReason string) types.GomegaMatcher {
	return g.HaveField("Reason", g.Equal(conditionReason))
}

func WithConditionMessage(conditionMessage string) types.GomegaMatcher {
	return g.HaveField("Message", g.Equal(conditionMessage))
}

func WithConditionObservedGeneration(observedGeneration int64) types.GomegaMatcher {
	return g.HaveField("ObservedGeneration", g.Equal(observedGeneration))
}

func HaveCondition(conditionType string, conditionStatus metav1.ConditionStatus, conditionMatcher ...types.GomegaMatcher) types.GomegaMatcher {
	conditionMatchers := []types.GomegaMatcher{
		g.HaveField("Type", g.Equal(conditionType)),
		g.HaveField("Status", g.Equal(conditionStatus)),
	}
	conditionMatchers = append(conditionMatchers, conditionMatcher...)
	return g.HaveField("Status.Conditions", g.ContainElement(g.SatisfyAll(conditionMatchers...)))
}
