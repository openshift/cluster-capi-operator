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

package revisiongenerator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	operatorv1alpha1ac "github.com/openshift/client-go/operator/applyconfigurations/operator/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	k8syaml "sigs.k8s.io/yaml"

	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const (
	revisionContentIDLen = 8
	// maxRevisionNameLen is the maximum length of the revision name field specified in the API schema.
	maxRevisionNameLen = 255
)

var (
	errProviderProfileNotFound = errors.New("no provider profile found for component")
	errContentIDMismatch       = errors.New("content ID mismatch")
)

// RenderedRevision represents a set of components whose manifests have been
// fully rendered and are ready to be applied to a cluster.
type RenderedRevision interface {
	// ContentID returns a unique identifier for the revision's content.
	ContentID() (string, error)

	// Components returns the rendered components for this revision.
	Components() []RenderedComponent

	// ForInstall creates an InstallerRevision by assigning a release version
	// and revision index to this rendered content.
	ForInstall(releaseVersion string, revisionIndex int64) (InstallerRevision, error)
}

// InstallerRevision is a RenderedRevision that has been assigned a revision
// identity (name and index), making it ready for installation or conversion
// to an API revision.
type InstallerRevision interface {
	RenderedRevision

	// RevisionName returns the name of this revision.
	RevisionName() operatorv1alpha1.RevisionName

	// RevisionIndex returns the revision index.
	RevisionIndex() int64

	// ToAPIRevision converts this revision to an API revision.
	ToAPIRevision() (operatorv1alpha1.ClusterAPIInstallerRevision, error)
}

// RenderedComponent represents a single provider component with manifests
// separated into CRDs and other objects.
type RenderedComponent interface {
	// Name returns the component name.
	Name() string
	// CRDs returns the CRD objects for this component.
	CRDs() []client.Object
	// Objects returns the non-CRD objects for this component.
	Objects() []client.Object
}

type renderedRevision struct {
	components []*renderedComponent
	contentID  string
}

var _ RenderedRevision = &renderedRevision{}

// NewRenderedRevision creates a new RenderedRevision from a list of provider image manifests.
func NewRenderedRevision(profiles []providerimages.ProviderImageManifests) (RenderedRevision, error) {
	return newRenderedRevision(profiles)
}

func newRenderedRevision(profiles []providerimages.ProviderImageManifests) (*renderedRevision, error) {
	components := make([]*renderedComponent, len(profiles))

	for i, profile := range profiles {
		component, err := newRenderedComponent(&profile)
		if err != nil {
			return nil, err
		}

		components[i] = component
	}

	return &renderedRevision{components: components}, nil
}

// ContentID returns a unique identifier for the revision's content.
// Specifically it returns a SHA256 over all manifests, but callers MUST NOT
// assume this.
func (r *renderedRevision) ContentID() (string, error) {
	if r.contentID == "" {
		h := sha256.New()

		for _, component := range r.components {
			contentID, err := component.contentID()
			if err != nil {
				return "", fmt.Errorf("error getting content ID: %w", err)
			}

			h.Write([]byte(contentID))
		}

		r.contentID = hex.EncodeToString(h.Sum(nil))
	}

	return r.contentID, nil
}

// Components returns the rendered components for this revision.
func (r *renderedRevision) Components() []RenderedComponent {
	return util.SliceMap(r.components, func(c *renderedComponent) RenderedComponent {
		return c
	})
}

// ForInstall creates an InstallerRevision by assigning a release version and
// revision index to this rendered content.
func (r *renderedRevision) ForInstall(releaseVersion string, revisionIndex int64) (InstallerRevision, error) {
	contentID, err := r.ContentID()
	if err != nil {
		return nil, fmt.Errorf("error calculating contentID: %w", err)
	}

	return &installerRevision{
		renderedRevision: r,
		revisionName:     buildRevisionName(releaseVersion, contentID, revisionIndex),
		revisionIndex:    revisionIndex,
	}, nil
}

// installerRevision is a renderedRevision that has been assigned a revision
// identity (name and index).
type installerRevision struct {
	*renderedRevision
	revisionName  operatorv1alpha1.RevisionName
	revisionIndex int64
}

var _ InstallerRevision = &installerRevision{}

// RevisionName returns the name of this revision.
func (r *installerRevision) RevisionName() operatorv1alpha1.RevisionName {
	return r.revisionName
}

// RevisionIndex returns the revision index.
func (r *installerRevision) RevisionIndex() int64 {
	return r.revisionIndex
}

// ForInstall returns the receiver since it already has an identity.
func (r *installerRevision) ForInstall(_ string, _ int64) (InstallerRevision, error) {
	return r, nil
}

// ToAPIRevision converts this revision to an API revision.
func (r *installerRevision) ToAPIRevision() (operatorv1alpha1.ClusterAPIInstallerRevision, error) {
	apiComponents := make([]operatorv1alpha1.ClusterAPIInstallerComponent, len(r.components))
	for i, component := range r.components {
		apiComponents[i] = operatorv1alpha1.ClusterAPIInstallerComponent{
			Type: operatorv1alpha1.InstallerComponentTypeImage,
			Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
				Ref:     operatorv1alpha1.ImageDigestFormat(component.imageRef),
				Profile: component.profile,
			},
		}
	}

	contentID, err := r.ContentID()
	if err != nil {
		return operatorv1alpha1.ClusterAPIInstallerRevision{}, fmt.Errorf("error calculating contentID: %w", err)
	}

	return operatorv1alpha1.ClusterAPIInstallerRevision{
		Name:       r.revisionName,
		Revision:   r.revisionIndex,
		ContentID:  contentID,
		Components: apiComponents,
	}, nil
}

// buildRevisionName constructs a revision name from version, contentID, and revision index.
func buildRevisionName(releaseVersion, contentID string, index int64) operatorv1alpha1.RevisionName {
	// Format: <version>-<contentID[:8]>-<number>
	shortContentID := contentID
	if len(shortContentID) > revisionContentIDLen {
		shortContentID = shortContentID[:revisionContentIDLen]
	}

	// Truncate release version if necessary to fit in the revision name.
	// This ensures we keep the unique suffix if we ever have to truncate.
	suffix := fmt.Sprintf("-%s-%d", shortContentID, index)

	maxVersionLen := max(0, maxRevisionNameLen-len(suffix))
	if len(releaseVersion) > maxVersionLen {
		releaseVersion = releaseVersion[:maxVersionLen]
	}

	name := releaseVersion + suffix

	return operatorv1alpha1.RevisionName(name)
}

// NewInstallerRevisionFromAPI creates an InstallerRevision by matching the
// components in an API revision against the provided provider profiles and
// rendering the matched manifests. The revision name and index are taken
// directly from the API revision (not recomputed). Components are matched by
// Image.Ref and Image.Profile. An error is returned if any component cannot
// be found in the provided profiles, or if the rendered content ID does not
// match the content ID recorded in the API revision.
func NewInstallerRevisionFromAPI(
	apiRev operatorv1alpha1.ClusterAPIInstallerRevision,
	providerProfiles []providerimages.ProviderImageManifests,
) (InstallerRevision, error) {
	matched := make([]providerimages.ProviderImageManifests, len(apiRev.Components))

	for i, component := range apiRev.Components {
		found := false

		for _, profile := range providerProfiles {
			if operatorv1alpha1.ImageDigestFormat(profile.ImageRef) == component.Image.Ref &&
				profile.Profile == component.Image.Profile {
				matched[i] = profile
				found = true

				break
			}
		}

		if !found {
			return nil, fmt.Errorf("%w %d (image=%s, profile=%s)",
				errProviderProfileNotFound, i, component.Image.Ref, component.Image.Profile)
		}
	}

	rendered, err := newRenderedRevision(matched)
	if err != nil {
		return nil, err
	}

	// Validate that the rendered content ID matches the API revision content ID.
	if contentID, err := rendered.ContentID(); err != nil {
		return nil, fmt.Errorf("error computing content ID: %w", err)
	} else if contentID != apiRev.ContentID {
		return nil, fmt.Errorf("%w: rendered revision has content ID %s, but API revision specifies %s",
			errContentIDMismatch, contentID, apiRev.ContentID)
	}

	return &installerRevision{
		renderedRevision: rendered,
		revisionName:     apiRev.Name,
		revisionIndex:    apiRev.Revision,
	}, nil
}

type renderedComponent struct {
	name     string
	imageRef string
	profile  string

	crds    []unstructured.Unstructured
	objects []unstructured.Unstructured
}

func newRenderedComponent(providerProfile *providerimages.ProviderImageManifests) (*renderedComponent, error) {
	component := &renderedComponent{
		name:     providerProfile.Name,
		imageRef: providerProfile.ImageRef,
		profile:  providerProfile.Profile,
	}

	for yaml, err := range providerProfile.Manifests() {
		if err != nil {
			return nil, fmt.Errorf("error reading manifests: %w", err)
		}

		yaml, err = transformYaml(providerProfile, yaml)
		if err != nil {
			return nil, fmt.Errorf("error transforming manifest yaml: %w", err)
		}

		var unstructured unstructured.Unstructured
		if err := k8syaml.Unmarshal([]byte(yaml), &unstructured); err != nil {
			return nil, fmt.Errorf("error unmarshalling transformed manifest: %w", err)
		}

		switch unstructured.GroupVersionKind().GroupKind() {
		case schema.GroupKind{Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"}:
			component.crds = append(component.crds, unstructured)
		default:
			component.objects = append(component.objects, unstructured)
		}
	}

	return component, nil
}

var _ RenderedComponent = &renderedComponent{}

// Name returns the component name.
func (c *renderedComponent) Name() string {
	return c.name
}

// CRDs returns the CRD objects for this component.
func (c *renderedComponent) CRDs() []client.Object {
	return util.SliceMap(c.crds, func(crd unstructured.Unstructured) client.Object {
		return &crd
	})
}

// Objects returns the non-CRD objects for this component.
func (c *renderedComponent) Objects() []client.Object {
	return util.SliceMap(c.objects, func(obj unstructured.Unstructured) client.Object {
		return &obj
	})
}

func (c *renderedComponent) contentID() (string, error) {
	h := sha256.New()

	for _, obj := range slices.Concat(c.crds, c.objects) {
		data, err := json.Marshal(obj.Object)
		if err != nil {
			return "", fmt.Errorf("error marshalling object: %w", err)
		}

		h.Write(data)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// RevisionsToApplyConfig converts a slice of ClusterAPIInstallerRevision to
// their apply configuration representations via JSON round-trip. Both types
// share identical JSON tags (code-generated from the same schema), so this
// conversion is lossless and maintenance-free.
func RevisionsToApplyConfig(revs []operatorv1alpha1.ClusterAPIInstallerRevision) ([]operatorv1alpha1ac.ClusterAPIInstallerRevisionApplyConfiguration, error) {
	data, err := json.Marshal(revs)
	if err != nil {
		return nil, fmt.Errorf("error marshalling revisions: %w", err)
	}

	var acs []operatorv1alpha1ac.ClusterAPIInstallerRevisionApplyConfiguration
	if err := json.Unmarshal(data, &acs); err != nil {
		return nil, fmt.Errorf("error unmarshalling revision apply configs: %w", err)
	}

	return acs, nil
}
