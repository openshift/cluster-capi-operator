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
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	imageConfigName = "cluster"

	// openshiftConfigNamespace is the namespace where user-provided
	// configuration objects live (pull-secret, image config CA bundles, etc.).
	openshiftConfigNamespace = "openshift-config"

	// trustedCABundleName is the CNO-managed ConfigMap containing the merged
	// trust bundle (system CAs + proxy/install-time CAs). This is the same
	// data that gets injected into ConfigMaps labeled with
	// config.openshift.io/inject-trusted-cabundle=true.
	trustedCABundleName      = "trusted-ca-bundle"
	trustedCABundleNamespace = "openshift-config-managed"
)

var errUnexpectedTransportType = errors.New("unexpected DefaultTransport type, expected *http.Transport")

// getTrustedCATransport builds a cert pool from two sources and returns an
// http.RoundTripper configured to trust those CAs in addition to the system
// CAs. This is intended to be used to get the required CAs to pull images
// from mirror registries, where the default pod/system CAs may not work.
//
// Source 1: openshift-config-managed/trusted-ca-bundle — the CNO-managed
// merged trust bundle containing system CAs plus install-time CAs from
// additionalTrustBundle (via proxy.config). This is the primary source for
// disconnected clusters; AWS and agent-based installs configure CAs here,
// not in image.config.
//
// Source 2: image.config.openshift.io/cluster additionalTrustedCA — a Day 2
// field referencing a ConfigMap in openshift-config with registry-specific
// CAs that may not be in the proxy trust chain.
//
// Returns remote.DefaultTransport when no additional CAs are found (the
// common case on connected clusters).
func getTrustedCATransport(ctx context.Context, c client.Reader, log logr.Logger) (http.RoundTripper, error) {
	// Start from the system cert pool as a baseline. The CNO trust bundle
	// already contains system CAs so these will be duplicated, but
	// x509.CertPool deduplicates internally (by SHA-224 of cert.Raw) and
	// this ensures we never lose system trust if the CNO bundle is absent.
	//
	// SystemCertPool only errors on active failures (permission denied, I/O
	// errors) — missing cert files return an empty pool with nil error. When
	// it does error, we fall back to an empty pool because the CNO trust
	// bundle (Source 1) already includes system CAs.
	pool, err := x509.SystemCertPool()
	if err != nil {
		log.Error(err, "failed to load system cert pool, falling back to empty pool; CAs from CNO trust bundle will still be loaded")

		pool = x509.NewCertPool()
	}

	keysWithCerts := 0

	// Source 1: CNO-managed merged trust bundle (install-time + proxy CAs).
	n, err := loadCertsFromConfigMap(ctx, c, log, pool,
		trustedCABundleName, trustedCABundleNamespace)
	if err != nil {
		return nil, err
	}

	keysWithCerts += n

	// Source 2: image.config additionalTrustedCA (Day 2 registry-specific CAs).
	n, err = loadImageConfigCerts(ctx, c, log, pool)
	if err != nil {
		return nil, err
	}

	keysWithCerts += n

	if keysWithCerts == 0 {
		log.Info("no additional trusted CAs found, using system CAs only")
		return remote.DefaultTransport, nil
	}

	log.Info("loaded additional trusted CAs for registry access", "keysWithCerts", keysWithCerts)

	// Clone go-containerregistry's DefaultTransport so we stay in sync
	// with upstream settings, then overlay our custom TLS config.
	base, ok := remote.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("%w: got %T", errUnexpectedTransportType, remote.DefaultTransport)
	}

	transport := base.Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}

	return transport, nil
}

// loadCertsFromConfigMap reads all data entries from a ConfigMap and appends
// any valid PEM certificates to the pool. Returns the number of data keys
// that contained at least one valid PEM certificate.
func loadCertsFromConfigMap(ctx context.Context, c client.Reader, log logr.Logger, pool *x509.CertPool, name, namespace string) (int, error) {
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &cm); err != nil {
		if apierrors.IsNotFound(err) {
			return 0, nil
		}

		return 0, fmt.Errorf("failed to get CA ConfigMap %s/%s: %w", namespace, name, err)
	}

	added := 0

	for key, pemData := range cm.Data {
		if pool.AppendCertsFromPEM([]byte(pemData)) {
			added++
		} else {
			log.Info("ConfigMap key did not contain valid PEM certificates", "configMap", name, "namespace", namespace, "key", key)
		}
	}

	if added > 0 {
		log.Info("loaded CAs from ConfigMap", "configMap", name, "namespace", namespace, "keysWithCerts", added)
	}

	return added, nil
}

// loadImageConfigCerts reads image.config.openshift.io/cluster, and if
// additionalTrustedCA is set, loads certs from the referenced ConfigMap.
func loadImageConfigCerts(ctx context.Context, c client.Reader, log logr.Logger, pool *x509.CertPool) (int, error) {
	var imageConfig configv1.Image
	if err := c.Get(ctx, types.NamespacedName{Name: imageConfigName}, &imageConfig); err != nil {
		if apierrors.IsNotFound(err) {
			return 0, nil
		}

		return 0, fmt.Errorf("failed to get image config: %w", err)
	}

	cmName := imageConfig.Spec.AdditionalTrustedCA.Name
	if cmName == "" {
		return 0, nil
	}

	return loadCertsFromConfigMap(ctx, c, log, pool, cmName, openshiftConfigNamespace)
}
