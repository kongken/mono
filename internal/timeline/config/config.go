package config

import (
	"fmt"
	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/servicetoken"
)

type StorageConfig struct {
	S3Store   string `yaml:"s3_store"`
	KeyPrefix string `yaml:"key_prefix"`
}

type AppConfig struct {
	DBStore      string                    `yaml:"db_store"`
	AutoMigrate  bool                      `yaml:"auto_migrate"`
	GRPC         grpcx.ServerConfig        `yaml:"grpc"`
	InternalAuth servicetoken.VerifyConfig `yaml:"internal_auth"`
	Storage      StorageConfig             `yaml:"storage"`
}

func (c *AppConfig) Print() {}
func (c *AppConfig) Validate() error {
	if c.DBStore == "" {
		return fmt.Errorf("db_store is required")
	}
	if c.Storage.S3Store == "" {
		return fmt.Errorf("storage.s3_store is required")
	}
	if err := c.GRPC.Validate(); err != nil {
		return err
	}
	if c.InternalAuth.Issuer == "" || c.InternalAuth.Audience == "" || len(c.InternalAuth.TrustedKeys) == 0 {
		return fmt.Errorf("internal_auth requires issuer, audience and at least one trusted_keys entry")
	}
	return nil
}
