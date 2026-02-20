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
	"fmt"
	"slices"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8syaml "sigs.k8s.io/yaml"

	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
)

const (
	revisionContentIDLen = 8
	// maxRevisionNameLen is the maximum length of the revision name field specified in the API schema.
	maxRevisionNameLen = 255
)

type RenderedRevision interface {
	ContentID() (string, error)
	ToAPIRevision(releaseVersion string, revisionIndex int64) (operatorv1alpha1.ClusterAPIInstallerRevision, error)
}

type renderedRevision struct {
	components []*renderedComponent
	contentID  string
}

var _ RenderedRevision = &renderedRevision{}

func NewRenderedRevision(profiles []providerimages.ProviderImageManifests) (RenderedRevision, error) {
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

func (r *renderedRevision) ToAPIRevision(releaseVersion string, revisionIndex int64) (operatorv1alpha1.ClusterAPIInstallerRevision, error) {
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
		Name:       buildRevisionName(releaseVersion, contentID, revisionIndex),
		Revision:   revisionIndex,
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

	name := fmt.Sprintf("%s-%s-%d", releaseVersion, shortContentID, index)

	// Truncate if necessary
	if len(name) > maxRevisionNameLen {
		name = name[:maxRevisionNameLen]
	}

	return operatorv1alpha1.RevisionName(name)
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
