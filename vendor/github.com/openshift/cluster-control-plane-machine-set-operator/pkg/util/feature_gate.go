/*
Copyright 2022 Red Hat, Inc.

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
	"os"
	"time"

	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"

	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	releaseVersionEnvVariableName = "RELEASE_VERSION"
	unknownVersionValue           = "unknown"
)

var (
	// errUnsupportedPlatform defines an error for an unsupported platform.
	errFeatureGateDetectionTimeout = errors.New("timed out waiting for FeatureGate detection")
)

// GetReleaseVersion returns a string representing the release version.
func GetReleaseVersion() string {
	releaseVersion := os.Getenv(releaseVersionEnvVariableName)
	if len(releaseVersion) == 0 {
		releaseVersion = unknownVersionValue
	}

	return releaseVersion
}

// SetupFeatureGateAccessor Setup FeatureGateAccess instance.
func SetupFeatureGateAccessor(mgr manager.Manager) (featuregates.FeatureGateAccess, error) {
	desiredVersion := GetReleaseVersion()
	missingVersion := "0.0.1-snapshot"

	configClient, err := configv1client.NewForConfig(mgr.GetConfig())
	if err != nil {
		return nil, fmt.Errorf("unable to create config client: %w", err)
	}

	configInformers := configinformers.NewSharedInformerFactory(configClient, 10*time.Minute)

	// By default, this will exit(0) if the featuregates change
	featureGateAccessor := featuregates.NewFeatureGateAccess(
		desiredVersion, missingVersion,
		configInformers.Config().V1().ClusterVersions(),
		configInformers.Config().V1().FeatureGates(),
		events.NewLoggingEventRecorder("controlplanemachineset"),
	)
	go featureGateAccessor.Run(context.Background())
	go configInformers.Start(context.Background().Done())

	select {
	case <-featureGateAccessor.InitialFeatureGatesObserved():
		return featureGateAccessor, nil
	case <-time.After(1 * time.Minute):
		return nil, errFeatureGateDetectionTimeout
	}
}
