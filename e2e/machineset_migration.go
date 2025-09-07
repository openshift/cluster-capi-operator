// Copyright 2024 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

const (
	// SynchronizedCondition indicates that the machine is synchronized with the provider's state.
	SynchronizedCondition machinev1beta1.ConditionType = "Synchronized"
	// MAPIPausedCondition indicates that the machine API is paused.
	MAPIPausedCondition machinev1beta1.ConditionType = "Paused"
	// CAPIPausedCondition indicates that the cluster API is paused.
	CAPIPausedCondition = clusterv1.PausedV1Beta2Condition
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration][platform:aws][Disruptive] MachineSet Migration Tests", Ordered, Label("Conformance"), Label("Serial"), func() {
	BeforeAll(func() {
		InitCommonVariables()
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this only support on aws", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	var _ = Describe("Create MAPI MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName = "ms-authoritativeapi-mapi"
		var mapiMSAuthCAPIName = "ms-authoritativeapi-capi"
		var existingCAPIMSAuthorityMAPIName = "capi-machineset-authoritativeapi-mapi"
		var existingCAPIMSAuthorityCAPIName = "capi-machineset-authoritativeapi-capi"

		var awsMachineTemplate *awsv1.AWSMachineTemplate
		var capiMachineSet *clusterv1.MachineSet
		var mapiMachineSet *machinev1beta1.MachineSet
		var err error

		Context("with spec.authoritativeAPI: MAPI and existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				capiMachineSet = createCAPIMachineSet(ctx, cl, 0, existingCAPIMSAuthorityMAPIName, "")

				Eventually(func() error {
					awsMachineTemplate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, existingCAPIMSAuthorityMAPIName, capiframework.CAPINamespace)
					if err != nil {
						return fmt.Errorf("failed to get AWSMachineTemplate: %w", err)
					}

					return nil
				}, capiframework.WaitMedium, capiframework.RetryShort).Should(Succeed(), "awsMachineTemplate should exist")

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MAPI and existing CAPI MachineSet with same name' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*machinev1beta1.MachineSet{},
					)
				})
			})

			// https://issues.redhat.com/browse/OCPCLOUD-2641
			PIt("should reject creation of MAPI MachineSet with same name as existing CAPI MachineSet", func() {
				By("Creating a same name MAPI MachineSet")
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, existingCAPIMSAuthorityMAPIName, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
				Expect(err).To(HaveOccurred(), "denied request to create MAPI MachineSet %s", mapiMachineSet.GetName())
			})
		})

		Context("with spec.authoritativeAPI: MAPI and when no existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthMAPIName, machinev1beta1.MachineAuthorityMachineAPI, machinev1beta1.MachineAuthorityMachineAPI)
				Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet %s creation should succeed", mapiMachineSet.GetName())

				Eventually(func() error {
					awsMachineTemplate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, mapiMSAuthMAPIName, capiframework.CAPINamespace)
					if err != nil {
						return fmt.Errorf("failed to get AWSMachineTemplate: %w", err)
					}

					return nil
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "awsMachineTemplate should exist")

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MAPI and when no existing CAPI MachineSet with same name' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*machinev1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should find MAPI MachineSet .status.authoritativeAPI to equal MAPI", func() {
				Eventually(komega.Object(mapiMachineSet)).Should(HaveField("Status.AuthoritativeAPI", Equal(machinev1beta1.MachineAuthorityMachineAPI)))
			})

			It("should verify that MAPI MachineSet paused condition is False", func() {
				verifyMAPIPausedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityMachineAPI)
			})

			It("should verify that MAPI MachineSet Synchronized condition is True", func() {
				verifySynchronizedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityMachineAPI)
			})

			It("should find that MAPI MachineSet has a CAPI MachineSet mirror", func() {
				Eventually(func() error {
					capiMachineSet, err = capiframework.GetMachineSet(cl, mapiMSAuthMAPIName, capiframework.CAPINamespace)
					if err != nil {
						return fmt.Errorf("failed to get CAPI MachineSet: %w", err)
					}

					return nil
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "CAPI MachineSet mirror should exist")
			})

			It("should verify that the mirror CAPI MachineSet has Paused condition True", func() {
				verifyCAPIPausedCondition(capiMachineSet, machinev1beta1.MachineAuthorityMachineAPI)
			})
		})

		Context("with spec.authoritativeAPI: CAPI and existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				capiMachineSet = createCAPIMachineSet(ctx, cl, 0, existingCAPIMSAuthorityCAPIName, "m5.large")

				By("Creating a same name MAPI MachineSet")
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, existingCAPIMSAuthorityCAPIName, machinev1beta1.MachineAuthorityClusterAPI, machinev1beta1.MachineAuthorityClusterAPI)
				Expect(err).ToNot(HaveOccurred(), "failed to create MAPI MachineSet %s", existingCAPIMSAuthorityCAPIName)

				Eventually(func() error {
					awsMachineTemplate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, existingCAPIMSAuthorityCAPIName, capiframework.CAPINamespace)
					if err != nil {
						return fmt.Errorf("failed to get AWSMachineTemplate: %w", err)
					}

					return nil
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "awsMachineTemplate should exist")

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: CAPI and existing CAPI MachineSet with same name' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*machinev1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should verify that MAPI MachineSet has Paused condition True", func() {
				verifyMAPIPausedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityClusterAPI)
			})

			// bug https://issues.redhat.com/browse/OCPBUGS-55337
			PIt("should verify that the non-authoritative MAPI MachineSet providerSpec has been updated to reflect the authoritative CAPI MachineSet mirror values", func() {
				Eventually(func() string {
					providerSpec := mapiMachineSet.Spec.Template.Spec.ProviderSpec
					var awsConfig machinev1beta1.AWSMachineProviderConfig
					_ = json.Unmarshal(providerSpec.Value.Raw, &awsConfig)

					return awsConfig.InstanceType
				}, capiframework.WaitMedium, capiframework.RetryShort).Should(Equal("m5.large"), "MAPI MSet Sepc should be updated to reflect existing CAPI mirror")
			})
		})

		Context("with spec.authoritativeAPI: CAPI and no existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				mapiMachineSet, err = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthCAPIName, machinev1beta1.MachineAuthorityClusterAPI, machinev1beta1.MachineAuthorityClusterAPI)
				Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet %s creation should succeed", mapiMachineSet.GetName())

				Eventually(func() error {
					capiMachineSet, err = capiframework.GetMachineSet(cl, mapiMSAuthCAPIName, capiframework.CAPINamespace)
					if err != nil {
						return fmt.Errorf("failed to get CAPI MachineSet: %w", err)
					}

					return nil
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "CAPI MachineSet should exist")

				Eventually(func() error {
					awsMachineTemplate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, mapiMSAuthCAPIName, capiframework.CAPINamespace)
					if err != nil {
						return fmt.Errorf("failed to get AWSMachineTemplate: %w", err)
					}

					return nil
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "awsMachineTemplate should exist")

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: CAPI and no existing CAPI MachineSet with same name' resources")
					cleanupTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*machinev1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should find MAPI MachineSet .status.authoritativeAPI to equal CAPI", func() {
				Eventually(komega.Object(mapiMachineSet)).Should(HaveField("Status.AuthoritativeAPI", Equal(machinev1beta1.MachineAuthorityClusterAPI)))
			})

			It("should verify that MAPI MachineSet paused condition is True", func() {
				verifyMAPIPausedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that MAPI MachineSet Synchronized condition is True", func() {
				verifySynchronizedCondition(mapiMachineSet, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that the non-authoritative MAPI MachineSet has an authoritative CAPI MachineSet mirror", func() {
				Eventually(func() error {
					capiMachineSet, err = capiframework.GetMachineSet(cl, mapiMSAuthCAPIName, capiframework.CAPINamespace)
					if err != nil {
						return fmt.Errorf("failed to get CAPI MachineSet: %w", err)
					}

					return nil
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "CAPI MachineSet should exist")
			})

			It("should verify that CAPI MachineSet has Paused condition False", func() {
				verifyCAPIPausedCondition(capiMachineSet, machinev1beta1.MachineAuthorityClusterAPI)
			})
		})
	})
})

//nolint:unparam
func createMAPIMachineSetWithAuthoritativeAPI(ctx context.Context, cl client.Client, replicas int, machineSetName string, machineSetAuthority machinev1beta1.MachineAuthority, machineAuthority machinev1beta1.MachineAuthority) (*machinev1beta1.MachineSet, error) {
	By(fmt.Sprintf("Creating MAPI MachineSet with spec.authoritativeAPI: %s, spec.template.spec.authoritativeAPI: %s, replicas=%d", machineSetAuthority, machineAuthority, replicas))
	machineSetParams := mapiframework.BuildMachineSetParams(ctx, cl, replicas)
	machineSetParams.Name = machineSetName
	machineSetParams.MachinesetAuthoritativeAPI = machineSetAuthority
	machineSetParams.MachineAuthoritativeAPI = machineAuthority
	// Now CAPI machineSet doesn't support taint, remove it. card https://issues.redhat.com/browse/OCPCLOUD-2861
	machineSetParams.Taints = []corev1.Taint{}
	mapiMachineSet, err := mapiframework.CreateMachineSet(cl, machineSetParams)
	Expect(err).ToNot(HaveOccurred(), "MAPI MachineSet %s creation should succeed", machineSetName)

	capiMachineSet := &clusterv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineSetName,
			Namespace: capiframework.CAPINamespace,
		},
	}
	Eventually(komega.Get(capiMachineSet), capiframework.WaitShort, capiframework.RetryShort).Should(
		Succeed(), "Mirror CAPI MachineSet should be created within 1 minute")

	//nolint:exhaustive
	switch machineAuthority {
	case machinev1beta1.MachineAuthorityMachineAPI:
		mapiframework.WaitForMachineSet(ctx, cl, machineSetName)
	case machinev1beta1.MachineAuthorityClusterAPI:
		capiframework.WaitForMachineSet(ctx, cl, machineSetName, capiframework.CAPINamespace)
	}

	return mapiMachineSet, nil
}

func createCAPIMachineSet(ctx context.Context, cl client.Client, replicas int32, machineSetName string, instanceType string) *clusterv1.MachineSet {
	By(fmt.Sprintf("Creating CAPI MachineSet %s with %d replicas", machineSetName, replicas))

	_, mapiDefaultProviderSpec := getDefaultAWSMAPIProviderSpec(cl, ctx)
	createAWSClient(ctx, mapiDefaultProviderSpec.Placement.Region)
	awsMachineTemplate := newAWSMachineTemplate(mapiDefaultProviderSpec)
	awsMachineTemplate.Name = machineSetName

	if instanceType != "" {
		awsMachineTemplate.Spec.Template.Spec.InstanceType = instanceType
	}

	if err := cl.Create(ctx, awsMachineTemplate); err != nil {
		Expect(err).ToNot(HaveOccurred())
	}

	machineSet := capiframework.CreateMachineSet(ctx, cl, capiframework.NewMachineSetParams(
		machineSetName,
		clusterName,
		"",
		replicas,
		corev1.ObjectReference{
			Kind:       "AWSMachineTemplate",
			APIVersion: infraAPIVersion,
			Name:       machineSetName,
		},
		"worker-user-data",
	))

	capiframework.WaitForMachineSet(ctx, cl, machineSet.Name, machineSet.Namespace)

	return machineSet
}

func verifySynchronizedCondition(mapiMachineSet *machinev1beta1.MachineSet, authority machinev1beta1.MachineAuthority) {
	By("Verify the MAPI MachineSet Synchronized condition is True")

	var expectedMessage string

	//nolint:exhaustive
	switch authority {
	case machinev1beta1.MachineAuthorityMachineAPI:
		expectedMessage = "Successfully synchronized MAPI MachineSet to CAPI"
	case machinev1beta1.MachineAuthorityClusterAPI:
		expectedMessage = "Successfully synchronized CAPI MachineSet to MAPI"
	default:
		Fail(fmt.Sprintf("unknown authoritativeAPI type: %v", authority))
	}

	Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		WithTransform(
			func(ms *machinev1beta1.MachineSet) []machinev1beta1.Condition {
				return ms.Status.Conditions
			},
			ContainElement(
				SatisfyAll(
					HaveField("Type", Equal(SynchronizedCondition)),
					HaveField("Status", Equal(corev1.ConditionTrue)),
					HaveField("Reason", Equal("ResourceSynchronized")),
					HaveField("Message", Equal(expectedMessage)),
				),
			),
		),
		fmt.Sprintf("Expected Synchronized condition for %s not found or incorrect", authority),
	)
}

func verifyMAPIPausedCondition(mapiMachineSet *machinev1beta1.MachineSet, authority machinev1beta1.MachineAuthority) {
	var conditionMatcher types.GomegaMatcher

	//nolint:exhaustive
	switch authority {
	case machinev1beta1.MachineAuthorityMachineAPI:
		By("Verifying MAPI MachineSet is unpaused")

		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(MAPIPausedCondition)),
			HaveField("Status", Equal(corev1.ConditionFalse)),
			HaveField("Reason", Equal("AuthoritativeAPIMachineAPI")),
			HaveField("Message", ContainSubstring("MachineAPI")),
		)
	case machinev1beta1.MachineAuthorityClusterAPI:
		By("Verifying MAPI MachineSet is paused")

		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(MAPIPausedCondition)),
			HaveField("Status", Equal(corev1.ConditionTrue)),
			HaveField("Reason", Equal("AuthoritativeAPINotMachineAPI")),
			HaveField("Message", ContainSubstring("ClusterAPI")),
		)
	default:
		Fail(fmt.Sprintf("unknown authoritativeAPI type: %v", authority))
	}

	Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		HaveField("Status.Conditions", ContainElement(conditionMatcher)),
		fmt.Sprintf("Expected MAPI MachineSet with correct paused condition for %s", authority),
	)
}

func verifyCAPIPausedCondition(capiMachineSet *clusterv1.MachineSet, authority machinev1beta1.MachineAuthority) {
	var conditionMatcher types.GomegaMatcher

	//nolint:exhaustive
	switch authority {
	case machinev1beta1.MachineAuthorityClusterAPI:
		By("Verifying CAPI MachineSet is unpaused (ClusterAPI is authoritative)")

		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(CAPIPausedCondition)),
			HaveField("Status", Equal(metav1.ConditionFalse)),
			HaveField("Reason", Equal("NotPaused")),
		)
	case machinev1beta1.MachineAuthorityMachineAPI:
		By("Verifying CAPI MachineSet is paused (MachineAPI is authoritative)")

		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(CAPIPausedCondition)),
			HaveField("Status", Equal(metav1.ConditionTrue)),
			HaveField("Reason", Equal("Paused")),
		)
	default:
		Fail(fmt.Sprintf("unknown authoritativeAPI type: %v", authority))
	}

	Eventually(komega.Object(capiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		HaveField("Status.V1Beta2.Conditions", ContainElement(conditionMatcher)),
		fmt.Sprintf("Expected CAPI MachineSet with correct paused condition for %s", authority),
	)
}

func cleanupTestResources(ctx context.Context, cl client.Client, capiMachineSets []*clusterv1.MachineSet, awsMachineTemplates []*awsv1.AWSMachineTemplate, mapiMachineSets []*machinev1beta1.MachineSet) {
	for _, ms := range capiMachineSets {
		if ms == nil {
			continue
		}

		By(fmt.Sprintf("Deleting CAPI MachineSet %s", ms.Name))
		capiframework.DeleteMachineSets(ctx, cl, ms)
		capiframework.WaitForMachineSetsDeleted(ctx, cl, ms)
	}

	for _, ms := range mapiMachineSets {
		if ms == nil {
			continue
		}

		By(fmt.Sprintf("Deleting MAPI MachineSet %s", ms.Name))

		err := mapiframework.DeleteMachineSets(cl, ms)
		if err != nil && !apierrors.IsNotFound(err) {
			Expect(err).ToNot(HaveOccurred(), "Failed to delete MAPI MachineSet")
		}

		mapiframework.WaitForMachineSetsDeleted(ctx, cl, ms)
	}

	for _, template := range awsMachineTemplates {
		if template == nil {
			continue
		}

		By(fmt.Sprintf("Deleting awsMachineTemplate %s", template.Name))
		capiframework.DeleteAWSMachineTemplates(ctx, cl, template)
	}
}
