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
package machinesetsync

import (
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	capiv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta2"
	capav1builder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	controllers "github.com/openshift/cluster-capi-operator/pkg/controllers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type stubDiffResult struct {
	metadata     bool
	spec         bool
	providerSpec bool
	status       bool
}

func (d stubDiffResult) HasChanges() bool {
	return d.metadata || d.spec || d.providerSpec || d.status
}

func (d stubDiffResult) String() string {
	return "stub diff"
}

func (d stubDiffResult) HasMetadataChanges() bool {
	return d.metadata
}

func (d stubDiffResult) HasSpecChanges() bool {
	return d.spec
}

func (d stubDiffResult) HasProviderSpecChanges() bool {
	return d.providerSpec
}

func (d stubDiffResult) HasStatusChanges() bool {
	return d.status
}

func newMachineSetSyncUnitReconciler(objs []client.Object) *MachineSetSyncReconciler {
	builder := fake.NewClientBuilder().
		WithScheme(testEnv.Scheme).
		WithStatusSubresource(
			&mapiv1beta1.MachineSet{},
			&clusterv1.MachineSet{},
		)

	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}

	return &MachineSetSyncReconciler{
		Client:        builder.Build(),
		Scheme:        testEnv.Scheme,
		Platform:      configv1.AWSPlatformType,
		MAPINamespace: "openshift-machine-api",
		CAPINamespace: "openshift-cluster-api",
	}
}

func expectFieldError(err error, expectedType field.ErrorType, expectedField, expectedDetail string) {
	GinkgoHelper()

	var fieldErr *field.Error
	Expect(errors.As(err, &fieldErr)).To(BeTrue(), "expected a Kubernetes field error")
	Expect(fieldErr).To(SatisfyAll(
		HaveField("Type", Equal(expectedType)),
		HaveField("Field", Equal(expectedField)),
	))

	if expectedDetail != "" {
		Expect(fieldErr).To(HaveField("Detail", Equal(expectedDetail)))
	}
}

var _ = Describe("Unit tests for CAPIMachineSetStatusEqual", func() {
	now := metav1.Now()
	later := metav1.NewTime(now.Add(1 * time.Hour))

	type testInput struct {
		a           clusterv1.MachineSetStatus
		b           clusterv1.MachineSetStatus
		want        string
		wantChanges bool
	}

	DescribeTable("when comparing Cluster API MachineSets",
		func(tt testInput) {
			a := &clusterv1.MachineSet{
				Status: tt.a,
			}
			b := &clusterv1.MachineSet{
				Status: tt.b,
			}

			got, err := compareCAPIMachineSets(a, b)
			Expect(err).ToNot(HaveOccurred())

			Expect(got.HasChanges()).To(Equal(tt.wantChanges))

			if tt.wantChanges {
				Expect(got.String()).To(BeEquivalentTo(tt.want))
			}
		},
		Entry("no diff", testInput{
			a:           clusterv1.MachineSetStatus{},
			b:           clusterv1.MachineSetStatus{},
			want:        "",
			wantChanges: false,
		}),
		Entry("diff in ReadyReplicas", testInput{
			a: clusterv1.MachineSetStatus{
				ReadyReplicas: ptr.To[int32](3),
			},
			b: clusterv1.MachineSetStatus{
				ReadyReplicas: ptr.To[int32](5),
			},
			want:        ".[status].[readyReplicas]: 3 != 5",
			wantChanges: true,
		}),
		Entry("diff in AvailableReplicas", testInput{
			a: clusterv1.MachineSetStatus{
				AvailableReplicas: ptr.To[int32](2),
			},
			b: clusterv1.MachineSetStatus{
				AvailableReplicas: ptr.To[int32](4),
			},
			want:        ".[status].[availableReplicas]: 2 != 4",
			wantChanges: true,
		}),
		Entry("diff in ReadyReplicas and AvailableReplicas", testInput{
			a: clusterv1.MachineSetStatus{
				ReadyReplicas:     ptr.To[int32](3),
				AvailableReplicas: ptr.To[int32](2),
			},
			b: clusterv1.MachineSetStatus{
				ReadyReplicas:     ptr.To[int32](5),
				AvailableReplicas: ptr.To[int32](4),
			},
			want:        ".[status].[availableReplicas]: 2 != 4, .[status].[readyReplicas]: 3 != 5",
			wantChanges: true,
		}),
		Entry("same v1beta1 conditions", testInput{
			a: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
						Conditions: []clusterv1.Condition{
							{
								Type:               clusterv1.ReadyCondition,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: now,
							},
						},
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
						Conditions: []clusterv1.Condition{
							{
								Type:               clusterv1.ReadyCondition,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: now,
							},
						},
					},
				},
			},
			want:        "",
			wantChanges: false,
		}),
		Entry("changed v1beta1 condition Status", testInput{
			a: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
						Conditions: []clusterv1.Condition{
							{
								Type:               clusterv1.ReadyCondition,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: now,
							},
						},
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
						Conditions: []clusterv1.Condition{
							{
								Type:               clusterv1.ReadyCondition,
								Status:             corev1.ConditionFalse,
								LastTransitionTime: now,
							},
						},
					},
				},
			},
			want:        ".[status].[deprecated].[v1beta1].[conditions].[type=Ready].[status]: True != False",
			wantChanges: true,
		}),
		Entry("v1beta1 condition LastTransitionTime ignored", testInput{
			a: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
						Conditions: []clusterv1.Condition{
							{
								Type:               clusterv1.ReadyCondition,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: now,
							},
						},
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
						Conditions: []clusterv1.Condition{
							{
								Type:               clusterv1.ReadyCondition,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: later,
							},
						},
					},
				},
			},
			want:        "",
			wantChanges: false,
		}),
		Entry("multiple v1beta1 conditions with one changed", testInput{
			a: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
						Conditions: []clusterv1.Condition{
							{
								Type:               clusterv1.ReadyCondition,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: now,
							},
							{
								Type:               clusterv1.MachinesReadyCondition,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: now,
							},
						},
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
						Conditions: []clusterv1.Condition{
							{
								Type:               clusterv1.ReadyCondition,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: now,
							},
							{
								Type:               clusterv1.MachinesReadyCondition,
								Status:             corev1.ConditionFalse,
								LastTransitionTime: now,
							},
						},
					},
				},
			},
			want:        ".[status].[deprecated].[v1beta1].[conditions].[type=MachinesReady].[status]: True != False",
			wantChanges: true,
		}),
		Entry("same v1beta2 conditions", testInput{
			a: clusterv1.MachineSetStatus{
				Conditions: []metav1.Condition{
					{
						Type:               "Ready",
						Status:             metav1.ConditionTrue,
						LastTransitionTime: now,
						Reason:             "AllReady",
						Message:            "All machines are ready",
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				Conditions: []metav1.Condition{
					{
						Type:               "Ready",
						Status:             metav1.ConditionTrue,
						LastTransitionTime: now,
						Reason:             "AllReady",
						Message:            "All machines are ready",
					},
				},
			},
			want:        "",
			wantChanges: false,
		}),
		Entry("changed v1beta2 condition Status", testInput{
			a: clusterv1.MachineSetStatus{
				Conditions: []metav1.Condition{
					{
						Type:               "Ready",
						Status:             metav1.ConditionTrue,
						LastTransitionTime: now,
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				Conditions: []metav1.Condition{
					{
						Type:               "Ready",
						Status:             metav1.ConditionFalse,
						LastTransitionTime: now,
					},
				},
			},
			want:        ".[status].[conditions].[type=Ready].[status]: True != False",
			wantChanges: true,
		}),
		Entry("v1beta2 condition LastTransitionTime ignored", testInput{
			a: clusterv1.MachineSetStatus{
				Conditions: []metav1.Condition{
					{
						Type:               "Ready",
						Status:             metav1.ConditionTrue,
						LastTransitionTime: now,
						Reason:             "AllReady",
						Message:            "All machines are ready",
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				Conditions: []metav1.Condition{
					{
						Type:               "Ready",
						Status:             metav1.ConditionTrue,
						LastTransitionTime: later,
						Reason:             "AllReady",
						Message:            "All machines are ready",
					},
				},
			},
			want:        "",
			wantChanges: false,
		}),
		Entry("multiple v1beta2 conditions with one changed", testInput{
			a: clusterv1.MachineSetStatus{
				Conditions: []metav1.Condition{
					{
						Type:               "Ready",
						Status:             metav1.ConditionTrue,
						LastTransitionTime: now,
					},
					{
						Type:               "Available",
						Status:             metav1.ConditionTrue,
						LastTransitionTime: now,
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				Conditions: []metav1.Condition{
					{
						Type:               "Ready",
						Status:             metav1.ConditionTrue,
						LastTransitionTime: now,
					},
					{
						Type:               "Available",
						Status:             metav1.ConditionFalse,
						LastTransitionTime: now,
					},
				},
			},
			want:        ".[status].[conditions].[type=Available].[status]: True != False",
			wantChanges: true,
		}),
		Entry("Deprecated.v1beta1 nil vs non-nil", testInput{
			a: clusterv1.MachineSetStatus{
				Deprecated: nil,
				Replicas:   ptr.To[int32](3),
			},
			b: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
						ReadyReplicas: 3,
					},
				},
				Replicas: ptr.To[int32](3),
			},
			want:        ".[status].[deprecated]: <does not have key> != [v1beta1:[availableReplicas:0 fullyLabeledReplicas:0 readyReplicas:3]]",
			wantChanges: true,
		}),
	)
})

var _ = Describe("Unit tests for MachineSetSync owner reference validation", func() {
	var reconciler *MachineSetSyncReconciler

	BeforeEach(func() {
		reconciler = newMachineSetSyncUnitReconciler(nil)
	})

	Context("when validating a Machine API machine set", func() {
		It("should allow machine sets without owner references", func() {
			mapiMachineSet := machinev1resourcebuilder.MachineSet().
				WithNamespace("openshift-machine-api").
				WithName("foo").
				Build()

			Expect(reconciler.validateMAPIMachineSetOwnerReferences(mapiMachineSet)).To(Succeed())
		})

		It("should reject machine sets with owner references", func() {
			mapiMachineSet := machinev1resourcebuilder.MachineSet().
				WithNamespace("openshift-machine-api").
				WithName("foo").
				Build()
			mapiMachineSet.OwnerReferences = []metav1.OwnerReference{{
				APIVersion: "healthchecking.openshift.io/v1beta1",
				Kind:       "MachineHealthCheck",
				Name:       "foo",
			}}

			err := reconciler.validateMAPIMachineSetOwnerReferences(mapiMachineSet)
			expectFieldError(err, field.ErrorTypeInvalid, "metadata.ownerReferences", errMachineAPIMachineSetOwnerReferenceConversionUnsupported.Error())
		})
	})

	Context("when validating a Cluster API machine set", func() {
		clusterOwnerReference := metav1.OwnerReference{
			APIVersion: clusterv1.GroupVersion.String(),
			Kind:       clusterv1.ClusterKind,
			Name:       "cluster",
		}

		It("should allow machine sets without owner references", func() {
			capiMachineSet := capiv1resourcebuilder.MachineSet().
				WithNamespace("openshift-cluster-api").
				WithName("foo").
				Build()

			Expect(reconciler.validateCAPIMachineSetOwnerReferences(capiMachineSet)).To(Succeed())
		})

		It("should allow a single Cluster owner reference", func() {
			capiMachineSet := capiv1resourcebuilder.MachineSet().
				WithNamespace("openshift-cluster-api").
				WithName("foo").
				WithOwnerReferences([]metav1.OwnerReference{clusterOwnerReference}).
				Build()

			Expect(reconciler.validateCAPIMachineSetOwnerReferences(capiMachineSet)).To(Succeed())
		})

		It("should reject non-Cluster owner references", func() {
			capiMachineSet := capiv1resourcebuilder.MachineSet().
				WithNamespace("openshift-cluster-api").
				WithName("foo").
				WithOwnerReferences([]metav1.OwnerReference{{
					APIVersion: clusterv1.GroupVersion.String(),
					Kind:       "MachineDeployment",
					Name:       "foo",
				}}).
				Build()

			err := reconciler.validateCAPIMachineSetOwnerReferences(capiMachineSet)
			expectFieldError(err, field.ErrorTypeInvalid, "metadata.ownerReferences", errUnsuportedOwnerKindForConversion.Error())
		})

		It("should reject more than one owner reference", func() {
			capiMachineSet := capiv1resourcebuilder.MachineSet().
				WithNamespace("openshift-cluster-api").
				WithName("foo").
				WithOwnerReferences([]metav1.OwnerReference{
					clusterOwnerReference,
					{
						APIVersion: clusterv1.GroupVersion.String(),
						Kind:       clusterv1.ClusterKind,
						Name:       "another-cluster",
					},
				}).
				Build()

			err := reconciler.validateCAPIMachineSetOwnerReferences(capiMachineSet)
			expectFieldError(err, field.ErrorTypeTooMany, "metadata.ownerReferences", "")
		})
	})
})

var _ = Describe("Unit tests for infrastructure machine template cleanup helpers", func() {
	DescribeTable("should filter outdated infrastructure machine templates for supported providers",
		func(list client.ObjectList, currentTemplateName string, expectedNames []string) {
			outdatedTemplates, err := filterOutdatedInfraMachineTemplates(list, currentTemplateName)
			Expect(err).NotTo(HaveOccurred())

			outdatedTemplateNames := make([]string, 0, len(outdatedTemplates))
			for _, template := range outdatedTemplates {
				outdatedTemplateNames = append(outdatedTemplateNames, template.GetName())
			}

			Expect(outdatedTemplateNames).To(ConsistOf(expectedNames))
		},
		Entry("AWS machine templates",
			&awsv1.AWSMachineTemplateList{
				Items: []awsv1.AWSMachineTemplate{
					{ObjectMeta: metav1.ObjectMeta{Name: "current"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "old-1"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "old-2"}},
				},
			},
			"current",
			[]string{"old-1", "old-2"},
		),
		Entry("PowerVS machine templates",
			&ibmpowervsv1.IBMPowerVSMachineTemplateList{
				Items: []ibmpowervsv1.IBMPowerVSMachineTemplate{
					{ObjectMeta: metav1.ObjectMeta{Name: "current"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "old"}},
				},
			},
			"current",
			[]string{"old"},
		),
		Entry("OpenStack machine templates",
			&openstackv1.OpenStackMachineTemplateList{
				Items: []openstackv1.OpenStackMachineTemplate{
					{ObjectMeta: metav1.ObjectMeta{Name: "current"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "old"}},
				},
			},
			"current",
			[]string{"old"},
		),
	)

	It("should reject unexpected infrastructure machine template lists", func() {
		outdatedTemplates, err := filterOutdatedInfraMachineTemplates(&corev1.PodList{}, "current")
		Expect(outdatedTemplates).To(BeNil(), "expected no templates to be returned for unsupported list types")
		Expect(err).To(MatchError(ContainSubstring(errUnexpectedInfraMachineTemplateListType.Error())))
	})

	It("should separate active and deleting outdated templates", func() {
		now := metav1.Now()
		templatesToDelete, deletingTemplates := categorizeInfraMachineTemplates([]client.Object{
			&awsv1.AWSMachineTemplate{ObjectMeta: metav1.ObjectMeta{Name: "old-active"}},
			&awsv1.AWSMachineTemplate{ObjectMeta: metav1.ObjectMeta{
				Name:              "old-deleting",
				DeletionTimestamp: &now,
			}},
		})

		Expect(templatesToDelete).To(ConsistOf(HaveField("Name", Equal("old-active"))))
		Expect(deletingTemplates).To(ConsistOf(HaveField("Name", Equal("old-deleting"))))
	})
})

var _ = Describe("Unit tests for deleteOutdatedCAPIInfraMachineTemplates", func() {
	var mapiMachineSet *mapiv1beta1.MachineSet

	BeforeEach(func() {
		mapiMachineSet = machinev1resourcebuilder.MachineSet().
			WithNamespace("openshift-machine-api").
			WithName("foo").
			Build()
	})

	It("should do nothing when there are no labeled outdated templates", func() {
		currentTemplate := capav1builder.AWSMachineTemplate().
			WithNamespace("openshift-cluster-api").
			WithName("current").
			Build()
		currentTemplate.Labels = map[string]string{controllers.MachineSetOpenshiftLabelKey: mapiMachineSet.Name}

		unlabelledOldTemplate := capav1builder.AWSMachineTemplate().
			WithNamespace("openshift-cluster-api").
			WithName("old-unlabelled").
			Build()

		reconciler := newMachineSetSyncUnitReconciler([]client.Object{currentTemplate, unlabelledOldTemplate})

		shouldRequeue, err := reconciler.deleteOutdatedCAPIInfraMachineTemplates(ctx, mapiMachineSet, currentTemplate.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse(), "expected no requeue when only the current labeled template exists")
	})

	It("should requeue while labeled outdated templates are already deleting", func() {
		now := metav1.Now()
		currentTemplate := capav1builder.AWSMachineTemplate().
			WithNamespace("openshift-cluster-api").
			WithName("current").
			Build()
		currentTemplate.Labels = map[string]string{controllers.MachineSetOpenshiftLabelKey: mapiMachineSet.Name}

		deletingTemplate := capav1builder.AWSMachineTemplate().
			WithNamespace("openshift-cluster-api").
			WithName("old-deleting").
			Build()
		deletingTemplate.Labels = map[string]string{controllers.MachineSetOpenshiftLabelKey: mapiMachineSet.Name}
		deletingTemplate.Finalizers = []string{"example.com/finalizer"}
		deletingTemplate.DeletionTimestamp = &now

		reconciler := newMachineSetSyncUnitReconciler([]client.Object{currentTemplate, deletingTemplate})

		shouldRequeue, err := reconciler.deleteOutdatedCAPIInfraMachineTemplates(ctx, mapiMachineSet, currentTemplate.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeTrue(), "expected a requeue while outdated templates are already deleting")

		storedDeletingTemplate := &awsv1.AWSMachineTemplate{}
		Expect(reconciler.Client.Get(ctx, client.ObjectKeyFromObject(deletingTemplate), storedDeletingTemplate)).To(Succeed())
		Expect(storedDeletingTemplate.GetDeletionTimestamp()).NotTo(BeNil(), "expected the deleting template to remain in deleting state")
	})
})

var _ = Describe("Unit tests for MachineSet status generation gating", func() {
	It("should wait to update Cluster API status until the Machine API observed generation catches up", func() {
		mapiMachineSet := machinev1resourcebuilder.MachineSet().
			WithNamespace("openshift-machine-api").
			WithName("foo").
			Build()
		mapiMachineSet.Generation = 2
		mapiMachineSet.Status.ObservedGeneration = 1

		existingCAPIMachineSet := capiv1resourcebuilder.MachineSet().
			WithNamespace("openshift-cluster-api").
			WithName("foo").
			Build()
		existingCAPIMachineSet.Status.Replicas = ptr.To[int32](1)

		convertedCAPIMachineSet := existingCAPIMachineSet.DeepCopy()
		updatedOrCreatedCAPIMachineSet := existingCAPIMachineSet.DeepCopy()
		updatedOrCreatedCAPIMachineSet.Generation = 3

		reconciler := newMachineSetSyncUnitReconciler([]client.Object{existingCAPIMachineSet.DeepCopy()})

		updated, err := reconciler.ensureCAPIMachineSetStatusUpdated(
			ctx,
			mapiMachineSet,
			existingCAPIMachineSet,
			convertedCAPIMachineSet,
			updatedOrCreatedCAPIMachineSet,
			stubDiffResult{},
			true,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(updated).To(BeFalse(), "expected status updates to wait for the Machine API observed generation")

		storedCAPIMachineSet := &clusterv1.MachineSet{}
		Expect(reconciler.Client.Get(ctx, client.ObjectKeyFromObject(existingCAPIMachineSet), storedCAPIMachineSet)).To(Succeed())
		Expect(storedCAPIMachineSet.Status.Replicas).To(HaveValue(BeEquivalentTo(1)), "expected the stored Cluster API status to remain unchanged")
		Expect(storedCAPIMachineSet.Status.ObservedGeneration).To(BeZero(), "expected the stored Cluster API observed generation to remain unchanged")
	})

	It("should wait to update Machine API status until the Cluster API observed generation catches up", func() {
		existingMAPIMachineSet := machinev1resourcebuilder.MachineSet().
			WithNamespace("openshift-machine-api").
			WithName("foo").
			Build()
		existingMAPIMachineSet.Status.Replicas = 1

		convertedMAPIMachineSet := existingMAPIMachineSet.DeepCopy()
		updatedMAPIMachineSet := existingMAPIMachineSet.DeepCopy()
		updatedMAPIMachineSet.Generation = 4

		sourceCAPIMachineSet := capiv1resourcebuilder.MachineSet().
			WithNamespace("openshift-cluster-api").
			WithName("foo").
			Build()
		sourceCAPIMachineSet.Generation = 3
		sourceCAPIMachineSet.Status.ObservedGeneration = 2

		reconciler := newMachineSetSyncUnitReconciler([]client.Object{existingMAPIMachineSet.DeepCopy()})

		updated, err := reconciler.ensureMAPIMachineSetStatusUpdated(
			ctx,
			existingMAPIMachineSet,
			convertedMAPIMachineSet,
			updatedMAPIMachineSet,
			sourceCAPIMachineSet,
			stubDiffResult{},
			true,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(updated).To(BeFalse(), "expected status updates to wait for the Cluster API observed generation")

		storedMAPIMachineSet := &mapiv1beta1.MachineSet{}
		Expect(reconciler.Client.Get(ctx, client.ObjectKeyFromObject(existingMAPIMachineSet), storedMAPIMachineSet)).To(Succeed())
		Expect(storedMAPIMachineSet.Status.Replicas).To(BeEquivalentTo(1), "expected the stored Machine API status to remain unchanged")
		Expect(storedMAPIMachineSet.Status.ObservedGeneration).To(BeZero(), "expected the stored Machine API observed generation to remain unchanged")
	})
})
