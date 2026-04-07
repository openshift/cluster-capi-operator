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

package installer

import (
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

const (
	conditionTypeProgressing = "InstallerControllerProgressing"
	conditionTypeDegraded    = "InstallerControllerDegraded"
)

// Provider profile names used across tests.
const (
	providerCore           = "core"
	providerInfra          = "infra"
	providerAddon          = "addon"
	providerCoreV2         = "core-v2"
	providerDupObj         = "dup-obj"
	providerClusterScoped  = "cluster-scoped"
	providerClusterScoped2 = "cluster-scoped-2"
	providerCRD            = "crd-provider"
	providerNamespace      = "namespace-provider"
	providerDeployment     = "deployment-provider"
	providerVAP            = "vap-provider"
	providerIrregularCRD   = "irregular-resource-crd"
	providerAdoptExisting  = "adopt-existing"
	providerAdoptInvalid   = "adopt-invalid"

	coreCMName                = "test-cm-core"
	adoptCMName               = "test-cm-adopt"
	infraCMName               = "test-cm-infra"
	addonCMName               = "test-cm-addon"
	deploymentName            = "test-deployment"
	clusterRoleName           = "test-clusterrole"
	clusterRole2Name          = "test-clusterrole-2"
	testNamespaceName         = "test-related-ns"
	vapName                   = "test-vap"
	providerManyClusterScoped = "many-cluster-scoped"
	providerMixed             = "mixed"
	mixedCMName               = "test-cm-mixed"
)

var (
	testCRDGVK = schema.GroupVersionKind{
		Group:   "test.example.com",
		Version: "v1",
		Kind:    "TestWidget",
	}
	mixedCRDGVK = schema.GroupVersionKind{
		Group:   "test.example.com",
		Version: "v1",
		Kind:    "TestGadget",
	}
	irregularCRDGVK = schema.GroupVersionKind{
		Group:   "security.example.com",
		Version: "v1",
		Kind:    "Policy",
	}
)

var (
	// allProviderProfiles is the common pool of provider profiles, populated in BeforeSuite.
	allProviderProfiles []providerimages.ProviderImageManifests

	// providersByName maps provider name to its profile for easy lookup.
	providersByName map[string]providerimages.ProviderImageManifests
)

// validatingAdmissionPolicyYAML generates a minimal ValidatingAdmissionPolicy YAML.
func validatingAdmissionPolicyYAML(name string) string {
	return fmt.Sprintf(`apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: %s
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
    - apiGroups: ["*"]
      apiVersions: ["*"]
      operations: ["*"]
      resources: ["*"]
  validations:
  - expression: "true"`, name)
}

// setupProviderProfiles creates manifest fixtures and populates allProviderProfiles.
func setupProviderProfiles() {
	GinkgoHelper()

	tb := GinkgoTB()

	// Provider "core": ConfigMap-A with data v1
	core := test.NewProviderImageManifests(tb, providerCore).
		WithManifests(test.ConfigMapYAML(coreCMName, map[string]string{"version": "v1"})).
		Build()

	// Provider "infra": ConfigMap-B with data v1
	infra := test.NewProviderImageManifests(tb, providerInfra).
		WithManifests(test.ConfigMapYAML(infraCMName, map[string]string{"version": "v1"})).
		Build()

	// Provider "addon": ConfigMap-C (no CRDs)
	addon := test.NewProviderImageManifests(tb, providerAddon).
		WithManifests(test.ConfigMapYAML(addonCMName, map[string]string{"addon": "true"})).
		Build()

	// Provider "core-v2": ConfigMap-A with updated data v2
	coreV2 := test.NewProviderImageManifests(tb, providerCoreV2).
		WithManifests(test.ConfigMapYAML(coreCMName, map[string]string{"version": "v2"})).
		Build()

	// Provider "dup-obj": ConfigMap-A (same object as core, for duplicate testing)
	dupObj := test.NewProviderImageManifests(tb, providerDupObj).
		WithManifests(test.ConfigMapYAML(coreCMName, map[string]string{"version": "v1"})).
		Build()

	// Provider "cluster-scoped": a ClusterRole (non-namespaced, produces relatedObjects)
	clusterScoped := test.NewProviderImageManifests(tb, providerClusterScoped).
		WithManifests(test.ClusterRoleYAML(clusterRoleName)).
		Build()

	// Provider "cluster-scoped-2": a second ClusterRole (for multi-object relatedObjects testing)
	clusterScoped2 := test.NewProviderImageManifests(tb, providerClusterScoped2).
		WithManifests(test.ClusterRoleYAML(clusterRole2Name)).
		Build()

	// Provider "crd-provider": a CRD (produces a relatedObjects entry for instance type)
	testCRD := test.GenerateSchemalessSpecStatusCRD(testCRDGVK)
	crdProvider := test.NewProviderImageManifests(tb, providerCRD).
		WithManifests(test.CRDToYAML(testCRD)).
		Build()

	// Provider "namespace-provider": a Namespace object (cluster-scoped)
	nsProvider := test.NewProviderImageManifests(tb, providerNamespace).
		WithManifests(test.NamespaceYAML(testNamespaceName)).
		Build()

	// Provider "deployment-provider": a Deployment (probed for Available condition)
	deploymentProvider := test.NewProviderImageManifests(tb, providerDeployment).
		WithManifests(test.DeploymentYAML(deploymentName)).
		WithInstallOrder(1).
		Build()

	// Provider "mixed": a CRD and a ConfigMap in the same component (exercises both CRD and objects phases)
	mixedCRD := test.GenerateSchemalessSpecStatusCRD(mixedCRDGVK)
	mixed := test.NewProviderImageManifests(tb, providerMixed).
		WithManifests(
			test.CRDToYAML(mixedCRD),
			test.ConfigMapYAML(mixedCMName, map[string]string{"source": "mixed"}),
		).
		Build()

	// Provider "many-cluster-scoped": 10 ClusterRoles for testing relatedObjects ordering
	manyClusterScoped := test.NewProviderImageManifests(tb, providerManyClusterScoped).
		WithManifests(
			test.ClusterRoleYAML("test-cr-1"),
			test.ClusterRoleYAML("test-cr-2"),
			test.ClusterRoleYAML("test-cr-3"),
			test.ClusterRoleYAML("test-cr-4"),
			test.ClusterRoleYAML("test-cr-5"),
			test.ClusterRoleYAML("test-cr-6"),
			test.ClusterRoleYAML("test-cr-7"),
			test.ClusterRoleYAML("test-cr-8"),
			test.ClusterRoleYAML("test-cr-9"),
			test.ClusterRoleYAML("test-cr-10"),
		).
		Build()

	// Provider "vap-provider": ValidatingAdmissionPolicy (demonstrates irregular plural)
	vapProvider := test.NewProviderImageManifests(tb, providerVAP).
		WithManifests(validatingAdmissionPolicyYAML(vapName)).
		Build()

	// Provider "irregular-resource-crd": CRD with irregular resource name
	irregularCRD := test.GenerateSchemalessSpecStatusCRD(irregularCRDGVK)
	irregularCRDProvider := test.NewProviderImageManifests(tb, providerIrregularCRD).
		WithManifests(test.CRDToYAML(irregularCRD)).
		Build()

	// Provider "adopt-existing": ConfigMap with adopt-existing annotation
	adoptExisting := test.NewProviderImageManifests(tb, providerAdoptExisting).
		WithManifests(test.ConfigMapWithAnnotationsYAML(adoptCMName,
			map[string]string{revisiongenerator.AdoptExistingAnnotation: revisiongenerator.AdoptExistingAlways},
			map[string]string{"version": "v1"},
		)).
		Build()

	// Provider "adopt-invalid": ConfigMap with invalid adopt-existing annotation value
	adoptInvalid := test.NewProviderImageManifests(tb, providerAdoptInvalid).
		WithManifests(test.ConfigMapWithAnnotationsYAML(adoptCMName,
			map[string]string{revisiongenerator.AdoptExistingAnnotation: "invalid"},
			map[string]string{"version": "v1"},
		)).
		Build()

	allProviderProfiles = []providerimages.ProviderImageManifests{
		core, infra, addon, coreV2, dupObj,
		clusterScoped, clusterScoped2, crdProvider, nsProvider,
		deploymentProvider, mixed, manyClusterScoped,
		vapProvider, irregularCRDProvider,
		adoptExisting, adoptInvalid,
	}

	providersByName = make(map[string]providerimages.ProviderImageManifests, len(allProviderProfiles))
	for _, p := range allProviderProfiles {
		providersByName[p.Name] = p
	}
}

// addRevision appends a new revision to ClusterAPI.Status.Revisions.
// It uses revisiongenerator to compute the content ID, then writes via status update.
func addRevision(ctx context.Context, providerNames ...string) operatorv1alpha1.ClusterAPIInstallerRevision {
	GinkgoHelper()
	return addRevisionWithOpts(ctx, nil, providerNames...)
}

// addRevisionWithOpts is like addRevision but accepts render options (e.g. WithProxyConfig)
// so the content ID matches what the controller computes.
func addRevisionWithOpts(ctx context.Context, opts []revisiongenerator.RevisionRenderOption, providerNames ...string) operatorv1alpha1.ClusterAPIInstallerRevision {
	GinkgoHelper()

	// Get current ClusterAPI to determine revision index.
	clusterAPI := &operatorv1alpha1.ClusterAPI{}
	Expect(cl.Get(ctx, client.ObjectKey{Name: clusterAPIName}, clusterAPI)).To(Succeed())

	var apiRev operatorv1alpha1.ClusterAPIInstallerRevision

	By("Rendering new revision", func() {
		profiles := lookupProfiles(providerNames...)

		// Render the revision to compute the correct content ID.
		rendered, err := revisiongenerator.NewRenderedRevision(profiles, opts...)
		Expect(err).NotTo(HaveOccurred())

		revisionIndex := int64(len(clusterAPI.Status.Revisions) + 1)

		installerRev, err := rendered.ForInstall("4.18.0-test", revisionIndex)
		Expect(err).NotTo(HaveOccurred())

		apiRev, err = installerRev.ToAPIRevision()
		Expect(err).NotTo(HaveOccurred())
	})

	By("Adding revision to ClusterAPI status", func() {
		clusterAPI.Status.Revisions = append(clusterAPI.Status.Revisions, apiRev)
		clusterAPI.Status.DesiredRevision = apiRev.Name
		Expect(cl.Status().Update(ctx, clusterAPI)).To(Succeed())
	})

	return apiRev
}

func addRevisionAndWaitForSuccess(ctx context.Context, providerNames ...string) {
	GinkgoHelper()

	By("Adding a revision with providers: "+strings.Join(providerNames, ", "), func() {
		revision := addRevision(ctx, providerNames...)
		waitForRevision(ctx, revision.Name)
	})
}

// addEmptyRevision appends a revision with no components.
func addEmptyRevision(ctx context.Context) operatorv1alpha1.ClusterAPIInstallerRevision {
	GinkgoHelper()
	return addRevision(ctx)
}

// lookupProfiles maps provider names to their profiles.
func lookupProfiles(names ...string) []providerimages.ProviderImageManifests {
	GinkgoHelper()

	profiles := make([]providerimages.ProviderImageManifests, len(names))
	for i, name := range names {
		Expect(providersByName).To(HaveKey(name), "unknown provider profile: %s", name)
		profiles[i] = providersByName[name]
	}

	return profiles
}

// createFixtures creates ClusterAPI, ClusterOperator, and Proxy singletons.
func createFixtures(ctx context.Context) {
	GinkgoHelper()

	var cleanupObjs []client.Object //nolint:prealloc

	DeferCleanup(func(ctx context.Context) {
		deleteAndWait(ctx, cleanupObjs...)
	})

	clusterAPIObj := &operatorv1alpha1.ClusterAPI{
		ObjectMeta: metav1.ObjectMeta{Name: clusterAPIName},
		Spec:       &operatorv1alpha1.ClusterAPISpec{},
	}
	Expect(cl.Create(ctx, clusterAPIObj)).To(Succeed())
	cleanupObjs = append(cleanupObjs, clusterAPIObj)

	proxyObj := &configv1.Proxy{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
	}
	Expect(cl.Create(ctx, proxyObj)).To(Succeed())
	cleanupObjs = append(cleanupObjs, proxyObj)

	clusterOperatorObj := &configv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-api"},
	}
	Expect(cl.Create(ctx, clusterOperatorObj)).To(Succeed())
	cleanupObjs = append(cleanupObjs, clusterOperatorObj)
}

// createFixturesWithoutClusterAPI creates only the ClusterOperator and Proxy (not ClusterAPI).
func createFixturesWithoutClusterAPI(ctx context.Context) {
	GinkgoHelper()

	proxyObj := &configv1.Proxy{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
	}
	Expect(cl.Create(ctx, proxyObj)).To(Succeed())

	clusterOperatorObj := &configv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-api"},
	}
	Expect(cl.Create(ctx, clusterOperatorObj)).To(Succeed())

	DeferCleanup(func(ctx context.Context) {
		deleteAndWait(ctx, proxyObj, clusterOperatorObj)
	})
}

// deleteAndWait deletes objects and waits for them to disappear.
// Tolerates conflicts and not-found errors since the controller
// may be concurrently modifying these objects.
func deleteAndWait(ctx context.Context, objs ...client.Object) {
	GinkgoHelper()

	By("Deleting test objects", func() {
		for _, obj := range objs {
			Expect(cl.Delete(ctx, obj)).To(WithTransform(client.IgnoreNotFound, Succeed()))
		}
	})

	By("Waiting for test objects to be not found", func() {
		for _, obj := range objs {
			Eventually(kWithCtx(ctx).Get(obj)).
				WithContext(ctx).
				WithTimeout(defaultEventuallyTimeout).
				Should(test.BeK8SNotFound())
		}
	})
}

// triggerReconcile sends an event on the channel source to trigger reconciliation.
func triggerReconcile() {
	reconcileCh <- event.TypedGenericEvent[client.Object]{
		Object: &operatorv1alpha1.ClusterAPI{
			ObjectMeta: metav1.ObjectMeta{Name: clusterAPIName},
		},
	}
}

// waitForConditions waits for the ClusterOperator to have conditions matching all matchers.
func waitForConditions(ctx context.Context, matchers ...types.GomegaMatcher) {
	GinkgoHelper()

	co := &configv1.ClusterOperator{}
	co.SetName("cluster-api")
	Eventually(kWithCtx(ctx).Object(co)).
		WithContext(ctx).
		WithTimeout(defaultEventuallyTimeout).
		Should(HaveField("Status.Conditions", SatisfyAll(matchers...)))
}

// waitForRelatedObjects waits for the ClusterOperator to have relatedObjects matching all matchers.
func waitForRelatedObjects(ctx context.Context, matchers ...types.GomegaMatcher) {
	GinkgoHelper()

	co := &configv1.ClusterOperator{}
	co.SetName("cluster-api")
	Eventually(kWithCtx(ctx).Object(co)).
		WithContext(ctx).
		WithTimeout(defaultEventuallyTimeout).
		Should(HaveField("Status.RelatedObjects", SatisfyAll(matchers...)))
}

// getRelatedObjects returns the current relatedObjects from the ClusterOperator status.
func getRelatedObjects(ctx context.Context) []configv1.ObjectReference {
	GinkgoHelper()

	co := &configv1.ClusterOperator{}
	co.SetName("cluster-api")
	Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster-api"}, co)).To(Succeed())

	return co.Status.RelatedObjects
}

// makeDeploymentAvailable waits for the named Deployment to exist and then
// sets its Available condition to True. This is a common pattern when a test
// revision includes a Deployment that the controller probes.
func makeDeploymentAvailable(ctx context.Context, name, namespace string) {
	GinkgoHelper()

	deploy := &appsv1.Deployment{}
	deploy.SetName(name)
	deploy.SetNamespace(namespace)

	Eventually(func() error {
		return cl.Get(ctx, client.ObjectKeyFromObject(deploy), deploy)
	}).
		WithContext(ctx).
		WithTimeout(defaultEventuallyTimeout).
		Should(Succeed())

	Eventually(kWithCtx(ctx).UpdateStatus(deploy, func() {
		deploy.Status.Conditions = []appsv1.DeploymentCondition{
			{
				Type:   appsv1.DeploymentAvailable,
				Status: corev1.ConditionTrue,
				Reason: "MinimumReplicasAvailable",
			},
		}
	})).
		WithContext(ctx).
		WithTimeout(defaultEventuallyTimeout).
		Should(Succeed())
}

// waitForRevision waits for the given revision to be applied and the controller
// to report Progressing=False.
func waitForRevision(ctx context.Context, revision operatorv1alpha1.RevisionName) {
	GinkgoHelper()

	clusterAPI := &operatorv1alpha1.ClusterAPI{
		ObjectMeta: metav1.ObjectMeta{Name: clusterAPIName},
	}

	By("Waiting for revision "+string(revision)+" to be applied", func() {
		Eventually(kWithCtx(ctx).Object(clusterAPI)).
			WithContext(ctx).
			WithTimeout(defaultEventuallyTimeout).
			Should(HaveField("Status.CurrentRevision", Equal(revision)))
	})

	By("Waiting for conditions to be updated", func() {
		waitForConditions(ctx,
			test.HaveCondition(conditionTypeProgressing).WithStatus(configv1.ConditionFalse),
		)
	})
}
