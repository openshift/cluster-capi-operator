package providermetadata

import configv1 "github.com/openshift/api/config/v1"

const (
	AttributeKeyType    = "type"
	AttributeKeyVersion = "version"
)

type ProviderMetadata struct {
	Name         string                `json:"name"`
	Attributes   map[string]string     `json:"attributes,omitempty"`
	OCPPlatform  configv1.PlatformType `json:"ocpPlatform,omitempty"`
	SelfImageRef string                `json:"selfImageRef,omitempty"`
	InstallOrder int                   `json:"installOrder,omitempty"`
}

// MatchesPlatform reports whether this profile should be installed on the
// given cluster platform. Profiles with no OCPPlatform set (empty string)
// are platform-independent and match every cluster.
func (m ProviderMetadata) MatchesPlatform(clusterPlatform configv1.PlatformType) bool {
	return m.OCPPlatform == "" || m.OCPPlatform == clusterPlatform
}
