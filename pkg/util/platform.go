/*
Copyright 2024 Red Hat, Inc.

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
package util

import (
	"context"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	infrastructureResourceName = "cluster"
)

var (
	errNilInfrastructure = errors.New("error infrastructure is nil")
	errNoPlatformStatus  = errors.New("error getting PlatformStatus, field not set")
)

// GetPlatform returns the platform type from the infrastructure resource.
func GetPlatform(ctx context.Context, infra *configv1.Infrastructure) (configv1.PlatformType, error) {
	if infra == nil {
		return "", errNilInfrastructure
	}

	if infra.Status.PlatformStatus == nil {
		return "", errNoPlatformStatus
	}

	return infra.Status.PlatformStatus.Type, nil
}

// GetInfra returns the infrastructure resource.
func GetInfra(ctx context.Context, cl client.Reader) (*configv1.Infrastructure, error) {
	infra := &configv1.Infrastructure{}

	if err := cl.Get(ctx, client.ObjectKey{Name: infrastructureResourceName}, infra); err != nil {
		return nil, fmt.Errorf("failed to get infrastructure %q: %w", infra.Kind, err)
	}

	return infra, nil
}
