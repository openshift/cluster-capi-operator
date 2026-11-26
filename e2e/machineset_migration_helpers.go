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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	yaml "sigs.k8s.io/yaml"
)

// createCAPIMachineSet creates a CAPI MachineSet with an infra MachineTemplate and waits for it to be ready.
func createCAPIMachineSet(ctx context.Context, cl client.Client, replicas int32, machineSetName string, instanceType string) *clusterv1.MachineSet {
	GinkgoHelper()

	By(fmt.Sprintf("Creating CAPI MachineSet %s with %d replicas", machineSetName, replicas))

	mapiMS := capiframework.GetFirstMAPIMachineSet(ctx, cl)

	var objectRefKind string
	var infraMachineTemplate client.Object
	switch platform {
	case configv1.AWSPlatformType:
		infraMachineTemplate = createAWSMachineTemplateFromMachineSet(mapiMS, machineSetName, instanceType)
		objectRefKind = awsMachineTemplateKind
	case configv1.OpenStackPlatformType:
		infraMachineTemplate = createOpenStackMachineTemplateFromMachineSet(mapiMS, machineSetName)
		objectRefKind = openStackMachineTemplateKind
	default:
		Fail(fmt.Sprintf("Platform %q is not supported yet", platform))
	}

	machineSet := capiframework.CreateMachineSet(ctx, cl, capiframework.NewMachineSetParams(
		machineSetName,
		clusterName,
		"",
		replicas,
		clusterv1.ContractVersionedObjectReference{
			Kind:     objectRefKind,
			APIGroup: infraAPIGroup,
			Name:     machineSetName,
		},
		"worker-user-data",
	))

	trackResource(infraMachineTemplate)
	trackResource(machineSet)
	// The sync controller will create a mirrored MAPI MachineSet with the same name.
	trackResource(&mapiv1beta1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineSetName,
			Namespace: capiframework.MAPINamespace,
		},
	})

	capiframework.WaitForMachineSet(ctx, cl, machineSet.Name, machineSet.Namespace, capiframework.WaitLong)

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

	trackResource(mapiMachineSet)

	capiMachineSet := &clusterv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineSetName,
			Namespace: capiframework.CAPINamespace,
		},
	}
	Eventually(komega.Get(capiMachineSet), capiframework.WaitShort, capiframework.RetryShort).Should(
		Succeed(), "Should have mirror CAPI MachineSet created within 1 minute")

	trackResource(capiMachineSet)

	switch machineAuthority {
	case mapiv1beta1.MachineAuthorityMachineAPI:
		mapiframework.WaitForMachineSet(ctx, cl, machineSetName)
	case mapiv1beta1.MachineAuthorityClusterAPI:
		capiframework.WaitForMachineSet(ctx, cl, machineSetName, capiframework.CAPINamespace, capiframework.WaitLong)
	}

	return mapiMachineSet
}

// switchMachineSetAuthoritativeAPI updates the authoritativeAPI fields of a MAPI MachineSet.
func switchMachineSetAuthoritativeAPI(mapiMachineSet *mapiv1beta1.MachineSet, machineSetAuthority mapiv1beta1.MachineAuthority) {
	GinkgoHelper()

	By(fmt.Sprintf("Switching MachineSet %s AuthoritativeAPI to spec.authoritativeAPI: %s", mapiMachineSet.Name, machineSetAuthority))
	Eventually(komega.Update(mapiMachineSet, func() {
		mapiMachineSet.Spec.AuthoritativeAPI = machineSetAuthority
	}), capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Failed to update MachineSet %s AuthoritativeAPI", mapiMachineSet.Name)
}

// switchMachineSetTemplateAuthoritativeAPI updates the authoritativeAPI fields of a MAPI MachineSet's template.
func switchMachineSetTemplateAuthoritativeAPI(mapiMachineSet *mapiv1beta1.MachineSet, machineAuthority mapiv1beta1.MachineAuthority) {
	GinkgoHelper()

	By(fmt.Sprintf("Switching MachineSet %s spec.template.spec.authoritativeAPI: %s", mapiMachineSet.Name, machineAuthority))
	Eventually(komega.Update(mapiMachineSet, func() {
		mapiMachineSet.Spec.Template.Spec.AuthoritativeAPI = machineAuthority
	}), capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Failed to update MachineSet %s template AuthoritativeAPI", mapiMachineSet.Name)
}

// verifyMachineSetAuthoritative verifies that a MAPI MachineSet's status.authoritativeAPI matches the expected authority.
func verifyMachineSetAuthoritative(mapiMachineSet *mapiv1beta1.MachineSet, authority mapiv1beta1.MachineAuthority) {
	GinkgoHelper()

	By(fmt.Sprintf("Verifying the MachineSet authoritative is %s", authority))

	Eventually(komega.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		HaveField("Status.AuthoritativeAPI", Equal(authority)),
		"MachineSet %s: wanted AuthoritativeAPI %s", mapiMachineSet.Name, authority,
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

		Eventually(func(g Gomega) {
			g.Expect(komega.Get(ms)()).To(Succeed())
			g.Expect(ms.Status.Conditions).To(ContainElement(conditionMatcher),
				"MAPI MachineSet %s: wanted Paused condition for authority %s, got conditions: %s",
				ms.Name, authority, summarizeMAPIConditions(ms.Status.Conditions))
		}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed())

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

		Eventually(func(g Gomega) {
			g.Expect(komega.Get(ms)()).To(Succeed())
			g.Expect(ms.Status.Conditions).To(ContainElement(conditionMatcher),
				"CAPI MachineSet %s: wanted Paused condition for authority %s, got conditions: %s",
				ms.Name, authority, summarizeV1Beta2Conditions(ms.Status.Conditions))
		}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed())

	default:
		Fail(fmt.Sprintf("unsupported MachineSet type: %T", machineSet))
	}
}

// verifyMachinesetReplicas verifies that a MachineSet (MAPI or CAPI) has the expected number of replicas in its status.
func verifyMachinesetReplicas(machineSet client.Object, replicas int) {
	GinkgoHelper()

	Expect(machineSet).NotTo(BeNil(), "MachineSet parameter cannot be nil")
	Expect(machineSet.GetName()).NotTo(BeEmpty(), "MachineSet name cannot be empty")

	switch ms := machineSet.(type) {
	case *mapiv1beta1.MachineSet:
		By(fmt.Sprintf("Verifying MAPI MachineSet status.Replicas is %d", replicas))
		Eventually(komega.Object(ms), capiframework.WaitLong, capiframework.RetryMedium).Should(
			HaveField("Status.Replicas", Equal(int32(replicas))),
			"Should have MAPI MachineSet %q replicas status eventually be %d", ms.Name, replicas)
	case *clusterv1.MachineSet:
		By(fmt.Sprintf("Verifying CAPI MachineSet status.Replicas is %d", replicas))
		Eventually(komega.Object(ms), capiframework.WaitLong, capiframework.RetryMedium).Should(
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

	Eventually(func(g Gomega) {
		g.Expect(komega.Get(mapiMachineSet)()).To(Succeed())
		g.Expect(mapiMachineSet.Status.Conditions).To(
			ContainElement(SatisfyAll(
				HaveField("Type", Equal(SynchronizedCondition)),
				HaveField("Status", Equal(corev1.ConditionTrue)),
				HaveField("Reason", Equal("ResourceSynchronized")),
				HaveField("Message", Equal(expectedMessage)),
			)),
			"MachineSet %s: wanted Synchronized condition for authority %s, got conditions: %s",
			mapiMachineSet.Name, authority, summarizeMAPIConditions(mapiMachineSet.Status.Conditions),
		)
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed())
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
// Returns nil if the ProviderSpec is nil or unmarshalling fails, so it is safe
// to use inside WithTransform (no Expect/panic in retry loops).
func getAWSProviderSpecFromMachineSet(mapiMachineSet *mapiv1beta1.MachineSet) *mapiv1beta1.AWSMachineProviderConfig {
	if mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value == nil {
		return nil
	}

	providerSpec := &mapiv1beta1.AWSMachineProviderConfig{}
	if err := yaml.Unmarshal(mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, providerSpec); err != nil {
		GinkgoWriter.Printf("Warning: failed to unmarshal ProviderSpec for MachineSet %s: %v\n", mapiMachineSet.Name, err)
		return nil
	}

	return providerSpec
}

func updateInfraMachineSetProviderSpec(ctx context.Context, cl client.Client, mapiMachineSet *mapiv1beta1.MachineSet, updateFunc func(*mapiv1beta1.MachineSet) ([]byte, error)) error {
	GinkgoHelper()

	By(fmt.Sprintf("Updating MachineSet %s providerSpec", mapiMachineSet.Name))
	rawProviderSpec, err := updateFunc(mapiMachineSet)
	Expect(err).ToNot(HaveOccurred(), "failed to marshal updated provider spec")

	original := mapiMachineSet.DeepCopy()
	mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value.Raw = rawProviderSpec

	patch := client.MergeFrom(original)

	return cl.Patch(ctx, mapiMachineSet, patch)
}

// waitForMAPIMachineSetMirrors waits for the corresponding CAPI MachineSet and infra MachineTemplate mirrors to be created for a MAPI MachineSet.
func waitForMAPIMachineSetMirrors(machineSetNameMAPI string) (*clusterv1.MachineSet, client.Object) {
	GinkgoHelper()

	By(fmt.Sprintf("Verifying there is a CAPI MachineSet mirror and infra MachineTemplate for MAPI MachineSet %s", machineSetNameMAPI))

	// Direct Get instead of capiframework.GetMachineSet to avoid nested Eventually.
	capiMachineSet := &clusterv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineSetNameMAPI,
			Namespace: capiframework.CAPINamespace,
		},
	}
	Eventually(komega.Get(capiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		Succeed(), "Should have CAPI MachineSet %s/%s exist", capiframework.CAPINamespace, machineSetNameMAPI)

	capiMachineSet, infraMachineTemplate := waitForMachineSetInfraMachineTemplate(machineSetNameMAPI)

	return capiMachineSet, infraMachineTemplate
}

// waitForCAPIMachineSetMirror waits for a CAPI MachineSet mirror to be created for a MAPI MachineSet.
func waitForCAPIMachineSetMirror(machineName string) *clusterv1.MachineSet {
	GinkgoHelper()

	By(fmt.Sprintf("Verifying there is a CAPI MachineSet mirror for MAPI MachineSet %s", machineName))

	// Direct Get instead of capiframework.GetMachineSet to avoid nested Eventually.
	capiMachineSet := &clusterv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineName,
			Namespace: capiframework.CAPINamespace,
		},
	}
	Eventually(komega.Get(capiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		Succeed(), "Should have CAPI MachineSet %s/%s exist", capiframework.CAPINamespace, machineName)

	return capiMachineSet
}

// waitForInfraMachineTemplate waits for the infra MachineTemplate referenced by the CAPI MachineSet to exist.
func waitForInfraMachineTemplate(machineSetName string) client.Object {
	GinkgoHelper()

	By(fmt.Sprintf("Verifying there is an infra MachineTemplate referenced by CAPI MachineSet %s", machineSetName))

	_, infraMachineTemplate := waitForMachineSetInfraMachineTemplate(machineSetName)

	return infraMachineTemplate
}

// waitForMachineSetInfraMachineTemplate waits for the CAPI MachineSet and the infra MachineTemplate its
// infrastructureRef points at, and returns both.
//
// The MachineSet is re-read on every attempt so that a reference replaced while this is polling is
// followed to the template that is actually in use, instead of failing on, or arbitrarily picking
// between, the superseded and the current template.
func waitForMachineSetInfraMachineTemplate(machineSetName string) (*clusterv1.MachineSet, client.Object) {
	GinkgoHelper()

	var (
		capiMachineSet       *clusterv1.MachineSet
		infraMachineTemplate client.Object
	)

	Eventually(func() error {
		var err error
		capiMachineSet, infraMachineTemplate, err = resolveInfraMachineTemplateForMachineSet(ctx, cl, machineSetName)

		return err
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(),
		"Should have the infra MachineTemplate referenced by CAPI MachineSet %s/%s exist", capiframework.CAPINamespace, machineSetName)

	return capiMachineSet, infraMachineTemplate
}

// createAWSMachineTemplate creates a new AWSMachineTemplate with an optional update function to modify the spec.
func createAWSMachineTemplate(ctx context.Context, cl client.Client, originalName string, updateFunc func(*awsv1.AWSMachineSpec)) client.Object {
	GinkgoHelper()

	By("Creating a new awsMachineTemplate")

	return createInfraMachineTemplate(ctx, cl, originalName, func(mapiMS *mapiv1beta1.MachineSet) client.Object {
		mapiProviderSpec, err := mapi2capi.AWSProviderSpecFromRawExtension(mapiMS.Spec.Template.Spec.ProviderSpec.Value)
		Expect(err).ToNot(HaveOccurred(), "should not fail decoding MAPI provider spec")
		createAWSClient(mapiProviderSpec.Placement.Region)

		newTemplate := newAWSMachineTemplate(mapiMS, infra)
		if updateFunc != nil {
			updateFunc(&newTemplate.Spec.Template.Spec)
		}

		return newTemplate
	})
}

// createOpenStackMachineTemplate creates a new OpenStackMachineTemplate with an optional update function to modify the spec.
func createOpenStackMachineTemplate(ctx context.Context, cl client.Client, originalName string, updateFunc func(*openstackv1.OpenStackMachineSpec)) client.Object {
	GinkgoHelper()

	By("Creating a new openstackMachineTemplate")

	return createInfraMachineTemplate(ctx, cl, originalName, func(mapiMS *mapiv1beta1.MachineSet) client.Object {
		newTemplate := newOpenStackMachineTemplate(mapiMS, infra)
		if updateFunc != nil {
			updateFunc(&newTemplate.Spec.Template.Spec)
		}

		return newTemplate
	})
}

// createInfraMachineTemplate creates a new infra MachineTemplate with template generation function based on an existing MAPI machine set.
func createInfraMachineTemplate(ctx context.Context, cl client.Client, originalName string, generateTemplateFunc func(mapiMS *mapiv1beta1.MachineSet) client.Object) client.Object {
	GinkgoHelper()

	newTemplate := generateTemplateFunc(capiframework.GetFirstMAPIMachineSet(ctx, cl))
	newTemplate.SetName("new-" + originalName)

	Eventually(func() error { return cl.Create(ctx, newTemplate) }, capiframework.WaitMedium, capiframework.RetryMedium).Should(
		Succeed(), "Failed to create a new infra MachineTemplate %s", newTemplate.GetName())

	trackResource(newTemplate)

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

// cleanupMachineSetTestResources deletes MAPI MachineSets, CAPI MachineSets, and infra MachineTemplates created during tests.
func cleanupMachineSetTestResources(ctx context.Context, cl client.Client, capiMachineSets []*clusterv1.MachineSet, infraMachineTemplates []client.Object, mapiMachineSets []*mapiv1beta1.MachineSet) {
	GinkgoHelper()

	cleanupCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	for _, ms := range mapiMachineSets {
		if ms == nil {
			continue
		}

		By(fmt.Sprintf("Deleting MAPI MachineSet %s", ms.Name))

		var notFound bool

		Eventually(func() error {
			err := cl.Delete(cleanupCtx, ms)
			if apierrors.IsNotFound(err) {
				notFound = true
				return nil
			}

			return err
		}, time.Minute, capiframework.RetryShort).Should(Succeed(),
			"cleanup: delete MAPI MachineSet %s", ms.Name)

		if !notFound {
			mapiframework.WaitForMachineSetsDeleted(cleanupCtx, cl, ms)
		}
	}

	for _, ms := range capiMachineSets {
		if ms == nil {
			continue
		}

		By(fmt.Sprintf("Deleting CAPI MachineSet %s", ms.Name))
		capiframework.DeleteMachineSets(cleanupCtx, cl, ms)
		capiframework.WaitForMachineSetsDeleted(ms)
	}

	for _, template := range infraMachineTemplates {
		if template == nil {
			continue
		}

		By(fmt.Sprintf("Deleting infra MachineTemplate %s", template.GetName()))
		deleteInfraMachineTemplates(cleanupCtx, cl, template)
	}
}

// deleteInfraMachineTemplates deletes the specified infra MachineTemplates.
func deleteInfraMachineTemplates(ctx context.Context, cl client.Client, templates ...client.Object) {
	GinkgoHelper()

	for _, template := range templates {
		if template == nil {
			continue
		}

		By(fmt.Sprintf("Deleting infra MachineTemplate %q", template.GetName()))
		Eventually(func() error {
			return cl.Delete(ctx, template)
		}, time.Minute, capiframework.RetryShort).Should(SatisfyAny(
			Succeed(),
			WithTransform(apierrors.IsNotFound, BeTrue()),
		), "Delete infra MachineTemplate %s/%s should succeed, or infra MachineTemplate should not be found.",
			template.GetNamespace(), template.GetName())
	}
}

// resolveInfraMachineTemplateForMachineSet reads the Cluster API MachineSet and returns it together
// with the infra MachineTemplate its spec.template.spec.infrastructureRef currently points at.
//
// Templates are immutable, so a controller that rolls the infrastructure template creates a new
// object and repoints the reference, leaving more than one template whose name starts with the
// MachineSet name. Both the MachineSet and the template are read here so that callers retrying
// this function observe the reference as it is now rather than a stale or arbitrary template.
func resolveInfraMachineTemplateForMachineSet(ctx context.Context, cl client.Client, machineSetName string) (*clusterv1.MachineSet, client.Object, error) {
	if machineSetName == "" {
		return nil, nil, fmt.Errorf("machineSet name cannot be empty")
	}

	namespace := capiframework.CAPINamespace

	machineSet := &clusterv1.MachineSet{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: machineSetName}, machineSet); err != nil {
		return nil, nil, fmt.Errorf("get Cluster API MachineSet %s/%s: %w", namespace, machineSetName, err)
	}

	infraRef := machineSet.Spec.Template.Spec.InfrastructureRef
	if infraRef.Name == "" {
		return machineSet, nil, fmt.Errorf("no infrastructureRef name on Cluster API MachineSet %s/%s", namespace, machineSetName)
	}

	var template client.Object
	switch infraRef.Kind {
	case awsMachineTemplateKind:
		template = &awsv1.AWSMachineTemplate{}
	case openStackMachineTemplateKind:
		template = &openstackv1.OpenStackMachineTemplate{}
	default:
		return nil, nil, fmt.Errorf("unexpected infrastructure kind %q on Cluster API MachineSet %s/%s", infraRef.Kind, namespace, machineSetName)
	}

	if err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: infraRef.Name}, template); err != nil {
		return nil, nil, fmt.Errorf("get infra MachineTemplate %s/%s referenced by Cluster API MachineSet %s: %w", namespace, infraRef.Name, machineSetName, err)
	}

	return machineSet, template, nil
}
