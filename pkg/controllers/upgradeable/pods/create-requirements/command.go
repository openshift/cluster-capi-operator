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
package createrequirements

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	openshiftapiextensionsv1 "github.com/openshift/api/apiextensions/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/upgradeable/pods"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	k8syaml "sigs.k8s.io/yaml"
)

const (
	// RequirementNamePrefix names every requirement created by an assessment.
	RequirementNamePrefix = "capi-operator-upgradeable-"

	// requirementsFieldOwner owns the requirement set through server side apply.
	requirementsFieldOwner = "UpgradeableCompatibilityRequirements"
)

var (
	errNoProfiles     = errors.New("no provider profiles discovered")
	errCRDNameMissing = errors.New("CRD has no name")
)

// Run executes the requirements pod entrypoint.
func Run(ctx context.Context, args []string) error {
	return pods.Run(ctx, "upgradeable-compatibility-requirements", args, func(ctx context.Context, c client.Client, current, target string) error {
		return Step(ctx, c, current, target, providerimages.ProviderImageMountBase)
	})
}

// Step converges the requirement set on the CRDs of the target payload.
func Step(ctx context.Context, c client.Client, current, target, root string) error {
	// Parse every profile before publishing anything, so malformed YAML in a
	// later profile does not leave a partially created requirement set.
	crds, err := DiscoverCRDs(root)
	if err != nil {
		return pods.Terminal(err)
	}

	owner, err := pods.OwnerReference(ctx, c)
	if err != nil {
		return err
	}

	assessmentLabels := pods.AssessmentLabels(current, target)
	expected := sets.New[string]()
	requirements := make([]*openshiftapiextensionsv1.CompatibilityRequirement, 0, len(crds))

	for _, name := range sets.List(sets.KeySet(crds)) {
		requirement, err := BuildRequirement(crds[name])
		if err != nil {
			return pods.Terminal(err)
		}

		requirement.Labels = assessmentLabels
		requirement.OwnerReferences = []metav1.OwnerReference{owner}
		expected.Insert(requirement.Name)
		requirements = append(requirements, requirement)
	}

	// The target payload is the complete requirement set. A requirement whose CRD
	// it no longer contains must not keep contributing an earlier verdict.
	if err := deleteUnexpected(ctx, c, expected); err != nil {
		return err
	}

	// Server side apply replays safely after an ambiguous API failure and repairs
	// external edits to the fields this manager owns, including the labels that
	// identify the assessment.
	for _, requirement := range requirements {
		if err := c.Patch(ctx, requirement, client.Apply, operatorstatus.CAPIFieldOwner(requirementsFieldOwner), client.ForceOwnership); err != nil {
			return pods.APIError(fmt.Errorf("applying requirement %s: %w", requirement.Name, err))
		}
	}

	return nil
}

func deleteUnexpected(ctx context.Context, c client.Client, expected sets.Set[string]) error {
	existing := &openshiftapiextensionsv1.CompatibilityRequirementList{}
	if err := c.List(ctx, existing, client.MatchingLabels{pods.ManagedLabel: pods.ManagedLabelValue}); err != nil {
		return pods.APIError(fmt.Errorf("listing assessment requirements: %w", err))
	}

	for i := range existing.Items {
		requirement := &existing.Items[i]
		if expected.Has(requirement.Name) {
			continue
		}

		// Deletion is asynchronous because the requirement has a finalizer, and
		// nothing in this pod may wait for it.
		if err := c.Delete(ctx, requirement); client.IgnoreNotFound(err) != nil {
			return pods.APIError(fmt.Errorf("deleting stale requirement %s: %w", requirement.Name, err))
		}
	}

	return nil
}

// DiscoverCRDs aggregates all mounted profiles. The first occurrence of a CRD
// wins, ordered by image volume name and then by profile name.
func DiscoverCRDs(root string) (map[string]*apiextensionsv1.CustomResourceDefinition, error) {
	crds := map[string]*apiextensionsv1.CustomResourceDefinition{}

	type profileManifests struct{ name, path string }

	manifests := []profileManifests{}

	volumes, err := os.ReadDir(root) // ReadDir sorts entries by name.
	if err != nil {
		return nil, fmt.Errorf("reading provider image mounts: %w", err)
	}

	for _, volume := range volumes {
		if !volume.IsDir() {
			continue
		}

		dir := filepath.Join(root, volume.Name(), "capi-operator-manifests")

		profiles, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("reading profiles for %s: %w", volume.Name(), err)
		}

		// Profiles may contain different CRDs. Assessing only the installed
		// profile would leave part of the target provider image unchecked.
		for _, profile := range profiles {
			if !profile.IsDir() {
				continue
			}

			path := filepath.Join(dir, profile.Name(), "manifests.yaml")
			manifests = append(manifests, profileManifests{name: profile.Name(), path: path})
		}
	}

	if len(manifests) == 0 {
		// An unexpected image layout is not evidence of an empty CRD set.
		return nil, errNoProfiles
	}

	// Deterministic precedence keeps duplicate CRD selection stable across
	// restarts, even when different profiles contain different definitions.
	sort.SliceStable(manifests, func(i, j int) bool { return manifests[i].name < manifests[j].name })

	for _, profile := range manifests {
		if err := readCRDs(profile.path, crds); err != nil {
			return nil, err
		}
	}

	return crds, nil
}

func readCRDs(path string, crds map[string]*apiextensionsv1.CustomResourceDefinition) error {
	f, err := os.Open(path) //nolint:gosec // path constructed from trusted image mount directories
	if err != nil {
		return fmt.Errorf("opening profile manifests: %w", err)
	}
	defer func() { _ = f.Close() }()

	decoder := yaml.NewYAMLOrJSONDecoder(f, 4096)

	for {
		obj := &unstructured.Unstructured{}
		// Fail the whole assessment on malformed YAML rather than silently
		// dropping a document that might define a required CRD.
		if err := decoder.Decode(obj); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return fmt.Errorf("parsing %s: %w", path, err)
		}

		if obj.GetKind() != "CustomResourceDefinition" {
			continue
		}

		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, crd); err != nil {
			return fmt.Errorf("decoding CRD in %s: %w", path, err)
		}

		if crd.Name == "" {
			return fmt.Errorf("%w in %s", errCRDNameMissing, path)
		}

		if _, exists := crds[crd.Name]; !exists {
			crds[crd.Name] = crd
		}
	}
}

// BuildRequirement constructs an observation-only requirement for every served version.
func BuildRequirement(crd *apiextensionsv1.CustomResourceDefinition) (*openshiftapiextensionsv1.CompatibilityRequirement, error) {
	data, err := k8syaml.Marshal(crd)
	if err != nil {
		return nil, fmt.Errorf("encoding CRD %s: %w", crd.Name, err)
	}

	return &openshiftapiextensionsv1.CompatibilityRequirement{
		// Server side apply requires the applied manifest to be self-describing.
		TypeMeta:   metav1.TypeMeta{APIVersion: openshiftapiextensionsv1.GroupVersion.String(), Kind: "CompatibilityRequirement"},
		ObjectMeta: metav1.ObjectMeta{Name: RequirementName(crd.Name), Annotations: adoptionAnnotations(crd)},
		Spec: openshiftapiextensionsv1.CompatibilityRequirementSpec{
			CompatibilitySchema: openshiftapiextensionsv1.CompatibilitySchema{
				CustomResourceDefinition: openshiftapiextensionsv1.CRDData{Type: openshiftapiextensionsv1.CRDDataTypeYAML, Data: string(data)},
				// Preflight must cover every API version clients can still use,
				// not just the version used to store objects.
				RequiredVersions: openshiftapiextensionsv1.APIVersions{DefaultSelection: openshiftapiextensionsv1.APIVersionSetTypeAllServed},
			},
			// Observe incompatibility without preventing CRD changes needed for
			// remediation. Upgradeable, not this admission policy, blocks upgrades.
			CustomResourceDefinitionSchemaValidation: openshiftapiextensionsv1.CustomResourceDefinitionSchemaValidation{Action: openshiftapiextensionsv1.CRDAdmitActionWarn},
		},
	}, nil
}

// adoptionAnnotations carries the installer's adopt-existing declaration from
// the assessed CRD manifest onto the requirement. The installer strips the
// annotation from the objects it applies, so a live CRD never carries it: the
// target payload's intent is only observable where its manifests are read.
func adoptionAnnotations(crd *apiextensionsv1.CustomResourceDefinition) map[string]string {
	if crd.Annotations[revisiongenerator.AdoptExistingAnnotation] != revisiongenerator.AdoptExistingAlways {
		return nil
	}

	return map[string]string{
		revisiongenerator.AdoptExistingAnnotation: revisiongenerator.AdoptExistingAlways,
	}
}

// RequirementName returns the deterministic name of the requirement that
// assesses crdName. The human readable form is used unless it exceeds the
// maximum length permitted for a resource name.
func RequirementName(crdName string) string {
	name := RequirementNamePrefix + crdName
	if len(name) <= validation.DNS1123SubdomainMaxLength {
		return name
	}

	// Truncating would make distinct CRD names collide, and shortening their DNS
	// labels would change them, so the name is replaced by its hash instead.
	return fmt.Sprintf("%s%x", RequirementNamePrefix, sha256.Sum256([]byte(crdName)))
}
