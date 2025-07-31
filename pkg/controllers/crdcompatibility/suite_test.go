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

package crdcompatibility

import (
	"fmt"
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var (
	testEnv *envtest.Environment
	cfg     *rest.Config
	cl      client.Client
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CRDCompatibility Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(klog.Background())

	By("bootstrapping test environment")
	var err error
	testEnv = &envtest.Environment{}
	cfg, cl, err = test.StartEnvTest(testEnv)
	DeferCleanup(func() {
		By("tearing down the test environment")
		Expect(test.StopEnvTest(testEnv)).To(Succeed())
	})

	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())
	Expect(cl).NotTo(BeNil())

	komega.SetClient(cl)
})

type haveConditionMatcher struct {
	conditionType    string
	conditionStatus  metav1.ConditionStatus
	conditionReason  string
	conditionMessage string
}

func HaveCondition(conditionType string, conditionStatus metav1.ConditionStatus, conditionReason string, conditionMessage string) types.GomegaMatcher {
	return &haveConditionMatcher{
		conditionType:    conditionType,
		conditionStatus:  conditionStatus,
		conditionReason:  conditionReason,
		conditionMessage: conditionMessage,
	}
}

func (m haveConditionMatcher) Match(actual interface{}) (success bool, err error) {
	condition, ok := actual.(metav1.Condition)
	if !ok {
		return false, fmt.Errorf("value is not a metav1.Condition")
	}

	if condition.Type != m.conditionType {
		return false, fmt.Errorf("condition type is not %s", m.conditionType)
	}

	if condition.Status != m.conditionStatus {
		return false, fmt.Errorf("condition status is not %s", m.conditionStatus)
	}

	if condition.Reason != m.conditionReason {
		return false, fmt.Errorf("condition reason is not %s", m.conditionReason)
	}

	if condition.Message != m.conditionMessage {
		return false, fmt.Errorf("condition message is not %s", m.conditionMessage)
	}

	return true, nil
}

func (m haveConditionMatcher) FailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("Expected condition to have type=%s, status=%s, reason=%s, message=%s",
		m.conditionType, m.conditionStatus, m.conditionReason, m.conditionMessage)
}

func (m haveConditionMatcher) NegatedFailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("Expected condition to not have type=%s, status=%s, reason=%s, message=%s",
		m.conditionType, m.conditionStatus, m.conditionReason, m.conditionMessage)
}

// extractConditions uses reflection to safely extract the Conditions field from a client.Object.
// It returns an error if the object doesn't have the expected structure.
func extractConditions(obj client.Object) ([]metav1.Condition, error) {
	if obj == nil {
		return nil, fmt.Errorf("object is nil")
	}

	// Get the reflect.Value of the object
	objValue := reflect.ValueOf(obj)
	if objValue.Kind() == reflect.Ptr {
		objValue = objValue.Elem()
	}

	// Check if the object has a Status field
	statusField := objValue.FieldByName("Status")
	if !statusField.IsValid() {
		return nil, fmt.Errorf("object does not have a Status field")
	}

	// Check if Status is a struct
	if statusField.Kind() != reflect.Struct {
		return nil, fmt.Errorf("Status field is not a struct, got %v", statusField.Kind())
	}

	// Check if Status has a Conditions field
	conditionsField := statusField.FieldByName("Conditions")
	if !conditionsField.IsValid() {
		return nil, fmt.Errorf("Status does not have a Conditions field")
	}

	// Check if Conditions is a slice
	if conditionsField.Kind() != reflect.Slice {
		return nil, fmt.Errorf("Conditions field is not a slice, got %v", conditionsField.Kind())
	}

	// Convert the reflect.Value to []metav1.Condition
	conditions := conditionsField.Interface().([]metav1.Condition)
	return conditions, nil
}
