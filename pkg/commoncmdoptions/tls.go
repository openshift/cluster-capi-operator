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

package commoncmdoptions

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	utiltls "github.com/openshift/controller-runtime-common/pkg/tls"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// resolveTLSProfile fetches the cluster's TLS security profile from the APIServer
// configuration and returns:
//   - the resolved TLS profile spec
//   - a function to generate a SecurityProfileWatcher and add it to a manager
//
// The generator function takes manager and a cancel function. The cancel
// function should cancel the manager's context to trigger a restart.
func resolveTLSProfile(ctx context.Context, cfg *rest.Config, scheme *runtime.Scheme, log logr.Logger) (configv1.TLSProfileSpec, func(manager.Manager, context.CancelFunc) error, error) {
	cl, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return configv1.TLSProfileSpec{}, nil, fmt.Errorf("unable to create initial client: %w", err)
	}

	apiServer := &configv1.APIServer{}
	if err = cl.Get(ctx, client.ObjectKey{Name: utiltls.APIServerName}, apiServer); err != nil {
		return configv1.TLSProfileSpec{}, nil, fmt.Errorf("fetching APIServer: %w", err)
	}

	tlsProfileSpec, err := TLSProfileSpecFromClusterConfig(apiServer.Spec.TLSAdherence, apiServer.Spec.TLSSecurityProfile)
	if err != nil {
		return configv1.TLSProfileSpec{}, nil, fmt.Errorf("resolving TLS profile: %w", err)
	}

	setupSecurityProfileWatcher := func(mgr manager.Manager, cancel context.CancelFunc) error {
		return (&utiltls.SecurityProfileWatcher{
			Client:                    mgr.GetClient(),
			InitialTLSProfileSpec:     tlsProfileSpec,
			InitialTLSAdherencePolicy: apiServer.Spec.TLSAdherence,
			OnProfileChange: func(_ context.Context, _, _ configv1.TLSProfileSpec) {
				log.Info("TLS profile changed, restarting")
				cancel()
			},
			OnAdherencePolicyChange: func(_ context.Context, _, _ configv1.TLSAdherencePolicy) {
				log.Info("TLS adherence policy changed, restarting")
				cancel()
			},
		}).SetupWithManager(mgr)
	}

	return tlsProfileSpec, setupSecurityProfileWatcher, nil
}

// TLSProfileSpecFromClusterConfig resolves the TLS profile spec from the cluster's TLS
// security profile and adherence policy.
func TLSProfileSpecFromClusterConfig(tlsAdherence configv1.TLSAdherencePolicy, tlsSecurityProfile *configv1.TLSSecurityProfile) (configv1.TLSProfileSpec, error) {
	if !libgocrypto.ShouldHonorClusterTLSProfile(tlsAdherence) {
		tlsSecurityProfile = nil
	}

	return utiltls.GetTLSProfileSpec(tlsSecurityProfile) //nolint:wrapcheck
}
