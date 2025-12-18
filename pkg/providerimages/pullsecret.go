package providerimages

import (
	"bytes"
	"fmt"

	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/config/configfile"
	"github.com/docker/cli/cli/config/types"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
)

func parseDockerConfig(pullSecret []byte) (authn.Keychain, error) {
	if len(pullSecret) == 0 {
		return authn.DefaultKeychain, nil
	}

	cf, err := config.LoadFromReader(bytes.NewReader(pullSecret))
	if err != nil {
		return nil, fmt.Errorf("failed to parse docker config: %w", err)
	}

	return &configFileKeychain{cf: cf}, nil
}

// configFileKeychain implements authn.Keychain using a docker config file.
type configFileKeychain struct {
	cf *configfile.ConfigFile
}

func (k *configFileKeychain) Resolve(resource authn.Resource) (authn.Authenticator, error) {
	// Try to get auth config for the registry
	key := resource.RegistryStr()
	if key == name.DefaultRegistry {
		key = authn.DefaultAuthKey
	}

	cfg, err := k.cf.GetAuthConfig(key)
	if err != nil {
		return nil, err
	}

	// Check if we got an empty config
	empty := types.AuthConfig{}
	cfg.ServerAddress = "" // Clear for comparison
	if cfg == empty {
		return authn.Anonymous, nil
	}

	return authn.FromConfig(authn.AuthConfig{
		Username:      cfg.Username,
		Password:      cfg.Password,
		Auth:          cfg.Auth,
		IdentityToken: cfg.IdentityToken,
		RegistryToken: cfg.RegistryToken,
	}), nil
}
