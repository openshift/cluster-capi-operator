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
	"reflect"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
)

// waitForRevisionRollout waits until the revision controller has observed
// the ClusterAPI generation, the installer has converged
// (currentRevision == desiredRevision), and the converged revision's
// UnmanagedCustomResourceDefinitions satisfies matcher.
func waitForRevisionRollout(ctx context.Context, generation int64, matcher types.GomegaMatcher) {
	GinkgoHelper()

	clusterAPI := &operatorv1alpha1.ClusterAPI{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
	Eventually(ctx, func(g Gomega) {
		g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterAPI), clusterAPI)).To(Succeed())
		g.Expect(clusterAPI.Status.ObservedRevisionGeneration).To(BeNumerically(">=", generation),
			"the revision controller should observe ClusterAPI generation %d", generation)
		g.Expect(clusterAPI.Status.DesiredRevision).ToNot(BeEmpty())
		g.Expect(clusterAPI.Status.CurrentRevision).To(Equal(clusterAPI.Status.DesiredRevision),
			"the installer should converge on the new revision")
		g.Expect(clusterAPI.Status.Revisions).To(ContainElement(SatisfyAll(
			HaveField("Name", Equal(clusterAPI.Status.CurrentRevision)),
			HaveField("UnmanagedCustomResourceDefinitions", matcher),
		)))
	}).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(Succeed())
}

// clusterAPICRDName is the CRD defining the ClusterAPI singleton. Its
// schema carries the CEL validation that makes
// spec.unmanagedCustomResourceDefinitions irreversible in production.
const clusterAPICRDName = "clusterapis.operator.openshift.io"

// resetUnmanagedCRDsEntry removes the Clusters CRD from
// ClusterAPI/cluster.spec.unmanagedCustomResourceDefinitions, undoing an
// append of that entry, and returns the ClusterAPI state containing that
// removal: the caller waits for the revision rollout triggered by its
// Generation.
//
// Only that one entry is removed: the list is edited in place rather than
// replaced by a snapshot, so a caller can never discard an entry it did not
// add (e.g. one added by another test on the same cluster).
//
// The ClusterAPI CRD's CEL validation makes the entry irreversible in
// production (list items cannot be removed, the field cannot be unset), so
// this strips that validation from the CRD just long enough to perform the
// removal and then restores it from a snapshot (the caller waits for the
// revision rollout the removal triggers). A no-op when the Clusters CRD is not
// listed (e.g. the append never ran), in which case the state returned is the
// one read.
//
// The ClusterAPI CRD is managed by the CVO, which can drift-revert the
// strip (restoring the validation) at any moment, and kube-apiserver can
// keep enforcing the validation until it has registered the CRD update.
// The strip and the entry removal are therefore retried together until
// the removal lands: both are idempotent, and a reverted or
// not-yet-registered strip simply rejects the removal until the next
// attempt re-applies it.
func resetUnmanagedCRDsEntry(ctx context.Context) *operatorv1alpha1.ClusterAPI {
	GinkgoHelper()

	clusterAPI := &operatorv1alpha1.ClusterAPI{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
	Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterAPI), clusterAPI)).To(Succeed(),
		"reading ClusterAPI before resetting its unmanagedCustomResourceDefinitions")
	if !slices.Contains(clusterAPI.Spec.UnmanagedCustomResourceDefinitions, clusterCRDName) {
		return clusterAPI
	}

	celSnapshot := getClusterAPICRD(ctx)
	// Restore the CEL validation even when a step in between fails, so the
	// cluster is never left with the validation lifted. The restore is
	// idempotent and retries, so a CVO revert racing it cannot leave the
	// validation lifted either.
	defer restoreUnmanagedCRDsCELValidation(ctx, celSnapshot)

	Eventually(ctx, func(g Gomega) {
		g.Expect(stripUnmanagedCRDsCELValidationOnce(ctx)).To(Succeed(),
			"idempotently stripping the unmanagedCRDs CEL validation from %s", clusterAPICRDName)
		g.Expect(removeUnmanagedCRDsEntryOnce(ctx, clusterAPI)).To(Succeed(),
			"idempotently removing the %s entry from unmanagedCustomResourceDefinitions", clusterCRDName)
	}).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryShort).Should(Succeed(),
		"the %s entry should be removable once the CEL validation is lifted and registered", clusterCRDName)

	return clusterAPI
}

// getClusterAPICRD returns the live ClusterAPI CRD, the pre-strip state
// for restoreUnmanagedCRDsCELValidation.
func getClusterAPICRD(ctx context.Context) *apiextensionsv1.CustomResourceDefinition {
	GinkgoHelper()

	crd := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: clusterAPICRDName}}
	Expect(cl.Get(ctx, client.ObjectKeyFromObject(crd), crd)).To(Succeed(),
		"reading the %s before stripping its unmanagedCRDs CEL validation", clusterAPICRDName)
	return crd
}

// stripUnmanagedCRDsCELValidationOnce makes one idempotent attempt at
// removing the x-kubernetes-validations protecting
// spec.unmanagedCustomResourceDefinitions (the spec-level rule that the
// field cannot be unset and the list-level rule that items cannot be
// removed) from every version of the ClusterAPI CRD. It is a no-op when
// the validation is already stripped, e.g. when a previous attempt landed
// and the CVO has not reverted it.
func stripUnmanagedCRDsCELValidationOnce(ctx context.Context) error {
	crd := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: clusterAPICRDName}}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(crd), crd); err != nil {
		return fmt.Errorf("reading the %s: %w", clusterAPICRDName, err)
	}

	needsUpdate := false
	for i := range crd.Spec.Versions {
		version := &crd.Spec.Versions[i]
		if unmanagedCRDsValidationsOf(version).empty() {
			continue
		}
		unmanagedCRDsValidations{}.apply(version)
		needsUpdate = true
	}
	if !needsUpdate {
		return nil
	}

	if err := cl.Update(ctx, crd); err != nil {
		return fmt.Errorf("stripping the unmanagedCRDs CEL validation from %s: %w", clusterAPICRDName, err)
	}
	return nil
}

// removeUnmanagedCRDsEntryOnce makes one idempotent attempt at restoring
// ClusterAPI/cluster.spec.unmanagedCustomResourceDefinitions to initial,
// writing through clusterAPI (which then reflects the update). It is a
// no-op when the Clusters CRD is no longer listed. The write is rejected
// while the CEL validation is enforced (the CVO reverted the strip, or
// kube-apiserver has not registered it yet); the caller retries the pair.
func removeUnmanagedCRDsEntryOnce(ctx context.Context, clusterAPI *operatorv1alpha1.ClusterAPI) error {
	if err := cl.Get(ctx, client.ObjectKeyFromObject(clusterAPI), clusterAPI); err != nil {
		return fmt.Errorf("reading ClusterAPI: %w", err)
	}

	remainingEntries := slices.DeleteFunc(slices.Clone(clusterAPI.Spec.UnmanagedCustomResourceDefinitions),
		func(name string) bool { return name == clusterCRDName })
	if len(remainingEntries) == len(clusterAPI.Spec.UnmanagedCustomResourceDefinitions) {
		return nil
	}

	clusterAPI.Spec.UnmanagedCustomResourceDefinitions = remainingEntries
	if err := cl.Update(ctx, clusterAPI); err != nil {
		return fmt.Errorf("removing the %s entry from unmanagedCustomResourceDefinitions: %w", clusterCRDName, err)
	}
	return nil
}

// restoreUnmanagedCRDsCELValidation idempotently re-applies the
// unmanagedCRDs x-kubernetes-validations captured in the snapshot onto
// the live ClusterAPI CRD, retrying until every version carries them
// again: the CVO can drift-revert the CRD at any moment, so a single
// write followed by a bare check is not enough. The live CRD is fetched
// fresh, so a resourceVersion drift from the snapshot cannot fail the
// restore, and rewriting rules that are already present is a no-op.
func restoreUnmanagedCRDsCELValidation(ctx context.Context, snapshot *apiextensionsv1.CustomResourceDefinition) {
	GinkgoHelper()

	crd := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: clusterAPICRDName}}
	Eventually(ctx, func(g Gomega) {
		g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(crd), crd)).To(Succeed(),
			"reading the %s before restoring its unmanagedCRDs CEL validation", clusterAPICRDName)

		// The end state must be identical to the initial one, so verify the
		// validation against the snapshot rather than trusting the update,
		// and re-apply whenever a drift-revert (or our own strip landing
		// late) has dropped it.
		if !crdCarriesSnapshotValidations(crd, snapshot) {
			for i := range crd.Spec.Versions {
				version := &crd.Spec.Versions[i]
				snapshotVersion := findCRDVersion(snapshot, version.Name)
				if snapshotVersion == nil {
					continue
				}
				unmanagedCRDsValidationsOf(snapshotVersion).apply(version)
			}
			g.Expect(cl.Update(ctx, crd)).To(Succeed(),
				"restoring the unmanagedCRDs CEL validation on %s", clusterAPICRDName)
		}

		g.Expect(crdCarriesSnapshotValidations(crd, snapshot)).To(BeTrue(),
			"every version of %s should carry its unmanagedCRDs CEL validation again", clusterAPICRDName)
	}).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(Succeed(),
		"the unmanagedCRDs CEL validation should be back on %s", clusterAPICRDName)
}

// crdCarriesSnapshotValidations reports whether every version of the CRD
// carries the snapshot's unmanagedCRDs CEL validation.
func crdCarriesSnapshotValidations(crd, snapshot *apiextensionsv1.CustomResourceDefinition) bool {
	for i := range crd.Spec.Versions {
		version := &crd.Spec.Versions[i]
		snapshotVersion := findCRDVersion(snapshot, version.Name)
		if snapshotVersion == nil {
			continue
		}
		if !reflect.DeepEqual(unmanagedCRDsValidationsOf(version), unmanagedCRDsValidationsOf(snapshotVersion)) {
			return false
		}
	}
	return true
}

// unmanagedCRDsValidations is the pair of x-kubernetes-validations rule
// sets protecting spec.unmanagedCustomResourceDefinitions on one version of
// the ClusterAPI CRD: the rules on the spec schema itself ("cannot be
// unset") and on the list property ("items cannot be removed").
type unmanagedCRDsValidations struct {
	spec      apiextensionsv1.ValidationRules
	unmanaged apiextensionsv1.ValidationRules
}

// unmanagedCRDsValidationsOf extracts the unmanagedCRDs CEL validation from
// one version of the ClusterAPI CRD. A missing schema yields the zero
// value, so versions without the validation compare equal to snapshots
// without it.
func unmanagedCRDsValidationsOf(version *apiextensionsv1.CustomResourceDefinitionVersion) unmanagedCRDsValidations {
	var validations unmanagedCRDsValidations
	if version.Schema == nil || version.Schema.OpenAPIV3Schema == nil {
		return validations
	}
	specSchema, ok := version.Schema.OpenAPIV3Schema.Properties["spec"]
	if !ok {
		return validations
	}
	validations.spec = specSchema.XValidations
	if unmanaged, ok := specSchema.Properties["unmanagedCustomResourceDefinitions"]; ok {
		validations.unmanaged = unmanaged.XValidations
	}
	return validations
}

// empty reports whether neither rule set carries any rules, i.e. the
// validation is stripped.
func (v unmanagedCRDsValidations) empty() bool {
	return len(v.spec) == 0 && len(v.unmanaged) == 0
}

// apply writes the validation pair onto the version's schema. Versions
// without a spec schema are left untouched.
func (v unmanagedCRDsValidations) apply(version *apiextensionsv1.CustomResourceDefinitionVersion) {
	if version.Schema == nil || version.Schema.OpenAPIV3Schema == nil {
		return
	}
	specSchema, ok := version.Schema.OpenAPIV3Schema.Properties["spec"]
	if !ok {
		return
	}
	specSchema.XValidations = v.spec
	if unmanaged, ok := specSchema.Properties["unmanagedCustomResourceDefinitions"]; ok {
		unmanaged.XValidations = v.unmanaged
		specSchema.Properties["unmanagedCustomResourceDefinitions"] = unmanaged
	}
	version.Schema.OpenAPIV3Schema.Properties["spec"] = specSchema
}

// findCRDVersion returns the version with the given name from the CRD, or
// nil when absent.
func findCRDVersion(crd *apiextensionsv1.CustomResourceDefinition, name string) *apiextensionsv1.CustomResourceDefinitionVersion {
	for i := range crd.Spec.Versions {
		if crd.Spec.Versions[i].Name == name {
			return &crd.Spec.Versions[i]
		}
	}
	return nil
}
