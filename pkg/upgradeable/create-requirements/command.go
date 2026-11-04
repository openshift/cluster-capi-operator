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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	openshiftapiextensionsv1 "github.com/openshift/api/apiextensions/v1"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"github.com/openshift/cluster-capi-operator/pkg/upgradeable"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	k8syaml "sigs.k8s.io/yaml"
)

var (
	errStaleImages    = errors.New("payload images ConfigMap does not match the assessment")
	errNoProfiles     = errors.New("no provider profiles discovered")
	errCRDNameMissing = errors.New("CRD has no name")
	errNameTooLong    = errors.New("requirement name exceeds 253 characters")
)

// Run executes the requirements pod entrypoint.
func Run(ctx context.Context, args []string) error {
	return upgradeable.Run(ctx, "upgradeable-create-requirements", args, func(ctx context.Context, c client.Client, current, target string) error {
		return Step(ctx, c, current, target, providerimages.ProviderImageMountBase)
	})
}

// Step discovers the complete CRD set before creating any requirements.
func Step(ctx context.Context, c client.Client, current, target, root string) error {
	cm := &corev1.ConfigMap{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: upgradeable.Namespace, Name: upgradeable.ImagesConfigMapName}, cm); err != nil {
		if apierrors.IsNotFound(err) {
			return upgradeable.Terminal(err)
		}

		return upgradeable.APIError(err)
	}

	assessmentLabels := upgradeable.AssessmentLabels(current, target)
	if !labels.SelectorFromSet(assessmentLabels).Matches(labels.Set(cm.Labels)) {
		return upgradeable.Terminal(errStaleImages)
	}

	images := sets.New[string]()
	for _, image := range cm.Data {
		images.Insert(image)
	}

	crds, err := DiscoverCRDs(root, sets.List(images))
	if err != nil {
		return upgradeable.Terminal(err)
	}

	owner, err := upgradeable.OwnerReference(ctx, c)
	if err != nil {
		return err
	}

	for _, name := range sets.List(sets.KeySet(crds)) {
		cr, err := BuildRequirement(crds[name])
		if err != nil {
			return upgradeable.Terminal(err)
		}

		cr.Labels = assessmentLabels

		cr.OwnerReferences = []metav1.OwnerReference{owner}
		if err := c.Create(ctx, cr); err != nil && !apierrors.IsAlreadyExists(err) {
			return upgradeable.APIError(fmt.Errorf("creating requirement %s: %w", cr.Name, err))
		}
	}

	return nil
}

// DiscoverCRDs aggregates all mounted profiles alphabetically. The first
// occurrence of a CRD wins; image reference order breaks equal profile names.
func DiscoverCRDs(root string, images []string) (map[string]*apiextensionsv1.CustomResourceDefinition, error) {
	crds := map[string]*apiextensionsv1.CustomResourceDefinition{}

	type profileManifests struct{ name, path string }

	manifests := []profileManifests{}

	for _, image := range images {
		dir := filepath.Join(root, providerimages.VolumeNameForImageRef(image), "capi-operator-manifests")

		profiles, err := os.ReadDir(dir) // ReadDir sorts entries by name.
		if err != nil {
			return nil, fmt.Errorf("reading profiles for %s: %w", image, err)
		}

		for _, profile := range profiles {
			if !profile.IsDir() {
				continue
			}

			path := filepath.Join(dir, profile.Name(), "manifests.yaml")
			manifests = append(manifests, profileManifests{name: profile.Name(), path: path})
		}
	}

	if len(manifests) == 0 {
		return nil, errNoProfiles
	}

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
	name := "capi-operator-upgradeable-" + crd.Name
	if len(name) > 253 {
		return nil, fmt.Errorf("%w: %s", errNameTooLong, name)
	}

	crd = crd.DeepCopy()
	apiextensionsv1.SetObjectDefaults_CustomResourceDefinition(crd)

	data, err := k8syaml.Marshal(crd)
	if err != nil {
		return nil, fmt.Errorf("encoding CRD %s: %w", crd.Name, err)
	}

	return &openshiftapiextensionsv1.CompatibilityRequirement{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: openshiftapiextensionsv1.CompatibilityRequirementSpec{
			CompatibilitySchema: openshiftapiextensionsv1.CompatibilitySchema{
				CustomResourceDefinition: openshiftapiextensionsv1.CRDData{Type: openshiftapiextensionsv1.CRDDataTypeYAML, Data: string(data)},
				RequiredVersions:         openshiftapiextensionsv1.APIVersions{DefaultSelection: openshiftapiextensionsv1.APIVersionSetTypeAllServed},
			},
			CustomResourceDefinitionSchemaValidation: openshiftapiextensionsv1.CustomResourceDefinitionSchemaValidation{Action: openshiftapiextensionsv1.CRDAdmitActionWarn},
		},
	}, nil
}
