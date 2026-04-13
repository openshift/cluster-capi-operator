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
// CRD is not installed on the cluster, it is silently skipped. Wildcard
// sources (e.g. *.redhat.io) are included in the map and handled by
// resolveImageRef.
func getImageRegistryMirrors(ctx context.Context, c client.Reader) (map[string][]string, error) {
	mirrors := make(map[string][]string)

	idmsMirrors, err := getIDMSMirrors(ctx, c)
	if err != nil && !meta.IsNoMatchError(err) {
		return nil, err
	}

	for k, v := range idmsMirrors {
		mirrors[k] = append(mirrors[k], v...)
	}

	icspMirrors, err := getICSPMirrors(ctx, c)
	if err != nil && !meta.IsNoMatchError(err) {
		return nil, err
	}

	for k, v := range icspMirrors {
		mirrors[k] = append(mirrors[k], v...)
	}

	return mirrors, nil
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

// extractHostname splits an image reference into the registry hostname (with
// port, if present) and the remainder (path and digest/tag).
func extractHostname(ref string) (hostname, remainder string) {
	slashIdx := strings.Index(ref, "/")
	if slashIdx == -1 {
		return ref, ""
	}

	return ref[:slashIdx], ref[slashIdx:]
}

// hostnameWithoutPort strips the port from a registry hostname.
func hostnameWithoutPort(hostname string) string {
	colonIdx := strings.LastIndex(hostname, ":")
	if colonIdx == -1 {
		return hostname
	}

	return hostname[:colonIdx]
}

// wildcardMatchesRef reports whether a wildcard source (e.g. *.redhat.io)
// matches the hostname of an image reference. The wildcard requires at least
// one subdomain — *.redhat.io matches registry.redhat.io but not redhat.io.
// Ports in the ref's hostname are stripped before matching.
func wildcardMatchesRef(ref, wildcardSource string) bool {
	suffix := strings.TrimPrefix(wildcardSource, "*")

	hostname, _ := extractHostname(ref)
	domain := hostnameWithoutPort(hostname)

	return strings.HasSuffix(domain, suffix)
}

// resolveImageRef rewrites an image reference using the first matching mirror.
// Literal prefix matches are tried first (longest wins). If no literal match
// is found, wildcard sources (*.domain) are tried (longest wins). Wildcard
// replacement swaps the ref's hostname with the mirror, preserving the path.
// Returns the rewritten ref and the matched source, or the original ref with
// an empty source if no mirror matches.
func resolveImageRef(imageRef string, mirrors map[string][]string) (resolved string, matchedSource string) {
	if len(mirrors) == 0 {
		return imageRef, ""
	}

	// Literal prefix matching (most specific wins).
	var bestSource string

	for source := range mirrors {
		if strings.HasPrefix(source, "*.") {
			continue
		}

		if !sourceMatchesRef(imageRef, source) {
			continue
		}

		if len(source) > len(bestSource) {
			bestSource = source
		}
	}

	if bestSource != "" && len(mirrors[bestSource]) > 0 {
		suffix := strings.TrimPrefix(imageRef, bestSource)
		return mirrors[bestSource][0] + suffix, bestSource
	}

	// Wildcard matching (longest wildcard wins).
	var bestWildcard string

	for source := range mirrors {
		if !strings.HasPrefix(source, "*.") {
			continue
		}

		if !wildcardMatchesRef(imageRef, source) {
			continue
		}

		if len(source) > len(bestWildcard) {
			bestWildcard = source
		}
	}

	if bestWildcard != "" && len(mirrors[bestWildcard]) > 0 {
		_, remainder := extractHostname(imageRef)
		return mirrors[bestWildcard][0] + remainder, bestWildcard
	}

	return imageRef, ""
}
