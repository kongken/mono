package app

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/kongken/mono/internal/usersrv/config"
	"github.com/kongken/mono/internal/usersrv/externalprovider"
)

type noProviderNetwork struct{ calls atomic.Int64 }

func (n *noProviderNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	n.calls.Add(1)
	return nil, errors.New("provider network prohibited in startup")
}

func TestExternalProviderAssemblyIsOfflineAndIgnoresRegistrationGate(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		rt := &noProviderNetwork{}
		off := false
		auth := config.AuthConfig{RegistrationEnabled: &off, External: config.ExternalConfig{Enabled: enabled, Providers: map[string]config.ExternalProviderConfig{
			"google": {Enabled: enabled, ClientID: "client", ClientSecret: "secret", RedirectURI: "https://api.example.com/api/identity/callback/google", ConfigVersion: 1},
			"github": {Enabled: enabled, ClientID: "github-client", ClientSecret: "github-secret", RedirectURI: "https://api.example.com/api/identity/callback/github", ConfigVersion: 2},
		}}}
		providers, err := buildExternalProviders(auth, rt)
		if err != nil || rt.calls.Load() != 0 || len(providers) != 2 {
			t.Fatalf("startup assembly: %v", err)
		}
		// A disabled adapter refuses before input validation/network. An enabled
		// one performs input validation independent of closed registration.
		for _, name := range []externalprovider.ProviderName{externalprovider.GoogleName, externalprovider.GitHubName} {
			_, err = providers[name].AuthorizationURL(context.Background(), externalprovider.AuthorizationRequest{})
			if enabled && !errors.Is(err, externalprovider.ErrRejected) {
				t.Fatal("registration disabled the provider")
			}
			if !enabled && !errors.Is(err, externalprovider.ErrDisabled) {
				t.Fatal("provider disabled gate not retained")
			}
		}
		if rt.calls.Load() != 0 {
			t.Fatal("constructor/validation contacted provider")
		}
	}
}

func TestExternalProviderGatesAreIndependent(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, google := range []bool{false, true} {
			for _, github := range []bool{false, true} {
				rt := &noProviderNetwork{}
				auth := config.AuthConfig{External: config.ExternalConfig{Enabled: global, Providers: map[string]config.ExternalProviderConfig{
					"google": {Enabled: google, ClientID: "client", ClientSecret: "secret", RedirectURI: "https://api.example.com/api/identity/callback/google", ConfigVersion: 1},
					"github": {Enabled: github, ClientID: "client", ClientSecret: "secret", RedirectURI: "https://api.example.com/api/identity/callback/github", ConfigVersion: 1},
				}}}
				providers, err := buildExternalProviders(auth, rt)
				if err != nil || len(providers) != 2 {
					t.Fatalf("assembly failed: %v", err)
				}
				for name, enabled := range map[externalprovider.ProviderName]bool{externalprovider.GoogleName: global && google, externalprovider.GitHubName: global && github} {
					_, err := providers[name].ExchangeCode(context.Background(), externalprovider.CodeExchangeRequest{})
					want := externalprovider.ErrDisabled
					if enabled {
						want = externalprovider.ErrRejected
					}
					if !errors.Is(err, want) {
						t.Fatal("provider gate depends on another provider")
					}
				}
				if rt.calls.Load() != 0 {
					t.Fatal("startup contacted a provider")
				}
			}
		}
	}
}
