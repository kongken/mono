package config

import (
	"testing"

	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/servicetoken"
)

func valid() AppConfig {
	return AppConfig{
		DBStore: "main",
		GRPC:    grpcx.ServerConfig{Transport: "h2c", Listen: "127.0.0.1:18085"},
		InternalAuth: servicetoken.VerifyConfig{
			Issuer: "tripmap-bff", Audience: "metro",
			TrustedKeys: []servicetoken.TrustedKey{{KID: "dev-1", PublicKeyFile: "bff.pub"}},
		},
	}
}

func TestValidate(t *testing.T) {
	ok := valid()
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*AppConfig){
		"no database":    func(c *AppConfig) { c.DBStore = "" },
		"no listener":    func(c *AppConfig) { c.GRPC.Listen = "" },
		"no issuer":      func(c *AppConfig) { c.InternalAuth.Issuer = "" },
		"no audience":    func(c *AppConfig) { c.InternalAuth.Audience = "" },
		"no trusted key": func(c *AppConfig) { c.InternalAuth.TrustedKeys = nil },
	} {
		c := valid()
		mutate(&c)
		if c.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
