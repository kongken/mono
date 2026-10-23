// Package config is the metro service configuration. It shares the YAML file
// with Butterfly's store.db / store.redis definitions.
package config

import (
	"fmt"

	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/servicetoken"
)

// AppConfig is the metro service configuration.
type AppConfig struct {
	// DBStore names the store.db client of the metro PostgreSQL database. It is a
	// database of its own, not the tripmap or BFF one.
	DBStore string `yaml:"db_store"`
	// RedisStore optionally names a store.redis client. Nothing in the service
	// needs Redis yet; when set it is checked at startup and reported by /readyz.
	RedisStore string `yaml:"redis_store"`
	// AutoMigrate lets Ent create and extend tables at startup. Production
	// deployments apply migrations/metro/ instead and leave it off.
	AutoMigrate bool `yaml:"auto_migrate"`
	// GRPC is the listener the BFF calls; business RPCs are served only here.
	GRPC grpcx.ServerConfig `yaml:"grpc"`
	// InternalAuth lists the BFF public keys that may call this service.
	InternalAuth servicetoken.VerifyConfig `yaml:"internal_auth"`
}

func (c *AppConfig) Print() {}

// Validate checks the settings that must be right before the service starts.
func (c *AppConfig) Validate() error {
	if c.DBStore == "" {
		return fmt.Errorf("db_store is required")
	}
	if err := c.GRPC.Validate(); err != nil {
		return err
	}
	if c.InternalAuth.Issuer == "" || c.InternalAuth.Audience == "" || len(c.InternalAuth.TrustedKeys) == 0 {
		return fmt.Errorf("internal_auth requires issuer, audience and at least one trusted_keys entry")
	}
	return nil
}
