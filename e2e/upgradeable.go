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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	"github.com/openshift/origin/test/shared/updateservice"
)

// upgradeTargetEnvVar optionally names the release image whose Cluster API
// components the test should assess, ideally a real Y+1 release. When unset,
// the test falls back to RELEASE_IMAGE_LATEST, the release image CI provided
// for this job, which must name the payload the cluster runs. The specified
// image must be pullable from the test host and ship the provider manifests.
const upgradeTargetEnvVar = "OPENSHIFT_AVAILABLE_UPDATE"

const (
	// upgradeTestChannel is the Cincinnati channel the fixture graph
	// advertises and the test points ClusterVersion.spec.channel at.
	upgradeTestChannel = "test-channel"

	// upgradeTestNamespacePrefix is the generateName prefix for the
	// namespace hosting the fake cincinnati update service.
	upgradeTestNamespacePrefix = "upgradeable-preflight-e2e-"

	// availabilityGates is the spec property the test removes from the
	// Clusters CRD to produce an incompatible CRD: it is guaranteed to be
	// flagged by the compatibility check, never set on live objects, and
	// reverts byte-identically.
	availabilityGates = "availabilityGates"

	// The remaining constants are boxcutter's object-control markers
	// (vendored machinery/objects.go, isBoxcutterManaged). The installer
	// passes no owner to boxcutter, so these markers alone decide whether
	// boxcutter considers itself an object's controller: an object carrying
	// both is controlled (drift is force-recovered), an object carrying
	// neither hits the default CollisionProtectionPrevent path (a collision
	// error is registered and the object is left untouched). The revision
	// annotation's prefix is the revision engine's SystemPrefix (see
	// setupRevisionEngine). These are boxcutter implementation details, not
	// API: re-verify them when the vendored boxcutter version changes.
	boxcutterManagedByLabelKey   = "app.kubernetes.io/managed-by"
	boxcutterManagedByLabelValue = "boxcutter"
	boxcutterRevisionAnnotation  = operatorstatus.CAPIOperatorIdentifierDomain + "/revision"
)

// clusterVersionUpstreamState captures the ClusterVersion fields the test
// redirects, so teardown can restore them exactly.
type clusterVersionUpstreamState struct {
	channel  string
	upstream string
}

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:ClusterAPIOperator] Cluster API upgradeability preflight",
	// The Serial decorator keeps ginkgo from running this spec concurrently
	// with any other spec under --procs; the Serial label is what the
	// openshift-tests-extension routes on for its serial suite. Both are
	// needed: this spec mutates the ClusterAPI singleton and the Clusters
	// CRD, which other specs assert on.
	Serial, Label("Serial"), Label("Lifecycle:informing"), Label("skip-topology:External"),
	Ordered, func() {
		// clustersCRDPresent selects the flow the Its follow:
		// - CRD absent: ClusterAPIMachineManagement feature gate is not
		//   enabled; there are no existing CAPI CRDs. The test extracts the
		//   fixture CRD  from the target payload.
		//
		// - CRD present: ClusterAPIMachineManagement feature gate is enabled;
		//   CAPI CRDs are already present.
		var clustersCRDPresent bool

		// The fake upgrade target: the next-minor version computed from the
		// current release and the release image resolved from the
		// environment, offered to the CVO by the fixture update service.
		var upgradeTargetVersion, upgradeTargetPayload string

		// The image the fake update service pod runs. It is resolved from the
		// cluster's tools ImageStreamTag: the recorded payload reference is
		// rewritten to the cluster's mirror on pull, so the fixture works where
		// the current payload's own reference does not resolve from the test host.
		var graphServerImage string

		// The Clusters CRD as shipped in the target payload, extracted from
		// the release image. It 1 asserts against its compatibility, and the
		// incompatible CRD in It 2 is derived from it.
		var clustersFixtureCRD *apiextensionsv1.CustomResourceDefinition

		// CRDs present flow only: the pre-mutation snapshot of the installer-managed
		// Clusters CRD, restored by the AfterAll.
		var clustersCRDSnapshot *apiextensionsv1.CustomResourceDefinition

		// unmanagedCRDsStateCaptured records that BeforeAll got far enough
		// to read (and find clean) the initial ClusterAPI state. The AfterAll
		// resets nothing before that point: a skip or an early failure means
		// the test changed nothing.
		var unmanagedCRDsStateCaptured bool

		// clustersCRDMarkedUnmanaged records that the remediation step
		// appended the Clusters CRD to
		// ClusterAPI/cluster.spec.unmanagedCustomResourceDefinitions. The
		// AfterAll only ever removes an entry this test added: an entry listed
		// when the flow failed before that append belongs to another test, and
		// removing it would change that test's revision.
		var clustersCRDMarkedUnmanaged bool

		// The deployed cincinnati fixture, shared between the deploy and
		// CVO-redirect setup steps.
		var updateService *updateservice.UpdateService

		BeforeAll(func(ctx SpecContext) {
			if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIOperator) {
				Skip("Feature gate ClusterAPIOperator is not enabled.")
			}

			if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateCRDCompatibilityRequirementOperator) {
				Skip("Feature gate CRDCompatibilityRequirementOperator is not enabled.")
			}

			if IsMicroShift {
				Skip("Cluster API is not supported on MicroShift")
			}
			if infra.Status.ControlPlaneTopology == configv1.ExternalTopologyMode {
				Skip("Cluster API upgradeability preflight is not supported on External topology clusters.")
			}

			By("Verifying the Clusters CRD is not already unmanaged", func() {
				failIfClustersCRDAlreadyUnmanaged(ctx)
				unmanagedCRDsStateCaptured = true
			})

			By("Selecting the test flow from the Clusters CRD presence", func() {
				clustersCRDPresent = clustersCRDIsPresent(ctx)

				flow := "no-CRD (main-branch CI)"
				if clustersCRDPresent {
					flow = "CRD-present (stable branch or machine-management-enabled cluster)"
				}
				GinkgoWriter.Printf("Cluster API upgradeability preflight flow: %s\n", flow)
			})

			var currentVersion string
			// clusterPayload is only the graph's current node, an opaque,
			// cluster-facing string: it is never dereferenced, because on
			// dev-scripts it names a cluster-internal mirror the test host
			// cannot resolve.
			var clusterPayload string
			By("Reading the current release from the ClusterVersion", func() {
				clusterVersion := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterVersion), clusterVersion)).To(Succeed(),
					"reading ClusterVersion to determine the current release")
				Expect(clusterVersion.Status.Desired.Version).ToNot(BeEmpty(),
					"ClusterVersion status.desired.version must be set to compute the fake upgrade target")
				Expect(clusterVersion.Status.Desired.Image).ToNot(BeEmpty(),
					"ClusterVersion status.desired.image must be set: it seeds the graph's current node")
				currentVersion = clusterVersion.Status.Desired.Version
				clusterPayload = clusterVersion.Status.Desired.Image
				trackResource(clusterVersion)
			})

			By("Computing the fake next-minor upgrade target version", func() {
				target, err := nextMinorVersion(currentVersion)
				Expect(err).ToNot(HaveOccurred(), "computing the fake target for current version %s", currentVersion)
				upgradeTargetVersion = target
			})

			By("Resolving the update target release", func() {
				var err error
				upgradeTargetPayload, err = resolveUpdateTargetRelease()
				if errors.Is(err, errNoUpdateTarget) {
					msg := fmt.Sprintf("neither %s nor %s is set: no release image to assess (set %s, e.g. to the cluster's own payload %q)",
						upgradeTargetEnvVar, releaseImageLATESTEnvVar, upgradeTargetEnvVar, clusterPayload)
					if os.Getenv("OPENSHIFT_CI") == "true" {
						// ci-operator injects OPENSHIFT_CI=true into every test pod.
						Fail(msg)
					}
					Skip(msg)
				}
				Expect(err).ToNot(HaveOccurred())
			})

			By("Extracting the fixture Clusters CRD from the target payload", func() {
				var err error
				clustersFixtureCRD, err = clustersCRDFromReleaseImage(ctx, upgradeTargetPayload)
				Expect(err).ToNot(HaveOccurred(), "extracting the %s CRD from release image %s", clusterCRDName, upgradeTargetPayload)
				Expect(clustersFixtureCRD.Name).To(Equal(clusterCRDName),
					"the extracted CRD document should be %s", clusterCRDName)
			})

			By("Resolving the fake update service image from the cluster's tools ImageStreamTag", func() {
				var err error
				graphServerImage, err = resolveGraphServerImage(ctx)
				Expect(err).ToNot(HaveOccurred(),
					"resolving the fake update service image from %s", graphServerImageStreamTag)
			})

			By("Deploying the fake cincinnati update service", func() {
				namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: upgradeTestNamespacePrefix}}
				Expect(cl.Create(ctx, namespace)).To(Succeed(), "creating a namespace for the fake update service")
				DeferCleanup(func(ctx SpecContext) {
					Expect(cl.Delete(ctx, namespace)).To(Succeed())
					Eventually(ctx, func() bool {
						return apierrors.IsNotFound(cl.Get(ctx, client.ObjectKeyFromObject(namespace), &corev1.Namespace{}))
					}).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(BeTrue(),
						"the %s namespace should disappear after deletion", namespace.Name)
				})
				trackResource(namespace)

				graph := updateservice.TwoNodeGraph(upgradeTestChannel, currentVersion, clusterPayload,
					upgradeTargetVersion, upgradeTargetPayload)
				updateService = deployFakeUpdateService(ctx, namespace.Name, graphServerImage, graph)
				DeferCleanup(func(ctx SpecContext) {
					Expect(updateService.Cleanup(ctx)).To(Succeed(), "cleaning up the fake cincinnati update service")
				})
			})

			By("Pointing the CVO at the fake update service", func() {
				originalUpstream := redirectClusterVersionAtFixture(ctx, updateService.URL.String())
				DeferCleanup(func(ctx SpecContext) {
					restoreClusterVersionUpstream(ctx, originalUpstream, upgradeTargetVersion, upgradeTargetPayload)
				})
			})

			By("Waiting for the CVO to offer the fake upgrade target", func() {
				waitForAvailableTargetUpdate(ctx, upgradeTargetVersion, upgradeTargetPayload)
			})
		})

		It("should report Upgradeable=True naming the target release when the payload CRDs are compatible", NodeTimeout(20*time.Minute), func(ctx SpecContext) {
			By("Waiting for Upgradeable=True naming the target release", func() {
				expectUpgradeableCondition(ctx, configv1.ConditionTrue, upgradeTargetVersion)
			})
		})

		It("should report Upgradeable=False when a CRD conflicts and recover after the CRD is remediated", NodeTimeout(20*time.Minute), func(ctx SpecContext) {
			// NOTE(2026-11-09): OTE does not respect Ordered specs, so each
			// test in this suite must also be safe to execute individually

			By("Forcing the "+clustersFixtureCRD.Name+" to be incompatible", func() {
				if clustersCRDPresent {
					By("Making the installed Clusters CRD incompatible", func() {
						clustersCRDSnapshot = makeInstalledClustersCRDIncompatible(ctx)
					})
				} else {
					By("Creating an incompatible Clusters CRD from the target payload", func() {
						createIncompatibleClustersCRD(ctx, clustersFixtureCRD)
					})
				}

				By("Waiting for Upgradeable=False naming the conflicting CRD", func() {
					expectUpgradeableCondition(ctx, configv1.ConditionFalse, clusterCRDName)
				})
			})

			By("Remediating the incompatibility", func() {
				var clusterAPI *operatorv1alpha1.ClusterAPI
				By("Marking the Clusters CRD unmanaged", func() {
					// Record the append before attempting it: the AfterAll reset is a
					// no-op when the entry is absent, so a failed append leaves it
					// nothing to undo, while a landed append is always undone.
					clustersCRDMarkedUnmanaged = true
					clusterAPI = markClustersCRDUnmanaged(ctx)
				})

				// The installer creates a CompatibilityRequirement for each unmanaged CRD
				// and gates every revision on it reaching Admitted=True and
				// Compatible=True, so the revision cannot converge until the
				// remediation below lands.
				By("Remediating the Clusters CRD to the payload version", func() {
					remediateClustersCRDToPayload(ctx, clustersFixtureCRD)
				})

				By("Waiting for revision with unmanaged CRD to rollout", func() {
					waitForRevisionRollout(ctx, clusterAPI.Generation, ContainElement(clusterCRDName))
				})

				By("Waiting for Upgradeable=True naming the target release", func() {
					expectUpgradeableCondition(ctx, configv1.ConditionTrue, upgradeTargetVersion)
				})
			})
		})

		// The AfterAll runs before the DeferCleanups registered in BeforeAll
		// (CVO restore, fixture teardown, namespace deletion), so the reset
		// below happens while the CVO still sees a sane update service. A
		// per-It DeferCleanup is deliberately not used either: it would run
		// between the Its, but It 3 builds on the CRD state It 2 leaves
		// behind.
		AfterAll(func(ctx SpecContext) {
			// A skip or failure before the initial ClusterAPI state was
			// captured means the test has changed nothing: nothing to reset.
			if !unmanagedCRDsStateCaptured {
				return
			}

			// Only undo an append this test made: an entry listed when the flow
			// failed before the remediation step appended it belongs to another
			// test, and removing it would change that test's revision.
			var clusterAPI *operatorv1alpha1.ClusterAPI
			if clustersCRDMarkedUnmanaged {
				By("Resetting the unmanagedCustomResourceDefinitions entry the remediation step added", func() {
					clusterAPI = resetUnmanagedCRDsEntry(ctx)
				})
			}

			// The CRD restore must precede the rollout wait: until the snapshot's
			// control markers are restored, boxcutter sees the Clusters CRD as
			// uncontrolled and (with the listing reset above) registers a
			// collision, so the installer never converges and the wait would
			// fail before the restore could run, leaving the cluster with the
			// remediated schema and no installer markers.
			By("Returning the Clusters CRD to its pre-test state", func() {
				switch {
				case clustersCRDSnapshot != nil:
					// restore the snapshot, leaving the pre-existing CRD
					// exactly as the test found it even on mid-test failure.
					restoreClustersCRDFromSnapshot(ctx, clustersCRDSnapshot)
				case !clustersCRDPresent:
					// delete the CRD the test created, returning the cluster to
					// its pre-test state.
					deleteClustersCRDIfPresent(ctx)
				}
			})

			// Without a listing reset there is no revision change to wait for,
			// and no collision for the installer to recover from either.
			if clusterAPI == nil {
				return
			}

			By("Waiting for revision without unmanaged CRD to rollout", func() {
				waitForRevisionRollout(ctx, clusterAPI.Generation, Not(ContainElement(clusterCRDName)))
			})
		})
	})

// kWithCtx returns a Komega whose API calls use the given context, so its
// assertion helpers stop polling when the node context is cancelled. Specs that
// assert on resources the cluster itself rewrites use this in place of the
// package-global komega context.
func kWithCtx(ctx context.Context) komega.Komega {
	return komega.New(cl).WithContext(ctx)
}

// failIfClustersCRDAlreadyUnmanaged fails when the Clusters CRD is already
// listed as unmanaged in the ClusterAPI singleton: this test appends that
// entry itself, and it refuses to touch an entry it did not add.
func failIfClustersCRDAlreadyUnmanaged(ctx context.Context) {
	GinkgoHelper()

	clusterAPI := &operatorv1alpha1.ClusterAPI{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
	Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterAPI), clusterAPI)).To(Succeed(),
		"ClusterAPI singleton should exist before the upgradeability preflight")
	Expect(clusterAPI.Spec).ToNot(BeNil(), "ClusterAPI should have a spec")
	Expect(clusterAPI.Spec.UnmanagedCustomResourceDefinitions).ToNot(ContainElement(clusterCRDName),
		"%s is already listed in ClusterAPI/cluster.spec.unmanagedCustomResourceDefinitions. "+
			"Likely cause: a previous run of this test or of the unmanaged CRD handoff test (e2e/unmanaged_crd_handoff.go) "+
			"failed before its cleanup removed the entry again. Use a fresh cluster.", clusterCRDName)
}

// clustersCRDIsPresent reports whether the Clusters CRD exists on the
// cluster.
func clustersCRDIsPresent(ctx context.Context) bool {
	GinkgoHelper()

	crd := &apiextensionsv1.CustomResourceDefinition{}
	err := cl.Get(ctx, client.ObjectKey{Name: clusterCRDName}, crd)
	if apierrors.IsNotFound(err) {
		return false
	}
	Expect(err).ToNot(HaveOccurred(), "failed to get the %s CRD while probing the test flow", clusterCRDName)
	return true
}

// deployFakeUpdateService deploys the cincinnati fixture serving graph into
// namespace, running the pod with shellImage, and returns it. Cleanup is the
// caller's responsibility.
func deployFakeUpdateService(ctx context.Context, namespace, shellImage string, graph updateservice.Graph) *updateservice.UpdateService {
	GinkgoHelper()

	kubeClient, err := kubernetes.NewForConfig(restConfig)
	Expect(err).ToNot(HaveOccurred(), "building a clientset for the update-service fixture")

	updateService, err := updateservice.Run(ctx, kubeClient, namespace, shellImage, graph)
	Expect(err).ToNot(HaveOccurred(), "deploying the fake update service into namespace %s with image %s", namespace, shellImage)
	return updateService
}

// patchClusterVersionSpec merge-patches the version ClusterVersion spec
// channel and upstream. An empty value removes the field.
func patchClusterVersionSpec(ctx context.Context, channel, upstream string) {
	GinkgoHelper()

	spec := struct {
		Channel  *string `json:"channel"`
		Upstream *string `json:"upstream"`
	}{}
	if channel != "" {
		spec.Channel = &channel
	}
	if upstream != "" {
		spec.Upstream = &upstream
	}
	patch, err := json.Marshal(map[string]any{"spec": spec})
	Expect(err).ToNot(HaveOccurred(), "marshaling the ClusterVersion spec patch")

	clusterVersion := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}}
	Expect(cl.Patch(ctx, clusterVersion, client.RawPatch(types.MergePatchType, patch))).To(Succeed(),
		"patching ClusterVersion channel=%q upstream=%q", channel, upstream)
}

// redirectClusterVersionAtFixture points the CVO at the fake update service
// and returns the original channel/upstream state for teardown.
func redirectClusterVersionAtFixture(ctx context.Context, upstreamURL string) clusterVersionUpstreamState {
	GinkgoHelper()

	clusterVersion := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}}
	Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterVersion), clusterVersion)).To(Succeed(),
		"reading the ClusterVersion before redirecting it at the fixture")
	original := clusterVersionUpstreamState{
		channel:  clusterVersion.Spec.Channel,
		upstream: string(clusterVersion.Spec.Upstream),
	}

	patchClusterVersionSpec(ctx, upgradeTestChannel, upstreamURL)
	return original
}

// restoreClusterVersionUpstream restores the original channel/upstream and
// waits until the CVO no longer offers the fake target in either
// status.availableUpdates or status.conditionalUpdates, so later teardown
// steps (deleting the fixture) cannot leave the CVO degraded.
func restoreClusterVersionUpstream(ctx context.Context, original clusterVersionUpstreamState, targetVersion, targetImage string) {
	GinkgoHelper()

	patchClusterVersionSpec(ctx, original.channel, original.upstream)

	clusterVersion := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}}
	Eventually(ctx, func() error {
		if err := cl.Get(ctx, client.ObjectKeyFromObject(clusterVersion), clusterVersion); err != nil {
			return fmt.Errorf("reading ClusterVersion: %w", err)
		}
		if targetUpdateOffered(clusterVersion, targetVersion, targetImage) {
			return fmt.Errorf("the CVO still offers it: %s", describeUpdateOffers(clusterVersion))
		}
		return nil
	}).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).
		Should(Succeed(),
			"the CVO should stop offering the fake target %s (%s) after its upstream is restored", targetVersion, targetImage)
}

// recommendedConditionType is the condition type the CVO sets on
// status.conditionalUpdates entries to record whether the update is
// recommended for the cluster. There is no exported constant for it in
// openshift/api.
const recommendedConditionType = "Recommended"

// targetUpdateOffered reports whether the ClusterVersion offers the given
// release as an update target, either unconditionally in
// status.availableUpdates or in status.conditionalUpdates. On clusters
// where the CVO evaluates update risks (any cluster with the
// TechPreviewNoUpgrade feature set: config-operator reports
// Upgradeable=False for the feature set and every minor update is demoted
// to conditionalUpdates), the target only ever appears in
// conditionalUpdates, so both lists must be accepted. The Recommended
// condition is deliberately ignored: a demoted entry persists with
// Recommended=False and its risks listed, and that target is exactly what
// the upgradeability preflight must assess.
func targetUpdateOffered(clusterVersion *configv1.ClusterVersion, targetVersion, targetImage string) bool {
	for _, update := range clusterVersion.Status.AvailableUpdates {
		if update.Version == targetVersion && update.Image == targetImage {
			return true
		}
	}
	for _, conditional := range clusterVersion.Status.ConditionalUpdates {
		if conditional.Release.Version == targetVersion && conditional.Release.Image == targetImage {
			return true
		}
	}
	return false
}

// findClusterVersionCondition returns the condition of the given type from
// the ClusterVersion's legacy-style status conditions, or nil when absent.
func findClusterVersionCondition(clusterVersion *configv1.ClusterVersion, conditionType configv1.ClusterStatusConditionType) *configv1.ClusterOperatorStatusCondition {
	for i := range clusterVersion.Status.Conditions {
		if clusterVersion.Status.Conditions[i].Type == conditionType {
			return &clusterVersion.Status.Conditions[i]
		}
	}
	return nil
}

// describeUpdateOffers summarizes what the CVO currently offers, for the
// failure messages of the target waits: whether the update service was
// reached at all, which releases are offered unconditionally, and which
// risks keep conditional updates from being recommended.
func describeUpdateOffers(clusterVersion *configv1.ClusterVersion) string {
	retrieved := "unset"
	if condition := findClusterVersionCondition(clusterVersion, configv1.RetrievedUpdates); condition != nil {
		retrieved = fmt.Sprintf("%s (%s: %s)", condition.Status, condition.Reason, condition.Message)
	}

	available := make([]string, 0, len(clusterVersion.Status.AvailableUpdates))
	for _, update := range clusterVersion.Status.AvailableUpdates {
		available = append(available, update.Version)
	}

	conditional := make([]string, 0, len(clusterVersion.Status.ConditionalUpdates))
	for _, update := range clusterVersion.Status.ConditionalUpdates {
		risks := make([]string, 0, len(update.Risks))
		for _, risk := range update.Risks {
			risks = append(risks, risk.Name)
		}
		recommended := "Unknown"
		if condition := meta.FindStatusCondition(update.Conditions, recommendedConditionType); condition != nil {
			recommended = string(condition.Status)
		}
		conditional = append(conditional, fmt.Sprintf("%s (Recommended=%s, risks=%v)",
			update.Release.Version, recommended, risks))
	}

	return fmt.Sprintf("RetrievedUpdates=%s, availableUpdates=%v, conditionalUpdates=%v",
		retrieved, available, conditional)
}

// waitForAvailableTargetUpdate waits until the CVO, polling the redirected
// update service, offers the fake target in status.availableUpdates or
// status.conditionalUpdates (see targetUpdateOffered for why both lists
// count). The polling error carries what the CVO offers instead, so a
// timeout failure names the RetrievedUpdates state and any blocking risks
// rather than only the missing target.
func waitForAvailableTargetUpdate(ctx context.Context, targetVersion, targetImage string) {
	GinkgoHelper()

	clusterVersion := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}}
	Eventually(ctx, func() error {
		if err := cl.Get(ctx, client.ObjectKeyFromObject(clusterVersion), clusterVersion); err != nil {
			return fmt.Errorf("reading ClusterVersion: %w", err)
		}
		if targetUpdateOffered(clusterVersion, targetVersion, targetImage) {
			return nil
		}
		return fmt.Errorf("the CVO offers something else: %s", describeUpdateOffers(clusterVersion))
	}).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).
		Should(Succeed(),
			"the CVO should offer the fake target %s (%s) from the redirected update service", targetVersion, targetImage)
}

// expectUpgradeableCondition waits for the cluster-api ClusterOperator to
// report the Upgradeable condition with the given status and a message
// containing messageSubstring. The message assertion is what makes the
// True checks non-vacuous: without it, a controller that never noticed the
// fake update would pass on the default True.
func expectUpgradeableCondition(ctx context.Context, status configv1.ConditionStatus, messageSubstring string) {
	GinkgoHelper()

	clusterOperator := &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: framework.CAPIClusterOperatorName}}
	Eventually(ctx, kWithCtx(ctx).Object(clusterOperator)).
		WithTimeout(framework.WaitLong).WithPolling(framework.RetryMedium).
		Should(HaveField("Status.Conditions", test.HaveCondition(configv1.OperatorUpgradeable).
			WithStatus(status).
			WithMessage(ContainSubstring(messageSubstring))),
			"the cluster-api ClusterOperator should report Upgradeable=%s with a message naming %q", status, messageSubstring)
}

// createIncompatibleClustersCRD creates the payload fixture CRD with
// spec.availabilityGates removed from every served version. The AfterAll
// deletes it again (a per-It DeferCleanup would run between the Its, but
// It 3 builds on the CRD this leaves behind).
func createIncompatibleClustersCRD(ctx context.Context, fixture *apiextensionsv1.CustomResourceDefinition) {
	GinkgoHelper()

	incompatible := fixture.DeepCopy()
	// Creation must not carry server-populated metadata from the extracted
	// document.
	incompatible.ResourceVersion = ""
	incompatible.UID = ""
	incompatible.Generation = 0
	incompatible.CreationTimestamp = metav1.Time{}
	incompatible.ManagedFields = nil
	incompatible.Status = apiextensionsv1.CustomResourceDefinitionStatus{}

	removeSpecPropertyFromAllVersions(incompatible, availabilityGates)
	// The fixture's webhook conversion points at a service that does not
	// exist on machine-management-off clusters. The compatibility check
	// does not compare conversion, so serve None instead of a dangling
	// webhook reference.
	incompatible.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{Strategy: apiextensionsv1.NoneConverter}

	Expect(cl.Create(ctx, incompatible)).To(Succeed(), "creating the incompatible %s from the target payload", clusterCRDName)
	trackResource(incompatible)
}

// deleteClustersCRDIfPresent deletes the Clusters CRD and waits for it to
// disappear. A no-op when the CRD does not exist (e.g. It 2 never ran).
func deleteClustersCRDIfPresent(ctx context.Context) {
	GinkgoHelper()

	crd := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: clusterCRDName}}
	if err := cl.Delete(ctx, crd); err != nil && !apierrors.IsNotFound(err) {
		Expect(err).ToNot(HaveOccurred(), "deleting the %s", clusterCRDName)
	}

	Eventually(ctx, func() bool {
		return apierrors.IsNotFound(cl.Get(ctx, client.ObjectKeyFromObject(crd), &apiextensionsv1.CustomResourceDefinition{}))
	}).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(BeTrue(),
		"the %s should disappear after deletion", clusterCRDName)
}

// makeInstalledClustersCRDIncompatible makes the installer-managed Clusters
// CRD incompatible by removing spec.availabilityGates from every served
// version, in the same atomic update that strips boxcutter's control markers
// (the app.kubernetes.io/managed-by: boxcutter label and the per-object
// revision annotation) plus the installer's managed-by tracking label.
// Without its control markers boxcutter no longer recognizes the CRD as its
// own, so the installer's default collision protection (Prevent) makes it
// register a collision error and leave the incompatible CRD untouched,
// instead of force-recovering the CRD back to the desired schema. At
// mutation time no installer Deny requirement exists for the CRD (it is
// still managed) and the preflight requirements only Warn, so the update is
// admitted. It returns the pre-mutation snapshot for the AfterAll, which
// restores it so even a mid-test failure leaves the cluster with a
// compatible, installer-controlled CRD.
func makeInstalledClustersCRDIncompatible(ctx context.Context) *apiextensionsv1.CustomResourceDefinition {
	GinkgoHelper()

	liveCRD := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: clusterCRDName}}
	Expect(cl.Get(ctx, client.ObjectKeyFromObject(liveCRD), liveCRD)).To(Succeed(),
		"the installer-managed %s should exist before mutating it", clusterCRDName)
	snapshot := liveCRD.DeepCopy()

	// The collision mechanism depends on these markers being present before
	// the mutation strips them: a CRD without them would already be in
	// collision (or foreign), and the test would assert against a different
	// code path than the one it means to exercise.
	Expect(liveCRD.GetLabels()).To(HaveKeyWithValue(boxcutterManagedByLabelKey, boxcutterManagedByLabelValue),
		"the installer-managed %s should carry boxcutter's managed-by label before mutating it", clusterCRDName)
	Expect(liveCRD.GetAnnotations()).To(HaveKey(boxcutterRevisionAnnotation),
		"the installer-managed %s should carry boxcutter's revision annotation before mutating it", clusterCRDName)

	Eventually(ctx, kWithCtx(ctx).Update(liveCRD, func() {
		labels := liveCRD.GetLabels()
		delete(labels, revisiongenerator.ManagedLabelKey)
		delete(labels, boxcutterManagedByLabelKey)
		liveCRD.SetLabels(labels)

		annotations := liveCRD.GetAnnotations()
		delete(annotations, boxcutterRevisionAnnotation)
		liveCRD.SetAnnotations(annotations)

		removeSpecPropertyFromAllVersions(liveCRD, availabilityGates)
	})).WithTimeout(framework.WaitShort).WithPolling(framework.RetryShort).Should(Succeed(),
		"removing the control markers and %s should land in a single atomic update", availabilityGates)

	return snapshot
}

// restoreClustersCRDFromSnapshot restores the snapshot's schema (re-adding
// the removed property) and the control markers the mutation stripped: the
// installer's managed-by tracking label and boxcutter's managed-by label
// and revision annotation. Without the boxcutter markers restored the
// installer would keep registering collision errors for the CRD forever.
// The marker restore is skipped when the CRD is still listed as unmanaged,
// leaving a consistent state either way; the AfterAll resets the listing
// before calling this, so the markers are normally restored.
func restoreClustersCRDFromSnapshot(ctx context.Context, snapshot *apiextensionsv1.CustomResourceDefinition) {
	GinkgoHelper()

	liveCRD := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: clusterCRDName}}
	err := cl.Get(ctx, client.ObjectKeyFromObject(liveCRD), liveCRD)
	if apierrors.IsNotFound(err) {
		return // already deleted (test cleanup, or the CRD never existed)
	}
	Expect(err).ToNot(HaveOccurred(), "reading the %s before restoring its snapshot", clusterCRDName)

	clusterAPI := &operatorv1alpha1.ClusterAPI{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
	Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterAPI), clusterAPI)).To(Succeed())
	unmanaged := slices.Contains(clusterAPI.Spec.UnmanagedCustomResourceDefinitions, clusterCRDName)

	Eventually(ctx, kWithCtx(ctx).Update(liveCRD, func() {
		liveCRD.Spec.Versions = snapshot.Spec.Versions
		if !unmanaged {
			labels := liveCRD.GetLabels()
			if labels == nil {
				labels = map[string]string{}
			}
			labels[revisiongenerator.ManagedLabelKey] = snapshot.Labels[revisiongenerator.ManagedLabelKey]
			labels[boxcutterManagedByLabelKey] = snapshot.Labels[boxcutterManagedByLabelKey]
			liveCRD.SetLabels(labels)

			annotations := liveCRD.GetAnnotations()
			if annotations == nil {
				annotations = map[string]string{}
			}
			annotations[boxcutterRevisionAnnotation] = snapshot.Annotations[boxcutterRevisionAnnotation]
			liveCRD.SetAnnotations(annotations)
		}
	})).WithTimeout(framework.WaitShort).WithPolling(framework.RetryShort).Should(Succeed(),
		"restoring the %s snapshot schema and control markers should leave a compatible CRD", clusterCRDName)
}

// markClustersCRDUnmanaged appends the Clusters CRD to
// ClusterAPI/cluster.spec.unmanagedCustomResourceDefinitions and waits for
// the revision rollout the append triggers: the remediation write must
// happen under the new revision's Deny requirement.
func markClustersCRDUnmanaged(ctx context.Context) *operatorv1alpha1.ClusterAPI {
	GinkgoHelper()

	clusterAPI := &operatorv1alpha1.ClusterAPI{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
	Eventually(ctx, kWithCtx(ctx).Update(clusterAPI, func() {
		// The mutation is replayed on conflict, so only append if a
		// previous attempt did not already add the CRD.
		if !slices.Contains(clusterAPI.Spec.UnmanagedCustomResourceDefinitions, clusterCRDName) {
			clusterAPI.Spec.UnmanagedCustomResourceDefinitions = append(
				clusterAPI.Spec.UnmanagedCustomResourceDefinitions, clusterCRDName,
			)
		}
	})).WithTimeout(framework.WaitShort).WithPolling(framework.RetryShort).Should(Succeed(),
		"appending %s to unmanagedCustomResourceDefinitions should be conflict-safe", clusterCRDName)

	return clusterAPI
}

// remediateClustersCRDToPayload rewrites the Clusters CRD's versions to the
// payload fixture's.
func remediateClustersCRDToPayload(ctx context.Context, fixture *apiextensionsv1.CustomResourceDefinition) {
	GinkgoHelper()

	liveCRD := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: clusterCRDName}}
	Expect(cl.Get(ctx, client.ObjectKeyFromObject(liveCRD), liveCRD)).To(Succeed(),
		"the %s should exist when remediating it", clusterCRDName)

	Eventually(ctx, kWithCtx(ctx).Update(liveCRD, func() {
		liveCRD.Spec.Versions = fixture.Spec.Versions
	})).WithTimeout(framework.WaitShort).WithPolling(framework.RetryShort).Should(Succeed(),
		"remediating the %s to the payload version should be admitted under the Deny requirement", clusterCRDName)
}
