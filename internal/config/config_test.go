package config

import "testing"

func TestRequiredStores(t *testing.T) {
	if (&AppConfig{}).Validate() == nil {
		t.Fatal("empty configuration accepted")
	}
	cfg := &AppConfig{DBStore: "main", RedisStore: "main", Storage: StorageConfig{S3Store: "main"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
