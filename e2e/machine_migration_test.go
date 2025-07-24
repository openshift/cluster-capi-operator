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
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Machine Creation", Ordered, func() {
	var machineNameCAPI = "machine-auth-capi-creation"
	var newCapimachine *clusterv1.Machine
	var newMapiMachine *machinev1beta1.Machine
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this only support on aws", platform))
		}

		if !framework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	Context("when existing CAPI Machine with same name should allow creating the MAPI Machine with specAPI: CAPI", func() {
		BeforeAll(func() {
			newCapimachine = createCAPIMachine(ctx, cl, machineNameCAPI)
			createMAPIMachineWithAuthority(ctx, cl, machineNameCAPI, machinev1beta1.MachineAuthorityClusterAPI)

			DeferCleanup(func() {
				By("Cleaning up machine resources")
				cleanupMachineResources(
					ctx,
					cl,
					machineNameCAPI,
				)
			})
		})

		It("should verify MAPI Machine .status.authoritativeAPI to equal CAPI", func() {
			verifyMachineAuthoritative(ctx, cl, newCapimachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
		})
		//there is a bug for this https://issues.redhat.com/browse/OCPBUGS-54703
		//verifyMachineSynchronizedCondition(ctx, cl, newMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
		//verifyMachineSynchronizedGeneration(ctx, cl, newMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
		It("should verify MAPI Machine Paused condition is True", func() {
			verifyMAPIMachinePausedCondition(ctx, cl, newCapimachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
		})
		It("should verify CAPI Machine Paused condition is False", func() {
			verifyCAPIMachinePausedCondition(cl, newCapimachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
		})
	})

	Context("when no existing CAPI Machine with same name should allow creating the MAPI Machine with specAPI: CAPI", func() {
		BeforeAll(func() {
			newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, machineNameCAPI, machinev1beta1.MachineAuthorityClusterAPI)

			DeferCleanup(func() {
				By("Cleaning up machine resources")
				cleanupMachineResources(
					ctx,
					cl,
					machineNameCAPI,
				)
			})
		})

		It("should verify CAPI Machine get Running", func() {
			verifyMachineRunning(ctx, cl, newMapiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
		})
		It("should verify MAPI Machine .status.authoritativeAPI to equal CAPI", func() {
			verifyMachineAuthoritative(ctx, cl, newMapiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
		})
		//there is a bug for this https://issues.redhat.com/browse/OCPBUGS-54703
		//verifyMachineSynchronizedCondition(ctx, cl, newMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
		//verifyMachineSynchronizedGeneration(ctx, cl, newMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
		It("should verify MAPI Machine Paused condition is True", func() {
			verifyMAPIMachinePausedCondition(ctx, cl, newMapiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
		})
		It("should verify CAPI Machine Paused condition is False", func() {
			verifyCAPIMachinePausedCondition(cl, newMapiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
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

	templateAWSMachine, err := framework.GetAWSMachine(cl, templateCapiMachine.Name, framework.CAPINamespace)
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
			capiMachine, err := framework.GetMachine(cl, machineName, framework.CAPINamespace)
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

func verifyMachineSynchronizedCondition(ctx context.Context, cl client.Client, machineName string, auth machinev1beta1.MachineAuthority) {
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

func verifyMachineSynchronizedGeneration(ctx context.Context, cl client.Client, machineName string, auth machinev1beta1.MachineAuthority) {
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
			capiMachine, err := framework.GetMachine(cl, machineName, framework.CAPINamespace)
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

func verifyMAPIMachinePausedCondition(ctx context.Context, cl client.Client, machineName string, auth machinev1beta1.MachineAuthority) {
	var conditionMatcher types.GomegaMatcher

	switch auth {
	case machinev1beta1.MachineAuthorityMachineAPI:
		By("Verify the MAPI Machine is Unpaused")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(MAPIPausedCondition)),
			HaveField("Status", Equal(corev1.ConditionFalse)),
			HaveField("Reason", Equal("AuthoritativeAPIMachineAPI")),
			HaveField("Message", ContainSubstring("MachineAPI")),
		)
	case machinev1beta1.MachineAuthorityClusterAPI:
		By("Verify the MAPI Machine is Paused")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(MAPIPausedCondition)),
			HaveField("Status", Equal(corev1.ConditionTrue)),
			HaveField("Reason", Equal("AuthoritativeAPINotMachineAPI")),
			HaveField("Message", ContainSubstring("ClusterAPI")),
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

func verifyCAPIMachinePausedCondition(cl client.Client, machineName string, auth machinev1beta1.MachineAuthority) {
	var conditionMatcher types.GomegaMatcher

	switch auth {
	case machinev1beta1.MachineAuthorityClusterAPI:
		By("Verify the CAPI Machine is Unpaused")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(CAPIPausedCondition)),
			HaveField("Status", Equal(metav1.ConditionFalse)),
			HaveField("Reason", Equal("NotPaused")),
		)
	case machinev1beta1.MachineAuthorityMachineAPI:
		By("Verify the CAPI Machine is Paused")
		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(CAPIPausedCondition)),
			HaveField("Status", Equal(metav1.ConditionTrue)),
			HaveField("Reason", Equal("Paused")),
		)
	default:
		Fail(fmt.Sprintf("unknown authoritative API type: %v", auth))
	}

	Eventually(func() []metav1.Condition {
		capiMachine, err := framework.GetMachine(cl, machineName, framework.CAPINamespace)
		if err != nil {
			return nil
		}
		return capiMachine.Status.V1Beta2.Conditions
	}, framework.WaitMedium, framework.RetryMedium).Should(
		ContainElement(conditionMatcher),
		fmt.Sprintf("Expected paused condition for %s not found", auth),
	)
}

func cleanupMachineResources(ctx context.Context, cl client.Client, mapiMachineName string) {
	mapiMachine, err := mapiframework.GetMachine(cl, mapiMachineName)
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
}
