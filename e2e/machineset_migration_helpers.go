package e2e

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	yaml "sigs.k8s.io/yaml"
)

// createCAPIMachineSet creates a CAPI MachineSet with an AWSMachineTemplate and waits for it to be ready.
func createCAPIMachineSet(ctx context.Context, cl client.Client, replicas int32, machineSetName string, instanceType string) *clusterv1.MachineSet {
	GinkgoHelper()
	By(fmt.Sprintf("Creating CAPI MachineSet %s with %d replicas", machineSetName, replicas))
	_, mapiDefaultProviderSpec := getDefaultAWSMAPIProviderSpec()
	createAWSClient(mapiDefaultProviderSpec.Placement.Region)
	awsMachineTemplate := newAWSMachineTemplate(mapiDefaultProviderSpec)
	awsMachineTemplate.Name = machineSetName
	if instanceType != "" {
		awsMachineTemplate.Spec.Template.Spec.InstanceType = instanceType
	}

	Eventually(cl.Create(ctx, awsMachineTemplate), capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Failed to create a new awsMachineTemplate %s", awsMachineTemplate.Name)

	machineSet := capiframework.CreateMachineSet(ctx, cl, capiframework.NewMachineSetParams(
		machineSetName,
		clusterName,
		"",
		replicas,
		clusterv1.ContractVersionedObjectReference{
			Kind:     "AWSMachineTemplate",
			APIGroup: infraAPIGroup,
			Name:     machineSetName,
		},
		"worker-user-data",
	))

	capiframework.WaitForMachineSet(cl, machineSet.Name, machineSet.Namespace)
	return machineSet
}

// createMAPIMachineSetWithAuthoritativeAPI creates a MAPI MachineSet with specified authoritativeAPI and waits for the CAPI mirror to be created.
func createMAPIMachineSetWithAuthoritativeAPI(ctx context.Context, cl client.Client, replicas int, machineSetName string, machineSetAuthority mapiv1beta1.MachineAuthority, machineAuthority mapiv1beta1.MachineAuthority) *mapiv1beta1.MachineSet {
	GinkgoHelper()
	By(fmt.Sprintf("Creating MAPI MachineSet with spec.authoritativeAPI: %s, spec.template.spec.authoritativeAPI: %s, replicas=%d", machineSetAuthority, machineAuthority, replicas))
	machineSetParams := mapiframework.BuildMachineSetParams(ctx, cl, replicas)
	machineSetParams.Name = machineSetName
	machineSetParams.Labels[mapiframework.MachineSetKey] = machineSetName
	machineSetParams.MachinesetAuthoritativeAPI = machineSetAuthority
	machineSetParams.MachineAuthoritativeAPI = machineAuthority
	// Remove taints as CAPI MachineSets don't support them yet. This is a known limitation tracked in https://issues.redhat.com/browse/OCPCLOUD-2861
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
		Succeed(), "Should have mirror CAPI MachineSet created within 1 minute")

	switch machineAuthority {
	case mapiv1beta1.MachineAuthorityMachineAPI:
		mapiframework.WaitForMachineSet(ctx, cl, machineSetName)
	case mapiv1beta1.MachineAuthorityClusterAPI:
		capiframework.WaitForMachineSet(cl, machineSetName, capiframework.CAPINamespace)
	}
	return mapiMachineSet
}

// switchMachineSetAuthoritativeAPI updates the authoritativeAPI fields of a MAPI MachineSet and its template.
func switchMachineSetAuthoritativeAPI(mapiMachineSet *mapiv1beta1.MachineSet, machineSetAuthority mapiv1beta1.MachineAuthority, machineAuthority mapiv1beta1.MachineAuthority) {
	GinkgoHelper()
	By(fmt.Sprintf("Switching MachineSet %s AuthoritativeAPI to spec.authoritativeAPI: %s, spec.template.spec.authoritativeAPI: %s", mapiMachineSet.Name, machineSetAuthority, machineAuthority))
	Eventually(komega.Update(mapiMachineSet, func() {
		mapiMachineSet.Spec.AuthoritativeAPI = machineSetAuthority
		mapiMachineSet.Spec.Template.Spec.AuthoritativeAPI = machineAuthority
	}), capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Failed to update MachineSet %s AuthoritativeAPI", mapiMachineSet.Name)
}

// verifyMachineSetAuthoritative verifies that a MAPI MachineSet's status.authoritativeAPI matches the expected authority.
func verifyMachineSetAuthoritative(mapiMachineSet *mapiv1beta1.MachineSet, authority mapiv1beta1.MachineAuthority) {
	GinkgoHelper()
	By(fmt.Sprintf("Verifying the MachineSet authoritative is %s", authority))
	Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		HaveField("Status.AuthoritativeAPI", Equal(authority)),
		fmt.Sprintf("Expected MachineSet with correct status.AuthoritativeAPI %s", authority),
	)
}

// verifyMachineSetPausedCondition verifies the Paused condition of a MachineSet (MAPI or CAPI) based on its authoritative API.
func verifyMachineSetPausedCondition(machineSet client.Object, authority mapiv1beta1.MachineAuthority) {
	GinkgoHelper()
	Expect(machineSet).NotTo(BeNil(), "MachineSet parameter cannot be nil")
	Expect(machineSet.GetName()).NotTo(BeEmpty(), "MachineSet name cannot be empty")
	var conditionMatcher types.GomegaMatcher

	switch ms := machineSet.(type) {
	case *mapiv1beta1.MachineSet:
		// This is a MAPI MachineSet
		switch authority {
		case mapiv1beta1.MachineAuthorityMachineAPI:
			By("Verifying MAPI MachineSet is unpaused")
			conditionMatcher = SatisfyAll(
				HaveField("Type", Equal(MAPIPausedCondition)),
				HaveField("Status", Equal(corev1.ConditionFalse)),
				HaveField("Reason", Equal("AuthoritativeAPIMachineAPI")),
				HaveField("Message", ContainSubstring("MachineAPI")),
			)
		case mapiv1beta1.MachineAuthorityClusterAPI:
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

		Eventually(komega.Object(ms), capiframework.WaitMedium, capiframework.RetryMedium).Should(
			HaveField("Status.Conditions", ContainElement(conditionMatcher)),
			fmt.Sprintf("Should have the expected Paused condition for MAPI MachineSet %s with authority: %s", ms.Name, authority),
		)

	case *clusterv1.MachineSet:
		// This is a CAPI MachineSet
		switch authority {
		case mapiv1beta1.MachineAuthorityClusterAPI:
			By("Verifying CAPI MachineSet is unpaused")
			conditionMatcher = SatisfyAll(
				HaveField("Type", Equal(CAPIPausedCondition)),
				HaveField("Status", Equal(metav1.ConditionFalse)),
				HaveField("Reason", Equal("NotPaused")),
			)
		case mapiv1beta1.MachineAuthorityMachineAPI:
			By("Verifying CAPI MachineSet is paused")
			conditionMatcher = SatisfyAll(
				HaveField("Type", Equal(CAPIPausedCondition)),
				HaveField("Status", Equal(metav1.ConditionTrue)),
				HaveField("Reason", Equal("Paused")),
			)
		default:
			Fail(fmt.Sprintf("unknown authoritativeAPI type: %v", authority))
		}

		Eventually(komega.Object(ms), capiframework.WaitMedium, capiframework.RetryMedium).Should(
			HaveField("Status.Conditions", ContainElement(conditionMatcher)),
			fmt.Sprintf("Should have the expected Paused condition for CAPI MachineSet %s with authority: %s", ms.Name, authority),
		)

	default:
		Fail(fmt.Sprintf("unsupported MachineSet type: %T", machineSet))
	}
}

// verifyMachinesetReplicas verifies that a MachineSet (MAPI or CAPI) has the expected number of replicas in its status.
func verifyMachinesetReplicas(machineSet client.Object, replicas int) {
	GinkgoHelper()
	Expect(machineSet).NotTo(BeNil(), "Machine parameter cannot be nil")
	Expect(machineSet.GetName()).NotTo(BeEmpty(), "Machine name cannot be empty")
	switch ms := machineSet.(type) {
	case *mapiv1beta1.MachineSet:
		By(fmt.Sprintf("Verifying MAPI MachineSet status.Replicas is %d", replicas))
		Eventually(komega.Object(ms), capiframework.WaitOverLong, capiframework.RetryLong).Should(
			HaveField("Status.Replicas", Equal(int32(replicas))),
			"Should have MAPI MachineSet %q replicas status eventually be %d", ms.Name, replicas)
	case *clusterv1.MachineSet:
		By(fmt.Sprintf("Verifying CAPI MachineSet status.Replicas is %d", replicas))
		Eventually(komega.Object(ms), capiframework.WaitOverLong, capiframework.RetryLong).Should(
			HaveField("Status.Replicas", HaveValue(Equal(int32(replicas)))),
			"Should have CAPI MachineSet %q replicas status eventually be %d", ms.Name, replicas)
	default:
		Fail(fmt.Sprintf("unsupported MachineSet type: %T", machineSet))
	}
}

// verifyMAPIMachineSetSynchronizedCondition verifies that a MAPI MachineSet has the Synchronized condition set to True with the correct message.
func verifyMAPIMachineSetSynchronizedCondition(mapiMachineSet *mapiv1beta1.MachineSet, authority mapiv1beta1.MachineAuthority) {
	GinkgoHelper()
	By("Verifying the MAPI MachineSet Synchronized condition is True")
	var expectedMessage string

	switch authority {
	case mapiv1beta1.MachineAuthorityMachineAPI:
		expectedMessage = "Successfully synchronized MAPI MachineSet to CAPI"
	case mapiv1beta1.MachineAuthorityClusterAPI:
		expectedMessage = "Successfully synchronized CAPI MachineSet to MAPI"
	default:
		Fail(fmt.Sprintf("unknown authoritativeAPI type: %v", authority))
	}

	Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		WithTransform(
			func(ms *mapiv1beta1.MachineSet) []mapiv1beta1.Condition {
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
		fmt.Sprintf("Should have Synchronized condition for %s", authority),
	)
}

// verifyMAPIMachineSetProviderSpec verifies that a MAPI MachineSet's providerSpec matches the given Gomega matcher.
func verifyMAPIMachineSetProviderSpec(mapiMachineSet *mapiv1beta1.MachineSet, matcher types.GomegaMatcher) {
	GinkgoHelper()
	By(fmt.Sprintf("Verifying MAPI MachineSet %s ProviderSpec", mapiMachineSet.Name))
	Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryShort).Should(
		WithTransform(getAWSProviderSpecFromMachineSet, matcher),
	)
}

// getAWSProviderSpecFromMachineSet extracts and unmarshals the AWSMachineProviderConfig from a MAPI MachineSet.
func getAWSProviderSpecFromMachineSet(mapiMachineSet *mapiv1beta1.MachineSet) *mapiv1beta1.AWSMachineProviderConfig {
	GinkgoHelper()
	Expect(mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value).ToNot(BeNil())

	providerSpec := &mapiv1beta1.AWSMachineProviderConfig{}
	Expect(yaml.Unmarshal(mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, providerSpec)).To(Succeed())

	return providerSpec
}

// updateAWSMachineSetProviderSpec updates a MAPI MachineSet's AWS providerSpec using the provided update function.
func updateAWSMachineSetProviderSpec(ctx context.Context, cl client.Client, mapiMachineSet *mapiv1beta1.MachineSet, updateFunc func(*mapiv1beta1.AWSMachineProviderConfig)) {
	GinkgoHelper()
	By(fmt.Sprintf("Updating MachineSet %s providerSpec", mapiMachineSet.Name))
	providerSpec := getAWSProviderSpecFromMachineSet(mapiMachineSet)

	updateFunc(providerSpec)

	rawProviderSpec, err := json.Marshal(providerSpec)
	Expect(err).ToNot(HaveOccurred(), "failed to marshal updated provider spec")

	original := mapiMachineSet.DeepCopy()
	mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw = rawProviderSpec

	patch := client.MergeFrom(original)
	Expect(cl.Patch(ctx, mapiMachineSet, patch)).To(Succeed(), "failed to patch MachineSet provider spec")
}

// waitForMAPIMachineSetMirrors waits for the corresponding CAPI MachineSet and AWSMachineTemplate mirrors to be created for a MAPI MachineSet.
func waitForMAPIMachineSetMirrors(cl client.Client, machineSetNameMAPI string) (*clusterv1.MachineSet, *awsv1.AWSMachineTemplate) {
	GinkgoHelper()
	By(fmt.Sprintf("Verifying there is a CAPI MachineSet mirror and AWSMachineTemplate for MAPI MachineSet %s", machineSetNameMAPI))
	var err error
	var capiMachineSet *clusterv1.MachineSet
	var awsMachineTemplate *awsv1.AWSMachineTemplate

	Eventually(func() error {
		capiMachineSet = capiframework.GetMachineSet(cl, machineSetNameMAPI, capiframework.CAPINamespace)
		if capiMachineSet == nil {
			return fmt.Errorf("CAPI MachineSet %s/%s not found", capiframework.CAPINamespace, machineSetNameMAPI)
		}
		return nil
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Should have CAPI MachineSet %s/%s exist", capiframework.CAPINamespace, machineSetNameMAPI)

	Eventually(func() error {
		awsMachineTemplate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, machineSetNameMAPI, capiframework.CAPINamespace)
		return err
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Should have AWSMachineTemplate with prefix %s exist", machineSetNameMAPI)

	return capiMachineSet, awsMachineTemplate
}

// waitForCAPIMachineSetMirror waits for a CAPI MachineSet mirror to be created for a MAPI MachineSet.
func waitForCAPIMachineSetMirror(cl client.Client, machineName string) *clusterv1.MachineSet {
	GinkgoHelper()
	By(fmt.Sprintf("Verifying there is a CAPI MachineSet mirror for MAPI MachineSet %s", machineName))
	var capiMachineSet *clusterv1.MachineSet
	Eventually(func() error {
		capiMachineSet = capiframework.GetMachineSet(cl, machineName, capiframework.CAPINamespace)
		if capiMachineSet == nil {
			return fmt.Errorf("CAPI MachineSet %s/%s not found", capiframework.CAPINamespace, machineName)
		}
		return nil
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Should have CAPI MachineSet %s/%s exist", capiframework.CAPINamespace, machineName)
	return capiMachineSet
}

// waitForAWSMachineTemplate waits for an AWSMachineTemplate with the specified name prefix to be created.
func waitForAWSMachineTemplate(cl client.Client, prefix string) *awsv1.AWSMachineTemplate {
	GinkgoHelper()
	By(fmt.Sprintf("Verifying there is an AWSMachineTemplate with prefix %s", prefix))
	var awsMachineTemplate *awsv1.AWSMachineTemplate
	Eventually(func() error {
		var err error
		awsMachineTemplate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, prefix, capiframework.CAPINamespace)
		if err != nil {
			return fmt.Errorf("failed to get AWSMachineTemplate with prefix %s in %s: %w", prefix, capiframework.CAPINamespace, err)
		}
		return nil
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(),
		"Should have AWSMachineTemplate with prefix %s exist", prefix)
	return awsMachineTemplate
}

// createAWSMachineTemplate creates a new AWSMachineTemplate with an optional update function to modify the spec.
func createAWSMachineTemplate(ctx context.Context, cl client.Client, originalName string, updateFunc func(*awsv1.AWSMachineSpec)) *awsv1.AWSMachineTemplate {
	GinkgoHelper()
	By("Creating a new awsMachineTemplate")
	_, mapiDefaultProviderSpec := getDefaultAWSMAPIProviderSpec()
	createAWSClient(mapiDefaultProviderSpec.Placement.Region)

	newTemplate := newAWSMachineTemplate(mapiDefaultProviderSpec)
	newTemplate.Name = "new-" + originalName

	if updateFunc != nil {
		updateFunc(&newTemplate.Spec.Template.Spec)
	}

	Eventually(cl.Create(ctx, newTemplate), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		Succeed(), "Failed to create a new awsMachineTemplate %s", newTemplate.Name)

	return newTemplate
}

// updateCAPIMachineSetInfraTemplate updates a CAPI MachineSet's infrastructureRef to point to a new template.
func updateCAPIMachineSetInfraTemplate(capiMachineSet *clusterv1.MachineSet, newInfraTemplateName string) {
	GinkgoHelper()
	By(fmt.Sprintf("Updating CAPI MachineSet %s to point to new InfraTemplate %s", capiMachineSet.Name, newInfraTemplateName))
	Eventually(komega.Update(capiMachineSet, func() {
		capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name = newInfraTemplateName
	}), capiframework.WaitShort, capiframework.RetryShort).Should(
		Succeed(),
		"Failed to update CAPI MachineSet %s to point to new InfraTemplate %s",
		capiMachineSet.Name, newInfraTemplateName,
	)
}

// cleanupMachineSetTestResources deletes CAPI MachineSets, MAPI MachineSets, and AWSMachineTemplates created during tests.
func cleanupMachineSetTestResources(ctx context.Context, cl client.Client, capiMachineSets []*clusterv1.MachineSet, awsMachineTemplates []*awsv1.AWSMachineTemplate, mapiMachineSets []*mapiv1beta1.MachineSet) {
	for _, ms := range capiMachineSets {
		if ms == nil {
			continue
		}
		By(fmt.Sprintf("Deleting CAPI MachineSet %s", ms.Name))
		capiframework.DeleteMachineSets(ctx, cl, ms)
		capiframework.WaitForMachineSetsDeleted(cl, ms)
	}

	for _, ms := range mapiMachineSets {
		if ms == nil {
			continue
		}
		By(fmt.Sprintf("Deleting MAPI MachineSet %s", ms.Name))
		mapiframework.DeleteMachineSets(cl, ms)
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
