package config

import "fmt"

// AppConfig shares the YAML file with Butterfly's store.db/redis/s3 definitions.
type AppConfig struct {
	DBStore     string        `yaml:"db_store"`
	RedisStore  string        `yaml:"redis_store"`
	AutoMigrate bool          `yaml:"auto_migrate"`
	Storage     StorageConfig `yaml:"storage"`
}
type StorageConfig struct {
	S3Store   string `yaml:"s3_store"`
	KeyPrefix string `yaml:"key_prefix"`
}

func (c *AppConfig) Print() {}
func (c *AppConfig) Validate() error {
	if c.DBStore == "" || c.RedisStore == "" || c.Storage.S3Store == "" {
		return fmt.Errorf("db_store, redis_store and storage.s3_store are required")
	}
	return nil
}
