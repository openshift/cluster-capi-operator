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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"k8s.io/apimachinery/pkg/runtime/schema"

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

	coreCMName                = "test-cm-core"
	infraCMName               = "test-cm-infra"
	addonCMName               = "test-cm-addon"
	deploymentName            = "test-deployment"
	clusterRoleName           = "test-clusterrole"
	clusterRole2Name          = "test-clusterrole-2"
	testNamespaceName         = "test-related-ns"
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
)

var (
	// allProviderProfiles is the common pool of provider profiles, populated in BeforeSuite.
	allProviderProfiles []providerimages.ProviderImageManifests

	// providersByName maps provider name to its profile for easy lookup.
	providersByName map[string]providerimages.ProviderImageManifests
)

// setupProviderProfiles creates manifest fixtures and populates allProviderProfiles.
func setupProviderProfiles() {
	GinkgoHelper()

	tb := GinkgoTB()

	// Provider "core": ConfigMap-A with data v1
	core := test.NewTestProvider(tb, providerCore,
		test.WithManifests(test.ConfigMapYAML(coreCMName, map[string]string{"version": "v1"})),
	)

	// Provider "infra": ConfigMap-B with data v1
	infra := test.NewTestProvider(tb, providerInfra,
		test.WithManifests(test.ConfigMapYAML(infraCMName, map[string]string{"version": "v1"})),
	)

	// Provider "addon": ConfigMap-C (no CRDs)
	addon := test.NewTestProvider(tb, providerAddon,
		test.WithManifests(test.ConfigMapYAML(addonCMName, map[string]string{"addon": "true"})),
	)

	// Provider "core-v2": ConfigMap-A with updated data v2
	coreV2 := test.NewTestProvider(tb, providerCoreV2,
		test.WithManifests(test.ConfigMapYAML(coreCMName, map[string]string{"version": "v2"})),
	)

	// Provider "dup-obj": ConfigMap-A (same object as core, for duplicate testing)
	dupObj := test.NewTestProvider(tb, providerDupObj,
		test.WithManifests(test.ConfigMapYAML(coreCMName, map[string]string{"version": "v1"})),
	)

	// Provider "cluster-scoped": a ClusterRole (non-namespaced, produces relatedObjects)
	clusterScoped := test.NewTestProvider(tb, providerClusterScoped,
		test.WithManifests(test.ClusterRoleYAML(clusterRoleName)),
	)

	// Provider "cluster-scoped-2": a second ClusterRole (for multi-object relatedObjects testing)
	clusterScoped2 := test.NewTestProvider(tb, providerClusterScoped2,
		test.WithManifests(test.ClusterRoleYAML(clusterRole2Name)),
	)

	// Provider "crd-provider": a CRD (produces a relatedObjects entry for instance type)
	testCRD := test.GenerateSchemalessSpecStatusCRD(testCRDGVK)
	crdProvider := test.NewTestProvider(tb, providerCRD,
		test.WithManifests(test.CRDAsYAML(testCRD)),
	)

	// Provider "namespace-provider": a Namespace object (cluster-scoped)
	nsProvider := test.NewTestProvider(tb, providerNamespace,
		test.WithManifests(test.NamespaceYAML(testNamespaceName)),
	)

	// Provider "deployment-provider": a Deployment (probed for Available condition)
	deploymentProvider := test.NewTestProvider(tb, providerDeployment,
		test.WithManifests(test.DeploymentYAML(deploymentName)),
		test.WithInstallOrder(1),
	)

	// Provider "mixed": a CRD and a ConfigMap in the same component (exercises both CRD and objects phases)
	mixedCRD := test.GenerateSchemalessSpecStatusCRD(mixedCRDGVK)
	mixed := test.NewTestProvider(tb, providerMixed,
		test.WithManifests(
			test.CRDAsYAML(mixedCRD),
			test.ConfigMapYAML(mixedCMName, map[string]string{"source": "mixed"}),
		),
	)

	// Provider "many-cluster-scoped": 10 ClusterRoles for testing relatedObjects ordering
	manyClusterScoped := test.NewTestProvider(tb, providerManyClusterScoped,
		test.WithManifests(
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
		),
	)

	allProviderProfiles = []providerimages.ProviderImageManifests{
		core, infra, addon, coreV2, dupObj,
		clusterScoped, clusterScoped2, crdProvider, nsProvider,
		deploymentProvider, mixed, manyClusterScoped,
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

	// Get current ClusterAPI to determine revision index.
	clusterAPI := &operatorv1alpha1.ClusterAPI{}
	Expect(cl.Get(ctx, client.ObjectKey{Name: clusterAPIName}, clusterAPI)).To(Succeed())

	var apiRev operatorv1alpha1.ClusterAPIInstallerRevision

	By("Rendering new revision", func() {
		profiles := lookupProfiles(providerNames...)

		// Render the revision to compute the correct content ID.
		rendered, err := revisiongenerator.NewRenderedRevision(profiles)
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
	profiles := make([]providerimages.ProviderImageManifests, len(names))
	for i, name := range names {
		p, ok := providersByName[name]
		Expect(ok).To(BeTrue(), "unknown provider profile: %s", name)

		profiles[i] = p
	}

	return profiles
}

// createFixtures creates ClusterAPI and ClusterOperator singletons.
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

	clusterOperatorObj := &configv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-api"},
	}
	Expect(cl.Create(ctx, clusterOperatorObj)).To(Succeed())
	cleanupObjs = append(cleanupObjs, clusterOperatorObj)
}

// createFixturesWithoutClusterAPI creates only the ClusterOperator (not ClusterAPI).
func createFixturesWithoutClusterAPI(ctx context.Context) {
	GinkgoHelper()

	clusterOperatorObj := &configv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-api"},
	}
	Expect(cl.Create(ctx, clusterOperatorObj)).To(Succeed())

	DeferCleanup(func(ctx context.Context) {
		deleteAndWait(ctx, clusterOperatorObj)
	})
}

// deleteAndWait deletes objects and waits for them to disappear.
// Tolerates conflicts and not-found errors since the controller
// may be concurrently modifying these objects.
func deleteAndWait(ctx context.Context, objs ...client.Object) {
	GinkgoHelper()

	By("Deleting test objects", func() {
		for _, obj := range objs {
			Expect(cl.Delete(ctx, obj)).To(Succeed())
		}
	})

	By("Waiting for test objects to be not found", func() {
		for _, obj := range objs {
			Eventually(kWithCtx(ctx).Get(obj)).
				WithContext(ctx).
				WithTimeout(defaultEventuallyTimeout).
				Should(WithTransform(apierrors.IsNotFound, BeTrue()))
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

// waitForSuccess waits for both Progressing=False and Degraded=False with Success reason.
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
