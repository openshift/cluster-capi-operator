package controllers

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
)

func TestIsCAPIFeatureGateEnabled(t *testing.T) {
	tests := []struct {
		name        string
		featureGate *configv1.FeatureGate
		want        bool
		wantErr     bool
	}{
		{
			name: "custom enabled",
			featureGate: &configv1.FeatureGate{
				Spec: configv1.FeatureGateSpec{
					FeatureGateSelection: configv1.FeatureGateSelection{
						FeatureSet:      configv1.CustomNoUpgrade,
						CustomNoUpgrade: &configv1.CustomFeatureGates{Enabled: []string{ClusterAPIEnabled}},
					},
				},
			},
			want: true,
		},
		{
			name: "default not enabled",
			featureGate: &configv1.FeatureGate{
				Spec: configv1.FeatureGateSpec{
					FeatureGateSelection: configv1.FeatureGateSelection{
						FeatureSet: configv1.Default,
					},
				},
			},
			want: false,
		},
		{
			name: "techpreview not enabled", // TODO this will change
			featureGate: &configv1.FeatureGate{
				Spec: configv1.FeatureGateSpec{
					FeatureGateSelection: configv1.FeatureGateSelection{
						FeatureSet: configv1.TechPreviewNoUpgrade,
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := isCAPIFeatureGateEnabled(tt.featureGate)
			if (err != nil) != tt.wantErr {
				t.Errorf("isCAPIFeatureGateEnabled() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("isCAPIFeatureGateEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
