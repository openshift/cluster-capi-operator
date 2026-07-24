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
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	operatorv1alpha1ac "github.com/openshift/client-go/operator/applyconfigurations/operator/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"
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

// ParsedRevision represents a set of components whose manifests have been
// parsed from provider image manifests and are ready to be installed.
type ParsedRevision interface {
	// ContentID returns a unique identifier for the revision's content.
	ContentID() (string, error)

	// Components returns the parsed components for this revision.
	Components() []ParsedComponent

	// ForInstall creates an InstallerRevision by assigning a release version
	// and revision index to this parsed content.
	ForInstall(releaseVersion string, revisionIndex int64) (InstallerRevision, error)

	// ManifestSubstitutions returns a copy of the substitutions stored in
	// this revision. These are used by install-time transformers to expand
	// envsubst variables in manifests.
	ManifestSubstitutions() map[string]string
}

// InstallerRevision is a ParsedRevision that has been assigned a revision
// identity (name and index), making it ready for installation or conversion
// to an API revision.
type InstallerRevision interface {
	ParsedRevision

	// RevisionName returns the name of this revision.
	RevisionName() operatorv1alpha1.RevisionName

	// RevisionIndex returns the revision index.
	RevisionIndex() int64

	// ToAPIRevision converts this revision to an API revision.
	ToAPIRevision() (operatorv1alpha1.ClusterAPIInstallerRevision, error)
}

// ParsedComponent represents a single provider component with its manifests
// parsed and ready to be applied.
type ParsedComponent interface {
	// Name returns the component name.
	Name() string
	// Objects returns all objects for this component, including CRDs.
	Objects() []client.Object
}

type parsedRevision struct {
	components    []*parsedComponent
	contentID     string
	substitutions map[string]string
}

var _ ParsedRevision = &parsedRevision{}

// NewParsedRevision creates a new ParsedRevision from a list of provider image manifests.
func NewParsedRevision(profiles []providerimages.ProviderImageManifests, opts ...revisionRenderOption) (ParsedRevision, error) {
	return newParsedRevision(profiles, opts...)
}

// newParsedRevision implements NewParsedRevision. It exists to return a
// concrete type for internal use.
func newParsedRevision(profiles []providerimages.ProviderImageManifests, opts ...revisionRenderOption) (*parsedRevision, error) {
	cfg := &revisionRenderConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	components := make([]*parsedComponent, len(profiles))

	for i, profile := range profiles {
		component, err := newParsedComponent(&profile, cfg)
		if err != nil {
			return nil, err
		}

		components[i] = component
	}

	rev := &parsedRevision{
		components:    components,
		substitutions: maps.Clone(cfg.substitutions),
	}

	return rev, nil
}

// substitutionsFromMap converts a map to a sorted slice of API substitutions.
func substitutionsFromMap(m map[string]string) []operatorv1alpha1.ClusterAPIInstallerRevisionManifestSubstitution {
	if len(m) == 0 {
		return nil
	}

	subs := make([]operatorv1alpha1.ClusterAPIInstallerRevisionManifestSubstitution, 0, len(m))
	for k, v := range m {
		subs = append(subs, operatorv1alpha1.ClusterAPIInstallerRevisionManifestSubstitution{
			Key:   k,
			Value: &v,
		})
	}

	slices.SortFunc(subs, func(a, b operatorv1alpha1.ClusterAPIInstallerRevisionManifestSubstitution) int {
		return cmp.Compare(a.Key, b.Key)
	})

	return subs
}

// ManifestSubstitutions returns a copy of the substitutions stored in this revision.
func (r *parsedRevision) ManifestSubstitutions() map[string]string {
	return maps.Clone(r.substitutions)
}

// ContentID returns a unique identifier for the revision's content.
// Specifically it returns a SHA256 over all manifests and substitutions,
// but callers MUST NOT assume this.
func (r *parsedRevision) ContentID() (string, error) {
	if r.contentID == "" {
		h := sha256.New()

		for _, component := range r.components {
			contentID, err := component.contentID()
			if err != nil {
				return "", fmt.Errorf("error getting content ID: %w", err)
			}

			h.Write([]byte(contentID))
		}

		// Include substitutions in the hash so that different substitutions
		// always produce a different content ID and therefore trigger a new
		// revision, even if they were not used. This is not strictly necessary,
		// but it should reduce operator confusion if old but unused
		// substitutions continued to be listed in the current revision.
		//
		// Normalise nil to an empty map before marshalling so that a nil
		// substitutions map and an empty one produce the same hash, regardless
		// of how the revision was constructed.
		subs := r.substitutions
		if subs == nil {
			subs = map[string]string{}
		}

		if data, err := json.Marshal(subs); err == nil {
			h.Write(data)
		} else {
			return "", fmt.Errorf("error marshalling substitutions: %w", err)
		}

		r.contentID = hex.EncodeToString(h.Sum(nil))
	}

	return r.contentID, nil
}

// Components returns the parsed components for this revision.
func (r *parsedRevision) Components() []ParsedComponent {
	return util.SliceMap(r.components, func(c *parsedComponent) ParsedComponent {
		return c
	})
}

// ForInstall creates an InstallerRevision by assigning a release version and
// revision index to this parsed content.
func (r *parsedRevision) ForInstall(releaseVersion string, revisionIndex int64) (InstallerRevision, error) {
	contentID, err := r.ContentID()
	if err != nil {
		return nil, fmt.Errorf("error calculating contentID: %w", err)
	}

	return &installerRevision{
		parsedRevision: r,
		revisionName:   buildRevisionName(releaseVersion, contentID, revisionIndex),
		revisionIndex:  revisionIndex,
	}, nil
}

// installerRevision is a parsedRevision that has been assigned a revision
// identity (name and index).
type installerRevision struct {
	*parsedRevision
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
			Name: component.name,
			ClusterAPIInstallerComponentSource: operatorv1alpha1.ClusterAPIInstallerComponentSource{
				Type: operatorv1alpha1.InstallerComponentTypeImage,
				Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
					Ref:     operatorv1alpha1.ImageDigestFormat(component.imageRef),
					Profile: component.profile,
				},
			},
		}
	}

	contentID, err := r.ContentID()
	if err != nil {
		return operatorv1alpha1.ClusterAPIInstallerRevision{}, fmt.Errorf("error calculating contentID: %w", err)
	}

	return operatorv1alpha1.ClusterAPIInstallerRevision{
		Name:                  r.revisionName,
		Revision:              r.revisionIndex,
		ContentID:             contentID,
		ManifestSubstitutions: substitutionsFromMap(r.substitutions),
		Components:            apiComponents,
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

type revisionRenderConfig struct {
	objectCollectors []RevisionObjectCollector
	substitutions    map[string]string
}

type revisionRenderOption func(*revisionRenderConfig)

// RevisionObjectCollector is a function that will be called for each object in
// the parsed revision.
type RevisionObjectCollector func(obj unstructured.Unstructured)

// WithObjectCollectors adds object collectors to the revision render config.
func WithObjectCollectors(collectors ...RevisionObjectCollector) revisionRenderOption {
	return func(opts *revisionRenderConfig) {
		opts.objectCollectors = append(opts.objectCollectors, collectors...)
	}
}

// WithManifestSubstitutions adds envsubst-style substitutions that will be
// recorded on the revision and applied at install time . When called multiple
// times, later values merge with and override earlier ones.
func WithManifestSubstitutions(subs map[string]string) revisionRenderOption {
	return func(opts *revisionRenderConfig) {
		if opts.substitutions == nil {
			opts.substitutions = make(map[string]string, len(subs))
		}

		maps.Copy(opts.substitutions, subs)
	}
}

// NewInstallerRevisionFromAPI creates an InstallerRevision by matching the
// components in an API revision against the provided provider profiles and
// rendering the matched manifests. The revision name and index are taken
// directly from the API revision. Components are matched by Image.Ref and
// Image.Profile. An error is returned if any component cannot be found in the
// provided profiles, or if the parsed content ID does not match the content
// ID recorded in the API revision.
func NewInstallerRevisionFromAPI(
	apiRev operatorv1alpha1.ClusterAPIInstallerRevision,
	providerProfiles []providerimages.ProviderImageManifests,
	opts ...revisionRenderOption,
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

	apiSubs := make(map[string]string, len(apiRev.ManifestSubstitutions))
	for _, s := range apiRev.ManifestSubstitutions {
		apiSubs[s.Key] = ptr.Deref(s.Value, "")
	}

	opts = append(opts, WithManifestSubstitutions(apiSubs))

	parsed, err := newParsedRevision(matched, opts...)
	if err != nil {
		return nil, err
	}

	// Validate that the parsed content ID matches the API revision content ID.
	if contentID, err := parsed.ContentID(); err != nil {
		return nil, fmt.Errorf("error computing content ID: %w", err)
	} else if contentID != apiRev.ContentID {
		return nil, fmt.Errorf("%w: parsed revision has content ID %s, but API revision specifies %s",
			errContentIDMismatch, contentID, apiRev.ContentID)
	}

	return &installerRevision{
		parsedRevision: parsed,
		revisionName:   apiRev.Name,
		revisionIndex:  apiRev.Revision,
	}, nil
}

type parsedComponent struct {
	name     string
	imageRef string
	profile  string

	objects []unstructured.Unstructured
}

func newParsedComponent(providerProfile *providerimages.ProviderImageManifests, cfg *revisionRenderConfig) (*parsedComponent, error) {
	component := &parsedComponent{
		name:     providerProfile.Name,
		imageRef: providerProfile.ImageRef,
		profile:  providerProfile.Profile,
	}

	for yaml, err := range providerProfile.Manifests() {
		if err != nil {
			return nil, fmt.Errorf("error reading manifests: %w", err)
		}

		// Replace SelfImageRef with the actual image ref before unmarshalling.
		if providerProfile.SelfImageRef != "" {
			yaml = strings.ReplaceAll(yaml, providerProfile.SelfImageRef, providerProfile.ImageRef)
		}

		var obj unstructured.Unstructured
		if err := k8syaml.Unmarshal([]byte(yaml), &obj.Object); err != nil {
			return nil, fmt.Errorf("error unmarshalling manifest: %w", err)
		}

		for _, collector := range cfg.objectCollectors {
			collector(obj)
		}

		component.objects = append(component.objects, obj)
	}

	return component, nil
}

var _ ParsedComponent = &parsedComponent{}

// Name returns the component name.
func (c *parsedComponent) Name() string {
	return c.name
}

// Objects returns all objects for this component, including CRDs.
func (c *parsedComponent) Objects() []client.Object {
	return util.SliceMap(c.objects, func(obj unstructured.Unstructured) client.Object {
		return &obj
	})
}

func (c *parsedComponent) contentID() (string, error) {
	h := sha256.New()

	for _, obj := range c.objects {
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
