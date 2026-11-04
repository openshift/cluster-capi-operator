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
package upgradeable

import (
	"fmt"

	"github.com/blang/semver/v4"
	configv1 "github.com/openshift/api/config/v1"
)

func resolveTarget(cv *configv1.ClusterVersion) (*configv1.Release, error) {
	current, err := semver.Parse(cv.Status.Desired.Version)
	if err != nil {
		return nil, fmt.Errorf("parsing current release version: %w", err)
	}

	var (
		target        *configv1.Release
		targetVersion semver.Version
	)

	consider := func(release configv1.Release) {
		version, err := semver.Parse(release.Version)
		if err != nil || version.Major != current.Major || version.Minor != current.Minor+1 || version.Patch != 0 {
			return
		}

		if target == nil || version.GT(targetVersion) {
			target = &release
			targetVersion = version
		}
	}
	// Available updates win ties because only strictly newer candidates replace them.
	for _, release := range cv.Status.AvailableUpdates {
		consider(release)
	}

	for _, update := range cv.Status.ConditionalUpdates {
		consider(update.Release)
	}

	return target, nil
}
