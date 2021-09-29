package controllers

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"

	configv1 "github.com/openshift/api/config/v1"
)

// isCAPIFeatureGateEnabled determines whether the ClusterAPIEnabled feature gate is present in the current
// feature set.
func isCAPIFeatureGateEnabled(featureGate *configv1.FeatureGate) (bool, error) {
	if featureGate == nil {
		return false, nil
	}
	featureSet, ok := configv1.FeatureSets[featureGate.Spec.FeatureSet]
	if !ok {
		return false, fmt.Errorf(".spec.featureSet %q not found", featureGate.Spec.FeatureSet)
	}

	enabledFeatureGates := sets.NewString(featureSet.Enabled...)
	disabledFeatureGates := sets.NewString(featureSet.Disabled...)
	// CustomNoUpgrade will override the default enabled feature gates.
	if featureGate.Spec.FeatureSet == configv1.CustomNoUpgrade && featureGate.Spec.CustomNoUpgrade != nil {
		enabledFeatureGates = sets.NewString(featureGate.Spec.CustomNoUpgrade.Enabled...)
		disabledFeatureGates = sets.NewString(featureGate.Spec.CustomNoUpgrade.Disabled...)
	}

	return !disabledFeatureGates.Has(ClusterAPIEnabled) && enabledFeatureGates.Has(ClusterAPIEnabled), nil
}
