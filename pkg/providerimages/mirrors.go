package providerimages

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// getImageRegistryMirrors lists IDMS and ICSP resources from the cluster and
// builds a map of source registries/repos to their mirror equivalents.
// If a CRD is not installed on the cluster, it is silently skipped.
func getImageRegistryMirrors(ctx context.Context, c client.Reader, log logr.Logger) (map[string][]string, error) {
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

	// Wildcard IDMS sources (e.g. *.redhat.io) are not supported for
	// provider image resolution. Warn and remove so they don't silently
	// fail to match.
	for source := range mirrors {
		if strings.HasPrefix(source, "*.") {
			log.Info("ignoring unsupported wildcard mirror source", "source", source)
			delete(mirrors, source)
		}
	}

	if len(mirrors) > 0 {
		log.Info("found image registry mirrors", "sourceCount", len(mirrors))
	} else {
		log.Info("no image registry mirrors found")
	}

	return mirrors, nil
}

// getIDMSMirrors lists ImageDigestMirrorSet resources and returns a map of
// source repositories to their mirror locations.
func getIDMSMirrors(ctx context.Context, c client.Reader) (map[string][]string, error) {
	var list configv1.ImageDigestMirrorSetList
	if err := c.List(ctx, &list); err != nil {
		return nil, err
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
		return nil, err
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
	return mirrors[bestSource][0] + suffix
}
