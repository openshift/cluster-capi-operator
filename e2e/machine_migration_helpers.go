// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

func createCAPIMachine(ctx context.Context, cl client.Client, machineName string) *clusterv1.Machine {
	GinkgoHelper()

	Expect(machineName).NotTo(BeEmpty(), "Machine name cannot be empty")

	workerLabelSelector := metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      clusterv1.MachineControlPlaneLabel,
				Operator: metav1.LabelSelectorOpDoesNotExist,
			},
		},
	}

	capiMachineList := capiframework.GetMachines(&workerLabelSelector)
	// The test requires at least one existing CAPI machine to act as a reference for creating a new one.
	Expect(capiMachineList).NotTo(BeEmpty(), "Should have found CAPI machines in the openshift-cluster-api namespace to use as a reference for creating a new one")

	// Select the first machine from the list as our reference.
	referenceCapiMachine := capiMachineList[0]
	By(fmt.Sprintf("Using CAPI machine %s as a reference", referenceCapiMachine.Name))

	// Define the new machine based on the reference.
	newCapiMachine := &clusterv1.Machine{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Machine",
			APIVersion: clusterv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineName,
			Namespace: referenceCapiMachine.Namespace,
		},
		Spec: *referenceCapiMachine.Spec.DeepCopy(),
	}

	// Clear status and other instance-specific fields that should not be copied.
	newCapiMachine.Spec.ProviderID = ""
	newCapiMachine.Spec.InfrastructureRef.Name = machineName
	newCapiMachine.ObjectMeta.Labels = nil
	newCapiMachine.Status = clusterv1.MachineStatus{}

	By(fmt.Sprintf("Creating a new CAPI machine in namespace: %s", newCapiMachine.Namespace))
	Eventually(func() error {
		return cl.Create(ctx, newCapiMachine)
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Should have successfully created CAPI machine %s/%s", newCapiMachine.Namespace, newCapiMachine.Name)

	referenceAWSMachine := capiframework.GetAWSMachineWithRetry(referenceCapiMachine.Name, capiframework.CAPINamespace)
	// Define the new awsmachine based on the reference.
	newAWSMachine := &awsv1.AWSMachine{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AWSMachine",
			APIVersion: awsv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineName,
			Namespace: referenceAWSMachine.Namespace,
		},
		Spec: *referenceAWSMachine.Spec.DeepCopy(),
	}

	// Clear status and other instance-specific fields that should not be copied.
	newAWSMachine.Spec.ProviderID = nil
	newAWSMachine.Spec.InstanceID = nil
	newAWSMachine.ObjectMeta.Labels = nil
	newAWSMachine.Status = awsv1.AWSMachineStatus{}

	By(fmt.Sprintf("Creating a new CAPI AWSMachine in namespace: %s", newAWSMachine.Namespace))
	Eventually(func() error {
		return cl.Create(ctx, newAWSMachine)
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Should have successfully created AWSmachine %s/%s", newAWSMachine.Namespace, newAWSMachine.Name)

	verifyMachineRunning(cl, newCapiMachine)

	return newCapiMachine
}

func createMAPIMachineWithAuthority(ctx context.Context, cl client.Client, machineName string, authority mapiv1beta1.MachineAuthority) *mapiv1beta1.Machine {
	GinkgoHelper()

	Expect(machineName).NotTo(BeEmpty(), "Machine name cannot be empty")

	workerLabelSelector := metav1.LabelSelector{
		MatchLabels: map[string]string{
			"machine.openshift.io/cluster-api-machine-role": "worker",
		},
	}
	machineList, err := mapiframework.GetMachines(ctx, cl, &workerLabelSelector)

	Expect(err).NotTo(HaveOccurred(), "Should have successfully listed MAPI machines")
	// The test requires at least one existing MAPI machine to act as a reference for creating a new one.
	Expect(machineList).NotTo(BeEmpty(), "Should have found MAPI machines in the openshift-machine-api namespace to use as a reference for creating a new one")

	// Select the first machine from the list as our reference.
	referenceMachine := machineList[0]
	By(fmt.Sprintf("Using MAPI machine %s as a reference", referenceMachine.Name))

	// Define the new machine based on the reference.
	newMachine := &mapiv1beta1.Machine{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Machine",
			APIVersion: mapiv1beta1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineName,
			Namespace: referenceMachine.Namespace,
		},
		Spec: *referenceMachine.Spec.DeepCopy(),
	}

	// Clear status and other instance-specific fields that should not be copied.
	newMachine.Spec.ProviderID = nil
	newMachine.ObjectMeta.Labels = nil
	newMachine.Status = mapiv1beta1.MachineStatus{}
	newMachine.Spec.AuthoritativeAPI = authority
	By(fmt.Sprintf("Creating a new MAPI machine with AuthoritativeAPI: %s in namespace: %s", authority, newMachine.Namespace))
	Eventually(func() error {
		return cl.Create(ctx, newMachine)
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Should have successfully created MAPI machine %s with AuthoritativeAPI: %s", newMachine.Name, authority)

	return newMachine
}

// verifyMachineRunning verifies that a machine reaches Running state using the machine object directly.
func verifyMachineRunning(cl client.Client, machine client.Object) {
	GinkgoHelper()

	Expect(machine).NotTo(BeNil(), "Machine parameter cannot be nil")
	Expect(machine.GetName()).NotTo(BeEmpty(), "Machine name cannot be empty")

	switch machine.(type) {
	case *clusterv1.Machine:
		By(fmt.Sprintf("Verifying CAPI Machine %s reaches Running phase", machine.GetName()))
	case *mapiv1beta1.Machine:
		By(fmt.Sprintf("Verifying MAPI Machine %s reaches Running phase", machine.GetName()))
	default:
		Fail(fmt.Sprintf("unknown machine type: %T", machine))
	}

	Eventually(func() error {
		switch m := machine.(type) {
		case *clusterv1.Machine:
			capiMachine, err := capiframework.GetMachine(m.GetName(), m.GetNamespace())
			if err != nil {
				return fmt.Errorf("get CAPI Machine %s: %w", m.GetName(), err)
			}
			if capiMachine.Status.Phase != string(clusterv1.MachinePhaseRunning) {
				return fmt.Errorf("CAPI Machine %s: phase %q, want Running (conditions: %v)",
					m.GetName(), capiMachine.Status.Phase, summarizeV1Beta2Conditions(capiMachine.Status.Conditions))
			}
		case *mapiv1beta1.Machine:
			mapiMachine, err := mapiframework.GetMachine(cl, m.GetName())
			if err != nil {
				return fmt.Errorf("get MAPI Machine %s: %w", m.GetName(), err)
			}
			phase := ptr.Deref(mapiMachine.Status.Phase, "")
			if phase != "Running" {
				return fmt.Errorf("MAPI Machine %s: phase %q, want Running (conditions: %v)",
					m.GetName(), phase, summarizeMAPIConditions(mapiMachine.Status.Conditions))
			}
		}

		return nil
	}, capiframework.WaitLong, capiframework.RetryLong).Should(Succeed(),
		"Machine %s should reach Running state", machine.GetName())
}

func verifyMachineAuthoritative(mapiMachine *mapiv1beta1.Machine, authority mapiv1beta1.MachineAuthority) {
	GinkgoHelper()

	By(fmt.Sprintf("Verify the Machine authority is %s", authority))
	Eventually(komega.Object(mapiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		HaveField("Status.AuthoritativeAPI", Equal(authority)),
		fmt.Sprintf("Should have found Machine with status.AuthoritativeAPI:%s", authority),
	)
}

func verifyMAPIMachineSynchronizedCondition(mapiMachine *mapiv1beta1.Machine, authority mapiv1beta1.MachineAuthority) {
	GinkgoHelper()

	By("Verify the MAPI Machine synchronized condition is True")

	var expectedMessage string
	switch authority {
	case mapiv1beta1.MachineAuthorityMachineAPI:
		expectedMessage = "Successfully synchronized MAPI Machine to CAPI"
	case mapiv1beta1.MachineAuthorityClusterAPI:
		expectedMessage = "Successfully synchronized CAPI Machine to MAPI"
	default:
		Fail(fmt.Sprintf("unknown authoritativeAPI type: %v", authority))
	}

	Eventually(komega.Object(mapiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		WithTransform(
			func(m *mapiv1beta1.Machine) []mapiv1beta1.Condition {
				return m.Status.Conditions
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
		fmt.Sprintf("Should have found the expected Synchronized condition for MAPI Machine %s with authority: %s", mapiMachine.Name, authority),
	)
}

// verifyResourceRemoved verifies that a resource has been removed.
// This is a generic function that works with any client.Object type.
func verifyResourceRemoved(resource client.Object) {
	GinkgoHelper()

	Expect(resource).NotTo(BeNil(), "Resource parameter cannot be nil")
	Expect(resource.GetName()).NotTo(BeEmpty(), "Resource name cannot be empty")

	By(fmt.Sprintf("Verifying the %T %s is removed", resource, resource.GetName()))
	Eventually(komega.Get(resource), capiframework.WaitShort, capiframework.RetryShort).Should(WithTransform(apierrors.IsNotFound, BeTrue()), "Should have successfully removed %T %s", resource, resource.GetName())
}

// verifyMachinePausedCondition verifies the Paused condition for either MAPI or CAPI machines.
// This unified function determines the machine type and expected pause state based on the authority.
func verifyMachinePausedCondition(machine client.Object, authority mapiv1beta1.MachineAuthority) {
	GinkgoHelper()

	Expect(machine).NotTo(BeNil(), "Machine parameter cannot be nil")
	Expect(machine.GetName()).NotTo(BeEmpty(), "Machine name cannot be empty")

	var conditionMatcher types.GomegaMatcher

	switch m := machine.(type) {
	case *mapiv1beta1.Machine:
		// This is a MAPI Machine
		switch authority {
		case mapiv1beta1.MachineAuthorityMachineAPI:
			By("Verify the MAPI Machine is Unpaused")

			conditionMatcher = SatisfyAll(
				HaveField("Type", Equal(MAPIPausedCondition)),
				HaveField("Status", Equal(corev1.ConditionFalse)),
				HaveField("Reason", Equal("AuthoritativeAPIMachineAPI")),
				HaveField("Message", ContainSubstring("MachineAPI")),
			)
		case mapiv1beta1.MachineAuthorityClusterAPI:
			By("Verify the MAPI Machine is Paused")

			conditionMatcher = SatisfyAll(
				HaveField("Type", Equal(MAPIPausedCondition)),
				HaveField("Status", Equal(corev1.ConditionTrue)),
				HaveField("Reason", Equal("AuthoritativeAPINotMachineAPI")),
				HaveField("Message", ContainSubstring("ClusterAPI")),
			)
		default:
			Fail(fmt.Sprintf("unknown authoritativeAPI type: %v", authority))
		}

		Eventually(komega.Object(m), capiframework.WaitMedium, capiframework.RetryMedium).Should(
			HaveField("Status.Conditions", ContainElement(conditionMatcher)),
			fmt.Sprintf("Should have found the expected Paused condition for MAPI Machine %s with authority: %s", m.Name, authority),
		)

	case *clusterv1.Machine:
		// This is a CAPI Machine
		switch authority {
		case mapiv1beta1.MachineAuthorityClusterAPI:
			By("Verify the CAPI Machine is Unpaused")

			conditionMatcher = SatisfyAll(
				HaveField("Type", Equal(CAPIPausedCondition)),
				HaveField("Status", Equal(metav1.ConditionFalse)),
				HaveField("Reason", Equal("NotPaused")),
			)
		case mapiv1beta1.MachineAuthorityMachineAPI:
			By("Verify the CAPI Machine is Paused")

			conditionMatcher = SatisfyAll(
				HaveField("Type", Equal(CAPIPausedCondition)),
				HaveField("Status", Equal(metav1.ConditionTrue)),
				HaveField("Reason", Equal("Paused")),
			)
		default:
			Fail(fmt.Sprintf("unknown authoritativeAPI type: %v", authority))
		}

		Eventually(komega.Object(m), capiframework.WaitMedium, capiframework.RetryMedium).Should(
			HaveField("Status.Conditions", ContainElement(conditionMatcher)),
			fmt.Sprintf("Should have found the expected Paused condition for CAPI Machine %s with authority: %s", m.Name, authority),
		)

	default:
		Fail(fmt.Sprintf("unknown machine type: %T", machine))
	}
}

func cleanupMachineResources(ctx context.Context, cl client.Client, capiMachines []*clusterv1.Machine, mapiMachines []*mapiv1beta1.Machine) {
	GinkgoHelper()

	for _, m := range capiMachines {
		if m == nil {
			continue
		}

		By(fmt.Sprintf("Deleting CAPI Machine %s", m.Name))
		capiframework.DeleteMachines(ctx, cl, capiframework.CAPINamespace, m)
	}

	for _, m := range mapiMachines {
		if m == nil {
			continue
		}

		By(fmt.Sprintf("Deleting MAPI Machine %s", m.Name))
		Expect(mapiframework.DeleteMachines(ctx, cl, m)).To(Succeed())
		mapiframework.WaitForMachinesDeleted(cl, m)
	}
}

func updateMachineAuthoritativeAPI(mapiMachine *mapiv1beta1.Machine, newAuthority mapiv1beta1.MachineAuthority) {
	GinkgoHelper()

	Eventually(komega.Update(mapiMachine, func() {
		mapiMachine.Spec.AuthoritativeAPI = newAuthority
	}), capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Failed to update MAPI Machine AuthoritativeAPI to %s", newAuthority)
}

func summarizeV1Beta2Conditions(conditions []metav1.Condition) string {
	if len(conditions) == 0 {
		return "none"
	}

	var parts []string
	for _, c := range conditions {
		parts = append(parts, fmt.Sprintf("%s=%s", c.Type, c.Status))
	}

	return fmt.Sprintf("[%s]", strings.Join(parts, ", "))
}

func summarizeMAPIConditions(conditions []mapiv1beta1.Condition) string {
	if len(conditions) == 0 {
		return "none"
	}

	var parts []string
	for _, c := range conditions {
		parts = append(parts, fmt.Sprintf("%s=%s", c.Type, c.Status))
	}

	return fmt.Sprintf("[%s]", strings.Join(parts, ", "))
}

func verifyMachineSynchronizedGeneration(mapiMachine *mapiv1beta1.Machine, authority mapiv1beta1.MachineAuthority) {
	GinkgoHelper()

	switch authority {
	case mapiv1beta1.MachineAuthorityMachineAPI:
		Eventually(func(g Gomega) {
			g.Expect(komega.Get(mapiMachine)()).To(Succeed())
			g.Expect(mapiMachine.Status.SynchronizedGeneration).NotTo(BeZero())
			g.Expect(mapiMachine.Status.SynchronizedGeneration).To(Equal(mapiMachine.Generation),
				"MAPI SynchronizedGeneration (%d) should equal MAPI Generation (%d)",
				mapiMachine.Status.SynchronizedGeneration, mapiMachine.Generation)
		}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed())
	case mapiv1beta1.MachineAuthorityClusterAPI:
		capiMachine := &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: mapiMachine.Name, Namespace: capiframework.CAPINamespace},
		}

		Eventually(func(g Gomega) {
			g.Expect(komega.Get(mapiMachine)()).To(Succeed())
			g.Expect(komega.Get(capiMachine)()).To(Succeed())
			g.Expect(mapiMachine.Status.SynchronizedGeneration).NotTo(BeZero())
			g.Expect(mapiMachine.Status.SynchronizedGeneration).To(Equal(capiMachine.Generation),
				"MAPI SynchronizedGeneration (%d) should equal CAPI Generation (%d)",
				mapiMachine.Status.SynchronizedGeneration, capiMachine.Generation)
		}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed())
	default:
		Fail(fmt.Sprintf("unknown authoritativeAPI type: %v", authority))
	}
}
