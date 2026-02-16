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
package providerimages

import (
	"context"
	"fmt"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// getImageRegistryMirrors lists IDMS and ICSP resources from the cluster and
// builds a map of source registries/repos to their mirror equivalents. If a
// CRD is not installed on the cluster, it is silently skipped. Unsupported
// wildcard sources (e.g. *.redhat.io) are removed from the map and returned
// separately so the caller can log them.
func getImageRegistryMirrors(ctx context.Context, c client.Reader) (mirrors map[string][]string, skippedWildcards []string, err error) {
	mirrors = make(map[string][]string)

	idmsMirrors, err := getIDMSMirrors(ctx, c)
	if err != nil && !meta.IsNoMatchError(err) {
		return nil, nil, err
	}

	for k, v := range idmsMirrors {
		mirrors[k] = append(mirrors[k], v...)
	}

	icspMirrors, err := getICSPMirrors(ctx, c)
	if err != nil && !meta.IsNoMatchError(err) {
		return nil, nil, err
	}

	for k, v := range icspMirrors {
		mirrors[k] = append(mirrors[k], v...)
	}

	for source := range mirrors {
		if strings.HasPrefix(source, "*.") {
			skippedWildcards = append(skippedWildcards, source)
			delete(mirrors, source)
		}
	}

	return mirrors, skippedWildcards, nil
}

// getIDMSMirrors lists ImageDigestMirrorSet resources and returns a map of
// source repositories to their mirror locations.
func getIDMSMirrors(ctx context.Context, c client.Reader) (map[string][]string, error) {
	var list configv1.ImageDigestMirrorSetList
	if err := c.List(ctx, &list); err != nil {
		return nil, fmt.Errorf("failed to list ImageDigestMirrorSets: %w", err)
	}

	mirrors := make(map[string][]string)

	for _, idms := range list.Items {
		for _, mirror := range idms.Spec.ImageDigestMirrors {
			for _, m := range mirror.Mirrors {
				mirrors[mirror.Source] = append(mirrors[mirror.Source], string(m))
			}
		}
	}

	return mirrors, nil
}

// getICSPMirrors lists ImageContentSourcePolicy resources and returns a map of
// source repositories to their mirror locations.
func getICSPMirrors(ctx context.Context, c client.Reader) (map[string][]string, error) {
	var list operatorv1alpha1.ImageContentSourcePolicyList
	if err := c.List(ctx, &list); err != nil {
		return nil, fmt.Errorf("failed to list ImageContentSourcePolicies: %w", err)
	}

	mirrors := make(map[string][]string)

	for _, icsp := range list.Items {
		for _, rdm := range icsp.Spec.RepositoryDigestMirrors {
			mirrors[rdm.Source] = append(mirrors[rdm.Source], rdm.Mirrors...)
		}
	}

	return mirrors, nil
}

// sourceMatchesRef reports whether source is a prefix of ref at a path
// boundary. The character immediately after the source prefix must be '/', '@',
// or the strings must match exactly.
//
// Note: ':' is intentionally not a valid boundary. The upstream containers/image
// refMatchingPrefix treats ':' as a boundary but considers this a bug:
// https://github.com/containers/image/blob/main/pkg/sysregistriesv2/system_registries_v2.go#L900-L904
// Our prefix-replacement approach would produce invalid refs if ':' were matched
// (e.g. mirror.io:6000 + :5000/image = mirror.io:6000:5000/image).
func sourceMatchesRef(ref, source string) bool {
	if !strings.HasPrefix(ref, source) {
		return false
	}

	if len(ref) == len(source) {
		return true
	}

	nextChar := ref[len(source)]

	// This is the boundary check. We must match on / or @
	return nextChar == '/' || nextChar == '@'
}

// resolveImageRef rewrites an image reference using the first matching mirror.
// Uses longest-prefix matching to find the most specific source.
// Returns the rewritten ref if a mirror matches, or the original ref unchanged.
func resolveImageRef(imageRef string, mirrors map[string][]string) string {
	if len(mirrors) == 0 {
		return imageRef
	}

	var bestSource string

	for source := range mirrors {
		if !sourceMatchesRef(imageRef, source) {
			continue
		}

		if len(source) > len(bestSource) {
			bestSource = source
		}
	}

	if bestSource == "" || len(mirrors[bestSource]) == 0 {
		return imageRef
	}

	suffix := strings.TrimPrefix(imageRef, bestSource)

	// We can support falling back e.g "quay.io/openshift" => mirrors:
	// ["mirror-b.local/openshift", "mirror-c.local/openshift"] ut here we're
	// assuming mirrors are correctly configured, so for simplicity's
	// sake we're only taking the first.
	return mirrors[bestSource][0] + suffix
}
