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
package operatorstatus

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

func TestClusterOperatorOwnedStateChanged(t *testing.T) {
	const (
		installerGenerator   = ControllerResultGenerator("InstallerController")
		revisionGenerator    = ControllerResultGenerator("RevisionController")
		upgradeableGenerator = ControllerResultGenerator("UpgradeableController")
	)

	installerPredicate := ClusterOperatorOwnedStateChanged(installerGenerator, OwnedRelatedObjects())
	revisionPredicate := ClusterOperatorOwnedStateChanged(revisionGenerator, OwnedVersions())
	upgradeablePredicate := ClusterOperatorOwnedStateChanged(upgradeableGenerator, OwnedConditions(configv1.OperatorUpgradeable))

	condition := func(condType configv1.ClusterStatusConditionType, status configv1.ConditionStatus, reason, message string) configv1.ClusterOperatorStatusCondition {
		return configv1.ClusterOperatorStatusCondition{
			Type:               condType,
			Status:             status,
			Reason:             reason,
			Message:            message,
			LastTransitionTime: metav1.NewTime(time.Now()),
		}
	}

	installerSuccess := func() []configv1.ClusterOperatorStatusCondition {
		return []configv1.ClusterOperatorStatusCondition{
			condition(installerGenerator.SubConditionType(ConditionProgressingSuffix), configv1.ConditionFalse, ReasonAsExpected.String(), "Success"),
			condition(installerGenerator.SubConditionType(ConditionAvailableSuffix), configv1.ConditionTrue, ReasonAsExpected.String(), "Success"),
		}
	}

	clusterOperator := func(name string, status configv1.ClusterOperatorStatus) *configv1.ClusterOperator {
		co := &configv1.ClusterOperator{}
		co.SetName(name)
		co.Status = status

		return co
	}

	t.Run("update events", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			pred     predicate.Funcs
			old, new *configv1.ClusterOperator
			expected bool
		}{
			{
				name: "matches own progressing status change",
				pred: installerPredicate,
				old: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: installerSuccess(),
				}),
				new: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						condition(installerGenerator.SubConditionType(ConditionProgressingSuffix), configv1.ConditionTrue, ReasonEphemeralError.String(), "request timed out"),
						condition(installerGenerator.SubConditionType(ConditionAvailableSuffix), configv1.ConditionTrue, ReasonAsExpected.String(), "Success"),
					},
				}),
				expected: true,
			},
			{
				name: "matches own progressing reason and message change",
				pred: installerPredicate,
				old: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						condition(installerGenerator.SubConditionType(ConditionProgressingSuffix), configv1.ConditionFalse, ReasonAsExpected.String(), "Success"),
					},
				}),
				new: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						condition(installerGenerator.SubConditionType(ConditionProgressingSuffix), configv1.ConditionFalse, ReasonProgressing.String(), "installing"),
					},
				}),
				expected: true,
			},
			{
				name: "matches own available change",
				pred: installerPredicate,
				old: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: installerSuccess(),
				}),
				new: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						condition(installerGenerator.SubConditionType(ConditionProgressingSuffix), configv1.ConditionFalse, ReasonAsExpected.String(), "Success"),
						condition(installerGenerator.SubConditionType(ConditionAvailableSuffix), configv1.ConditionFalse, ReasonNonRetryableError.String(), "terminal error"),
					},
				}),
				expected: true,
			},
			{
				name: "matches own condition added",
				pred: installerPredicate,
				old: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: nil,
				}),
				new: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: installerSuccess(),
				}),
				expected: true,
			},
			{
				name: "matches own condition removed",
				pred: installerPredicate,
				old: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: installerSuccess(),
				}),
				new: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						condition(installerGenerator.SubConditionType(ConditionProgressingSuffix), configv1.ConditionFalse, ReasonAsExpected.String(), "Success"),
					},
				}),
				expected: true,
			},
			{
				name: "ignores own condition lastTransitionTime-only change",
				pred: installerPredicate,
				old: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						condition(installerGenerator.SubConditionType(ConditionProgressingSuffix), configv1.ConditionFalse, ReasonAsExpected.String(), "Success"),
					},
				}),
				new: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:               installerGenerator.SubConditionType(ConditionProgressingSuffix),
							Status:             configv1.ConditionFalse,
							Reason:             ReasonAsExpected.String(),
							Message:            "Success",
							LastTransitionTime: metav1.NewTime(time.Now().Add(time.Hour)),
						},
					},
				}),
				expected: false,
			},
			{
				name: "ignores another controller's sub-condition change",
				pred: installerPredicate,
				old: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: installerSuccess(),
				}),
				new: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: append(installerSuccess(),
						condition(revisionGenerator.SubConditionType(ConditionProgressingSuffix), configv1.ConditionFalse, ReasonAsExpected.String(), "Success"),
					),
				}),
				expected: false,
			},
			{
				name: "ignores top-level aggregated conditions",
				pred: installerPredicate,
				old: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: installerSuccess(),
				}),
				new: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: append(installerSuccess(),
						condition(configv1.OperatorProgressing, configv1.ConditionTrue, ReasonProgressing.String(), "aggregated"),
						condition(configv1.OperatorAvailable, configv1.ConditionFalse, ReasonProgressing.String(), "aggregated"),
						condition(configv1.OperatorDegraded, configv1.ConditionTrue, ReasonEphemeralError.String(), "aggregated"),
					),
				}),
				expected: false,
			},
			{
				name: "ignores top-level conditions on the upgradeable predicate",
				pred: upgradeablePredicate,
				old: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: nil,
				}),
				new: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						condition(configv1.OperatorProgressing, configv1.ConditionTrue, ReasonProgressing.String(), "aggregated"),
						condition(configv1.OperatorDegraded, configv1.ConditionTrue, ReasonEphemeralError.String(), "aggregated"),
					},
				}),
				expected: false,
			},
			{
				name: "matches the bare Upgradeable condition owned by the upgradeable controller",
				pred: upgradeablePredicate,
				old: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						condition(configv1.OperatorUpgradeable, configv1.ConditionTrue, ReasonAsExpected.String(), "compatible"),
					},
				}),
				new: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						condition(configv1.OperatorUpgradeable, configv1.ConditionFalse, ReasonAsExpected.String(), "incompatible"),
					},
				}),
				expected: true,
			},
			{
				name: "ignores the bare Upgradeable condition on the installer predicate",
				pred: installerPredicate,
				old: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: installerSuccess(),
				}),
				new: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: append(installerSuccess(),
						condition(configv1.OperatorUpgradeable, configv1.ConditionFalse, ReasonAsExpected.String(), "incompatible"),
					),
				}),
				expected: false,
			},
			{
				name: "matches own versions change with OwnedVersions",
				pred: revisionPredicate,
				old: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: nil,
					Versions: []configv1.OperandVersion{
						{Name: OperatorVersionKey, Version: "4.18.0"},
					},
				}),
				new: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: nil,
					Versions: []configv1.OperandVersion{
						{Name: OperatorVersionKey, Version: "4.19.0"},
					},
				}),
				expected: true,
			},
			{
				name: "ignores versions change without OwnedVersions",
				pred: installerPredicate,
				old: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: installerSuccess(),
					Versions: []configv1.OperandVersion{
						{Name: OperatorVersionKey, Version: "4.18.0"},
					},
				}),
				new: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions: installerSuccess(),
					Versions: []configv1.OperandVersion{
						{Name: OperatorVersionKey, Version: "4.19.0"},
					},
				}),
				expected: false,
			},
			{
				name: "matches own relatedObjects change with OwnedRelatedObjects",
				pred: installerPredicate,
				old: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions:     installerSuccess(),
					RelatedObjects: []configv1.ObjectReference{{Resource: "namespaces", Name: "a"}},
				}),
				new: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions:     installerSuccess(),
					RelatedObjects: []configv1.ObjectReference{{Resource: "namespaces", Name: "b"}},
				}),
				expected: true,
			},
			{
				name: "ignores relatedObjects change without OwnedRelatedObjects",
				pred: revisionPredicate,
				old: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					RelatedObjects: []configv1.ObjectReference{{Resource: "namespaces", Name: "a"}},
				}),
				new: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					RelatedObjects: []configv1.ObjectReference{{Resource: "namespaces", Name: "b"}},
				}),
				expected: false,
			},
			{
				name: "ignores updates to a different ClusterOperator",
				pred: installerPredicate,
				old: clusterOperator("machine-api", configv1.ClusterOperatorStatus{
					Conditions: installerSuccess(),
				}),
				new: clusterOperator("machine-api", configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						condition(installerGenerator.SubConditionType(ConditionProgressingSuffix), configv1.ConditionTrue, ReasonEphemeralError.String(), "request timed out"),
					},
				}),
				expected: false,
			},
			{
				name: "ignores updates with no owned state change",
				pred: installerPredicate,
				old: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions:     installerSuccess(),
					RelatedObjects: []configv1.ObjectReference{{Resource: "namespaces", Name: "a"}},
				}),
				new: clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
					Conditions:     installerSuccess(),
					RelatedObjects: []configv1.ObjectReference{{Resource: "namespaces", Name: "a"}},
				}),
				expected: false,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				g := NewWithT(t)

				ev := event.UpdateEvent{ObjectOld: tc.old, ObjectNew: tc.new}
				g.Expect(tc.pred.Update(ev)).To(Equal(tc.expected))
			})
		}
	})

	t.Run("create events", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			pred     predicate.Funcs
			object   *configv1.ClusterOperator
			expected bool
		}{
			{
				name:     "matches creation of the cluster-api ClusterOperator",
				pred:     installerPredicate,
				object:   clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{}),
				expected: true,
			},
			{
				name:     "ignores creation of a different ClusterOperator",
				pred:     installerPredicate,
				object:   clusterOperator("machine-api", configv1.ClusterOperatorStatus{}),
				expected: false,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				g := NewWithT(t)

				ev := event.CreateEvent{Object: tc.object}
				g.Expect(tc.pred.Create(ev)).To(Equal(tc.expected))
			})
		}
	})

	t.Run("delete and generic events never match", func(t *testing.T) {
		g := NewWithT(t)

		co := clusterOperator(ClusterOperatorName, configv1.ClusterOperatorStatus{
			Conditions: installerSuccess(),
		})

		for _, pred := range []predicate.Funcs{installerPredicate, revisionPredicate, upgradeablePredicate} {
			g.Expect(pred.Delete(event.DeleteEvent{Object: co})).To(BeFalse())
			g.Expect(pred.Generic(event.GenericEvent{Object: co})).To(BeFalse())
		}
	})
}
