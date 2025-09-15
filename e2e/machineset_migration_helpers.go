package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

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
	yaml "sigs.k8s.io/yaml"
)

func createMAPIMachineSetWithAuthoritativeAPI(ctx context.Context, cl client.Client, replicas int, machineSetName string, machineSetAuthority machinev1beta1.MachineAuthority, machineAuthority machinev1beta1.MachineAuthority) *machinev1beta1.MachineSet {
	By(fmt.Sprintf("Creating MAPI MachineSet with spec.authoritativeAPI: %s, spec.template.spec.authoritativeAPI: %s, replicas=%d", machineSetAuthority, machineAuthority, replicas))
	machineSetParams := mapiframework.BuildMachineSetParams(ctx, cl, replicas)
	machineSetParams.Name = machineSetName
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
		Succeed(), "Mirror CAPI MachineSet should be created within 1 minute")

	switch machineAuthority {
	case machinev1beta1.MachineAuthorityMachineAPI:
		mapiframework.WaitForMachineSet(ctx, cl, machineSetName)
	case machinev1beta1.MachineAuthorityClusterAPI:
		capiframework.WaitForMachineSet(cl, machineSetName, capiframework.CAPINamespace)
	}
	return mapiMachineSet
}

func createCAPIMachineSet(ctx context.Context, cl client.Client, replicas int32, machineSetName string, instanceType string) *clusterv1.MachineSet {
	By(fmt.Sprintf("Creating CAPI MachineSet %s with %d replicas", machineSetName, replicas))

	_, mapiDefaultProviderSpec := getDefaultAWSMAPIProviderSpec()
	createAWSClient(mapiDefaultProviderSpec.Placement.Region)
	awsMachineTemplate := newAWSMachineTemplate(mapiDefaultProviderSpec)
	awsMachineTemplate.Name = machineSetName
	if instanceType != "" {
		awsMachineTemplate.Spec.Template.Spec.InstanceType = instanceType
	}

	Eventually(cl.Create(ctx, awsMachineTemplate), capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Failed to create a new awsMachineTemplate %s", awsMachineTemplate.Name)

	machineSet := capiframework.CreateMachineSet(cl, capiframework.NewMachineSetParams(
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

	capiframework.WaitForMachineSet(cl, machineSet.Name, machineSet.Namespace)
	return machineSet
}

func verifyMachineSetAuthoritative(mapiMachineSet *machinev1beta1.MachineSet, authority machinev1beta1.MachineAuthority) {
	By(fmt.Sprintf("Verifying the MachineSet authoritative is %s", authority))
	Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		HaveField("Status.AuthoritativeAPI", Equal(authority)),
		fmt.Sprintf("Expected MachineSet with correct status.AuthoritativeAPI %s", authority),
	)
}

func verifyMachineSetSynchronizedCondition(mapiMachineSet *machinev1beta1.MachineSet, authority machinev1beta1.MachineAuthority) {
	By("Verifying the MAPI MachineSet Synchronized condition is True")
	var expectedMessage string

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

func verifyMAPIMachineSetPausedCondition(mapiMachineSet *machinev1beta1.MachineSet, authority machinev1beta1.MachineAuthority) {
	var conditionMatcher types.GomegaMatcher

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

func verifyCAPIMachineSetPausedCondition(capiMachineSet *clusterv1.MachineSet, authority machinev1beta1.MachineAuthority) {
	var conditionMatcher types.GomegaMatcher

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

func verifyMAPIMachineSetHasCAPIMirror(cl client.Client, machineSetNameMAPI string) (*clusterv1.MachineSet, *awsv1.AWSMachineTemplate) {
	By("Checking MAPI MachineSet has a CAPI MachineSet mirror")
	var err error
	var capiMachineSet *clusterv1.MachineSet
	var awsMachineTemplate *awsv1.AWSMachineTemplate

	Eventually(func() error {
		capiMachineSet = capiframework.GetMachineSet(cl, machineSetNameMAPI, capiframework.CAPINamespace)
		if capiMachineSet == nil {
			return fmt.Errorf("CAPI MachineSet %s/%s not found", capiframework.CAPINamespace, machineSetNameMAPI)
		}
		return nil
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "CAPI MachineSet should exist")

	Eventually(func() error {
		awsMachineTemplate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, machineSetNameMAPI, capiframework.CAPINamespace)
		return err
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "awsMachineTemplate should exist")

	return capiMachineSet, awsMachineTemplate
}

func verifyMAPIMachinesetReplicas(mapiMachineSet *machinev1beta1.MachineSet, replicas int) {
	By(fmt.Sprintf("Verifying MAPI MachineSet status.Replicas is %d", replicas))
	Eventually(komega.Object(mapiMachineSet), capiframework.WaitLong, capiframework.RetryLong).Should(
		HaveField("Status.Replicas", HaveValue(Equal(int32(replicas)))),
		"MAPI MachineSet %q replicas status should eventually be %d", mapiMachineSet.Name, replicas)
}

func verifyCAPIMachinesetReplicas(capiMachineSet *clusterv1.MachineSet, replicas int) {
	By(fmt.Sprintf("Verifying CAPI MachineSet status.Replicas is %d", replicas))
	Eventually(komega.Object(capiMachineSet), capiframework.WaitLong, capiframework.RetryLong).Should(
		HaveField("Status.Replicas", HaveValue(Equal(int32(replicas)))),
		"CAPI MachineSet %q replicas status should eventually be %d", capiMachineSet.Name, replicas)
}

func verifyAWSMachineTemplateDeleted(awsMachineTemplateName string) {
	By(fmt.Sprintf("Verifying the AWSMachineTemplate %s is removed", awsMachineTemplateName))
	Eventually(komega.Get(&awsv1.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      awsMachineTemplateName,
			Namespace: capiframework.CAPINamespace,
		},
	}), time.Minute).Should(WithTransform(apierrors.IsNotFound, BeTrue()))
}

func switchMachineSetAuthoritativeAPI(mapiMachineSet *machinev1beta1.MachineSet, machineSetAuthority machinev1beta1.MachineAuthority, machineAuthority machinev1beta1.MachineAuthority) {
	By(fmt.Sprintf("Switching MachineSet %s AuthoritativeAPI to spec.authoritativeAPI: %s, spec.template.spec.authoritativeAPI: %s", mapiMachineSet.Name, machineSetAuthority, machineAuthority))
	Eventually(komega.Update(mapiMachineSet, func() {
		mapiMachineSet.Spec.AuthoritativeAPI = machineSetAuthority
		mapiMachineSet.Spec.Template.Spec.AuthoritativeAPI = machineAuthority
	}), capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Failed to update MachineSet %s AuthoritativeAPI", mapiMachineSet.Name)
}

func getAWSProviderSpecFromMachineSet(ctx context.Context, cl client.Client, machineSetName string) *machinev1beta1.AWSMachineProviderConfig {
	currentMachineSet, _ := mapiframework.GetMachineSet(ctx, cl, machineSetName)
	Expect(currentMachineSet.Spec.Template.Spec.ProviderSpec.Value).ToNot(BeNil())

	providerSpec := &machinev1beta1.AWSMachineProviderConfig{}
	Expect(yaml.Unmarshal(currentMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, providerSpec)).To(Succeed())

	return providerSpec
}

func updateAWSMachineSetProviderSpec(ctx context.Context, cl client.Client, mapiMachineSet *machinev1beta1.MachineSet, updateFunc func(*machinev1beta1.AWSMachineProviderConfig)) {
	By(fmt.Sprintf("Updating MachineSet %s providerSpec", mapiMachineSet.Name))
	providerSpec := getAWSProviderSpecFromMachineSet(ctx, cl, mapiMachineSet.Name)

	updateFunc(providerSpec)

	rawProviderSpec, err := json.Marshal(providerSpec)
	Expect(err).ToNot(HaveOccurred(), "failed to marshal updated provider spec")

	original := mapiMachineSet.DeepCopy()
	mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw = rawProviderSpec

	patch := client.MergeFrom(original)
	Expect(cl.Patch(ctx, mapiMachineSet, patch)).To(Succeed(), "failed to patch MachineSet provider spec")
}

func getMAPIMachineSetInstanceType(ctx context.Context, cl client.Client, mapiMachineSet *machinev1beta1.MachineSet) string {
	providerSpec := getAWSProviderSpecFromMachineSet(ctx, cl, mapiMachineSet.Name)

	return providerSpec.InstanceType
}

func verifyMAPIMachineSetInstanceType(ctx context.Context, cl client.Client, mapiMachineSet *machinev1beta1.MachineSet, expectedInstanceType string) {
	By(fmt.Sprintf("Verifying MAPI MachineSet %s has instanceType = %s", mapiMachineSet.Name, expectedInstanceType))

	Eventually(func() string {
		return getMAPIMachineSetInstanceType(ctx, cl, mapiMachineSet)
	}, capiframework.WaitMedium, capiframework.RetryShort).Should(
		Equal(expectedInstanceType),
		"MachineSet %s providerSpec.instanceType should be %s",
		mapiMachineSet.Name, expectedInstanceType,
	)
}

func waitForCAPIMachineSetMirror(cl client.Client, machineName string) *clusterv1.MachineSet {
	By(fmt.Sprintf("Verifying there is a CAPI MachineSet mirror for  MAPI MachineSet %s", machineName))
	var capiMachineSet *clusterv1.MachineSet
	Eventually(func() error {
		capiMachineSet = capiframework.GetMachineSet(cl, machineName, capiframework.CAPINamespace)
		if capiMachineSet == nil {
			return fmt.Errorf("CAPI MachineSet %s/%s not found", capiframework.CAPINamespace, machineName)
		}
		return nil
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "CAPI MachineSet %s/%s should exist", capiframework.CAPINamespace, machineName)
	return capiMachineSet
}

func waitForAWSMachineTemplate(cl client.Client, prefix string) *awsv1.AWSMachineTemplate {
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
		"AWSMachineTemplate with prefix %s should exist", prefix)
	return awsMachineTemplate
}

func createAWSMachineTemplateWithInstanceType(ctx context.Context, cl client.Client, originalName, instanceType string) *awsv1.AWSMachineTemplate {
	By(fmt.Sprintf("Creating a new awsMachineTemplate with spec.instanceType=%s", instanceType))

	_, mapiDefaultProviderSpec := getDefaultAWSMAPIProviderSpec()
	createAWSClient(mapiDefaultProviderSpec.Placement.Region)

	newTemplate := newAWSMachineTemplate(mapiDefaultProviderSpec)
	newTemplate.Name = "new-" + originalName
	newTemplate.Spec.Template.Spec.InstanceType = instanceType

	Eventually(cl.Create(ctx, newTemplate), capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Failed to create a new awsMachineTemplate %s", newTemplate.Name)

	return newTemplate
}

func updateCAPIMachineSetInfraTemplate(capiMachineSet *clusterv1.MachineSet, newInfraTemplateName string) {
	By(fmt.Sprintf("Updating CAPI MachineSet %s to point to new InfraTemplate %s", capiMachineSet.Name, newInfraTemplateName))

	Eventually(komega.Update(capiMachineSet, func() {
		capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name = newInfraTemplateName
	}), capiframework.WaitShort, capiframework.RetryShort).Should(
		Succeed(),
		"Failed to update CAPI MachineSet %s to point to new InfraTemplate %s",
		capiMachineSet.Name, newInfraTemplateName,
	)
}

func cleanupMachineSetTestResources(ctx context.Context, cl client.Client, capiMachineSets []*clusterv1.MachineSet, awsMachineTemplates []*awsv1.AWSMachineTemplate, mapiMachineSets []*machinev1beta1.MachineSet) {
	for _, ms := range capiMachineSets {
		if ms == nil {
			continue
		}
		By(fmt.Sprintf("Deleting CAPI MachineSet %s", ms.Name))
		capiframework.DeleteMachineSets(cl, ms)
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
		capiframework.DeleteAWSMachineTemplates(cl, template)
	}
}
