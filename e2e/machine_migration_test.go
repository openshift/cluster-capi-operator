package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	SynchronizedCondition machinev1beta1.ConditionType = "Synchronized"
	PausedCondition       machinev1beta1.ConditionType = "Paused"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Machine Creation", Ordered, func() {
	var machineNameCAPI = "machine-auth-capi-creation"
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this only support on aws", platform))
		}
		/*
			if !framework.IsMachineAPIMigrationEnabled(ctx, cl) {
				Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
			}*/
	})

	AfterEach(func() {
		mapiMachine, err := mapiframework.GetMachine(cl, machineNameCAPI)
		if err != nil {
			klog.Warningf("Skip MAPI Machine cleanup: failed to get MAPI Machine: %v", err)
			return
		}

		if mapiMachine == nil {
			klog.Infof("No MAPI Machine found, nothing to clean up.")
			return
		}

		By("Deleting the created Machine")
		mapiframework.DeleteMachines(ctx, cl, mapiMachine)
		mapiframework.WaitForMachinesDeleted(cl, mapiMachine)
	})

	Context("when existing CAPI Machine with that name", func() {
		It("should allow creating the MAPI Machine with specAPI: CAPI", func() {
			newCapimachine := createCAPIMachine(ctx, cl, machineNameCAPI)
			createMAPIMachineWithAuthority(ctx, cl, machineNameCAPI, machinev1beta1.MachineAuthorityClusterAPI)

			verifyMachineAuthoritative(ctx, cl, newCapimachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
			//there is a bug for this https://issues.redhat.com/browse/OCPBUGS-54703
			//verifySynchronizedCondition(ctx, cl, newMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
			//verifySynchronizedGeneration(ctx, cl, newMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
			verifyMAPIPausedCondition(ctx, cl, newCapimachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
			verifyCAPIPausedCondition(cl, newCapimachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
		})
	})

	Context("when no existing CAPI Machine with that name", func() {
		It("should allow creating the MAPI Machine with specAPI: CAPI", func() {
			newMapiMachine := createMAPIMachineWithAuthority(ctx, cl, machineNameCAPI, machinev1beta1.MachineAuthorityClusterAPI)

			verifyMachineRunning(ctx, cl, newMapiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
			verifyMachineAuthoritative(ctx, cl, newMapiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
			//there is a bug for this https://issues.redhat.com/browse/OCPBUGS-54703
			//verifySynchronizedCondition(ctx, cl, newMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
			//verifySynchronizedGeneration(ctx, cl, newMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
			verifyMAPIPausedCondition(ctx, cl, newMapiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
			verifyCAPIPausedCondition(cl, newMapiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
		})
	})
})

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Machine Update", Ordered, func() {
	var machineNameCAPI = "machine-auth-capi-update"
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this only support on aws", platform))
		}
		/*
			if !framework.IsMachineAPIMigrationEnabled(ctx, cl) {
				Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
			}*/

	})

	AfterEach(func() {
		mapiMachine, err := mapiframework.GetMachine(cl, machineNameCAPI)
		if err != nil {
			klog.Warningf("Skip MAPI Machine cleanup: failed to get MAPI Machine: %v", err)
			return
		}

		if mapiMachine == nil {
			klog.Infof("No MAPI Machine found, nothing to clean up.")
			return
		}

		By("Deleting the created Machine")
		mapiframework.DeleteMachines(ctx, cl, mapiMachine)
		mapiframework.WaitForMachinesDeleted(cl, mapiMachine)
	})

	Context("when both MAPI and CAPI machine exist and specAPI: CAPI", func() {
		It("should allow updating the MAPI Machine labels/annotations", func() {
			//This doesn't work seems because of bug https://issues.redhat.com/browse/OCPBUGS-54703
			//newMapiMachine := createMAPIMachineWithAuthority(ctx, cl, machineNameCAPI, machinev1beta1.MachineAuthorityClusterAPI)
			//verifyMachineRunning(ctx, cl, newMapiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
			//update labels/annotations
		})
	})
})

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Machine Deletion", Ordered, func() {
	var machineNameCAPI = "machine-auth-capi-deletion"
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this only support on aws", platform))
		}
		/*
			if !framework.IsMachineAPIMigrationEnabled(ctx, cl) {
				Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
			}*/

	})

	AfterEach(func() {
		mapiMachine, err := mapiframework.GetMachine(cl, machineNameCAPI)
		if err != nil {
			klog.Warningf("Skip MAPI Machine cleanup: failed to get MAPI Machine: %v", err)
			return
		}

		if mapiMachine == nil {
			klog.Infof("No MAPI Machine found, nothing to clean up.")
			return
		}

		By("Deleting the created Machine")
		mapiframework.DeleteMachines(ctx, cl, mapiMachine)
		mapiframework.WaitForMachinesDeleted(cl, mapiMachine)
	})

	Context("when both MAPI and CAPI machine exist and specAPI: CAPI", func() {
		It("should delete MAPI machine work", func() {
			newMapiMachine := createMAPIMachineWithAuthority(ctx, cl, machineNameCAPI, machinev1beta1.MachineAuthorityClusterAPI)
			verifyMachineRunning(ctx, cl, newMapiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
			By("Deleting the MAPI Machine")
			mapiframework.DeleteMachines(ctx, cl, newMapiMachine)
			mapiframework.WaitForMachinesDeleted(cl, newMapiMachine)
			verifyCAPIMachineRemoved(cl, machineNameCAPI)
			verifyAWSMachineRemoved(cl, machineNameCAPI)
		})

		It("should delete CAPI machine work", func() {
			newMapiMachine := createMAPIMachineWithAuthority(ctx, cl, machineNameCAPI, machinev1beta1.MachineAuthorityClusterAPI)
			verifyMachineRunning(ctx, cl, newMapiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
			newCapiMachine, err := framework.GetMachine(cl, newMapiMachine.Name)
			Expect(err).NotTo(HaveOccurred(), "Failed to get capi machine")
			By("Deleting the CAPI Machine")
			framework.DeleteMachines(cl, newCapiMachine)
			framework.WaitForMachinesDeleted(cl, newCapiMachine)
			verifyMAPIMachineRemoved(cl, machineNameCAPI)
			verifyAWSMachineRemoved(cl, machineNameCAPI)
		})
	})
})

func createCAPIMachine(ctx context.Context, cl client.Client, machineName string) *clusterv1.Machine {
	capiMachineList, err := framework.GetMachines(cl)
	Expect(err).NotTo(HaveOccurred(), "Failed to list capi machines")
	// The test requires at least one existing capi machine to act as a template.
	Expect(capiMachineList).NotTo(BeEmpty(), "No capi machines found in the openshift-cluster-api namespace to use as a template")

	// Select the first machine from the list as our template.
	templateCapiMachine := capiMachineList[0]
	By(fmt.Sprintf("Using capi machine %s as a template", templateCapiMachine.Name))

	// Define the new machine based on the template.
	newCapiMachine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineName,
			Namespace: templateCapiMachine.Namespace,
		},
		Spec: *templateCapiMachine.Spec.DeepCopy(),
	}

	// Clear status and other instance-specific fields that should not be copied.
	newCapiMachine.Spec.ProviderID = nil
	newCapiMachine.Spec.InfrastructureRef.Name = machineName
	newCapiMachine.ObjectMeta.Labels = nil
	newCapiMachine.Status = clusterv1.MachineStatus{}

	By(fmt.Sprintf("Creating a new capi machine in namespace: %s", newCapiMachine.Namespace))
	Expect(cl.Create(ctx, newCapiMachine)).To(Succeed())

	templateAWSMachine, err := framework.GetAWSMachine(cl, templateCapiMachine.Name)
	Expect(err).NotTo(HaveOccurred(), "Failed to get AWSMachine")
	// Define the new awsmachine based on the template.
	newAWSMachine := &awsv1.AWSMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineName,
			Namespace: templateAWSMachine.Namespace,
		},
		Spec: *templateAWSMachine.Spec.DeepCopy(),
	}

	// Clear status and other instance-specific fields that should not be copied.
	newAWSMachine.Spec.ProviderID = nil
	newAWSMachine.Spec.InstanceID = nil
	newAWSMachine.ObjectMeta.Labels = nil
	newAWSMachine.Status = awsv1.AWSMachineStatus{}

	By(fmt.Sprintf("Creating a new awsmachine in namespace: %s", newAWSMachine.Namespace))
	Expect(cl.Create(ctx, newAWSMachine)).To(Succeed())

	verifyMachineRunning(ctx, cl, newCapiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)

	return newCapiMachine
}

func createMAPIMachineWithAuthority(ctx context.Context, cl client.Client, machineName string, auth machinev1beta1.MachineAuthority) *machinev1beta1.Machine {
	workerLabelSelector := metav1.LabelSelector{
		MatchLabels: map[string]string{
			"machine.openshift.io/cluster-api-machine-role": "worker",
		},
	}
	machineList, err := mapiframework.GetMachines(ctx, cl, &workerLabelSelector)

	Expect(err).NotTo(HaveOccurred(), "Failed to list mapi machines")
	// The test requires at least one existing mapi machine to act as a template.
	Expect(machineList).NotTo(BeEmpty(), "No mapi machines found in the openshift-machine-api namespace to use as a template")

	// Select the first machine from the list as our template.
	templateMachine := machineList[0]
	By(fmt.Sprintf("Using mapi machine %s as a template", templateMachine.Name))

	// Define the new machine based on the template.
	newMachine := &machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineName,
			Namespace: templateMachine.Namespace,
		},
		Spec: *templateMachine.Spec.DeepCopy(),
	}

	// Clear status and other instance-specific fields that should not be copied.
	newMachine.Spec.ProviderID = nil
	newMachine.ObjectMeta.Labels = nil
	newMachine.Status = machinev1beta1.MachineStatus{}
	newMachine.Spec.AuthoritativeAPI = auth
	By(fmt.Sprintf("Creating a new %s machine in namespace: %s", auth, newMachine.Namespace))
	Expect(cl.Create(ctx, newMachine)).To(Succeed())

	return newMachine
}

func verifyMachineRunning(ctx context.Context, cl client.Client, machineName string, auth machinev1beta1.MachineAuthority) {
	Eventually(func() string {
		switch auth {
		case machinev1beta1.MachineAuthorityClusterAPI:
			By("Verify the CAPI Machine is Running")
			capiMachine, err := framework.GetMachine(cl, machineName)
			if err != nil {
				return ""
			}
			return string(capiMachine.Status.Phase)
		case machinev1beta1.MachineAuthorityMachineAPI:
			By("Verify the MAPI Machine is Running")
			mapiMachine, err := mapiframework.GetMachine(cl, machineName)
			if err != nil {
				return ""
			}
			return string(*mapiMachine.Status.Phase)
		default:
			Fail(fmt.Sprintf("unknown authoritative API type: %v", auth))
			return ""
		}

	}, framework.WaitLong, framework.RetryLong).Should(Equal("Running"), "%s Machine did not get Running", auth)
}

func verifyMachineAuthoritative(ctx context.Context, cl client.Client, machineName string, auth machinev1beta1.MachineAuthority) {
	By(fmt.Sprintf("Verify the Machine authoritative is %s", auth))
	Eventually(func() machinev1beta1.MachineAuthority {
		mapiMachine, err := mapiframework.GetMachine(cl, machineName)
		if err != nil {
			return ""
		}
		return mapiMachine.Status.AuthoritativeAPI
	}, framework.WaitMedium, framework.RetryMedium).Should(
		Equal(auth),
		"MAPI Machine status.AuthoritativeAPI should be %s", auth)
}

func verifySynchronizedCondition(ctx context.Context, cl client.Client, machineName string, auth machinev1beta1.MachineAuthority) {
	By("Verify the MAPI Machine synchronized condition is True")
	var expectedMessage string
	switch auth {
	case machinev1beta1.MachineAuthorityMachineAPI:
		expectedMessage = "Successfully synchronized MAPI Machine to CAPI"
	case machinev1beta1.MachineAuthorityClusterAPI:
		expectedMessage = "Successfully synchronized CAPI Machine to MAPI"
	default:
		Fail(fmt.Sprintf("unknown authoritative API type: %v", auth))
	}

	Eventually(func() []machinev1beta1.Condition {
		mapiMachine, err := mapiframework.GetMachine(cl, machineName)
		if err != nil {
			return nil
		}
		return mapiMachine.Status.Conditions
	}, framework.WaitMedium, framework.RetryMedium).Should(
		ContainElement(
			SatisfyAll(
				HaveField("Type", Equal(SynchronizedCondition)),
				HaveField("Status", Equal(corev1.ConditionTrue)),
				HaveField("Reason", Equal("ResourceSynchronized")),
				HaveField("Message", Equal(expectedMessage)),
			),
		),
		fmt.Sprintf("Expected Synchronized condition for %s not found or incorrect", auth))
}

func verifySynchronizedGeneration(ctx context.Context, cl client.Client, machineName string, auth machinev1beta1.MachineAuthority) {
	synchronizedGeneration := -1
	Eventually(func() int {
		By("Get the MAPI Machine synchronizedGeneration")
		mapiMachine, err := mapiframework.GetMachine(cl, machineName)
		if err != nil {
			return 0
		}
		synchronizedGeneration = int(mapiMachine.Status.SynchronizedGeneration)
		mapiGeneration := int(mapiMachine.Generation)
		switch auth {
		case machinev1beta1.MachineAuthorityClusterAPI:
			By("Return the CAPI Machine generation")
			capiMachine, err := framework.GetMachine(cl, machineName)
			if err != nil {
				return 0
			}
			return int(capiMachine.Generation)
		case machinev1beta1.MachineAuthorityMachineAPI:
			By("Return the MAPI Machine generation")
			return mapiGeneration
		default:
			Fail(fmt.Sprintf("unknown authoritative API type: %v", auth))
			return 0
		}

	}, framework.WaitShort, framework.RetryShort).Should(Equal(synchronizedGeneration), "synchronizedGeneration is not %s  machine's generation", auth)
}

func verifyMAPIPausedCondition(ctx context.Context, cl client.Client, machineName string, auth machinev1beta1.MachineAuthority) {
	var conditionMatcher types.GomegaMatcher

	switch auth {
	case machinev1beta1.MachineAuthorityMachineAPI:
		By("Verify the MAPI Machine is Unpaused")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(PausedCondition)),
			HaveField("Status", Equal(corev1.ConditionFalse)),
			HaveField("Reason", Equal("AuthoritativeAPIMachineAPI")),
			HaveField("Message", Equal("The AuthoritativeAPI is set to MachineAPI")),
		)
	case machinev1beta1.MachineAuthorityClusterAPI:
		By("Verify the MAPI Machine is Paused")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(PausedCondition)),
			HaveField("Status", Equal(corev1.ConditionTrue)),
			HaveField("Reason", Equal("AuthoritativeAPINotMachineAPI")),
			HaveField("Message", Equal("The AuthoritativeAPI is set to ClusterAPI")),
		)
	default:
		Fail(fmt.Sprintf("unknown authoritative API type: %v", auth))
	}

	Eventually(func() []machinev1beta1.Condition {
		mapiMachine, err := mapiframework.GetMachine(cl, machineName)
		if err != nil {
			return nil
		}
		return mapiMachine.Status.Conditions
	}, framework.WaitMedium, framework.RetryMedium).Should(
		ContainElement(conditionMatcher),
		fmt.Sprintf("Expected paused condition for %s not found", auth),
	)
}

func verifyCAPIPausedCondition(cl client.Client, machineName string, auth machinev1beta1.MachineAuthority) {
	var conditionMatcher types.GomegaMatcher

	switch auth {
	case machinev1beta1.MachineAuthorityClusterAPI:
		By("Verify the CAPI Machine is Unpaused")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(capiv1beta1.PausedV1Beta2Condition)),
			HaveField("Status", Equal(metav1.ConditionFalse)),
			HaveField("Reason", Equal("NotPaused")),
		)
	case machinev1beta1.MachineAuthorityMachineAPI:
		By("Verify the CAPI Machine is Paused")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(capiv1beta1.PausedV1Beta2Condition)),
			HaveField("Status", Equal(metav1.ConditionTrue)),
			HaveField("Reason", Equal("Paused")),
		)
	default:
		Fail(fmt.Sprintf("unknown authoritative API type: %v", auth))
	}

	Eventually(func() []metav1.Condition {
		capiMachine, err := framework.GetMachine(cl, machineName)
		if err != nil {
			return nil
		}
		return capiMachine.Status.V1Beta2.Conditions
	}, framework.WaitMedium, framework.RetryMedium).Should(
		ContainElement(conditionMatcher),
		fmt.Sprintf("Expected paused condition for %s not found", auth),
	)
}

func verifyCAPIMachineRemoved(cl client.Client, machineName string) {
	By(fmt.Sprintf("Verifying the CAPI Machine %s is removed", machineName))
	capimachine, err := framework.GetMachine(cl, machineName)
	Expect(err).To(HaveOccurred())
	Expect(capimachine).To(BeNil())
}

func verifyAWSMachineRemoved(cl client.Client, machineName string) {
	By(fmt.Sprintf("Verifying the AWSMachine %s is removed", machineName))
	awsmachine, err := framework.GetAWSMachine(cl, machineName)
	Expect(err).To(HaveOccurred())
	Expect(awsmachine).To(BeNil())
}

func verifyMAPIMachineRemoved(cl client.Client, machineName string) {
	By(fmt.Sprintf("Verifying the MAPI Machine %s is removed", machineName))
	mapimachine, err := mapiframework.GetMachine(cl, machineName)
	Expect(err).To(HaveOccurred())
	Expect(mapimachine).To(BeNil())
}
