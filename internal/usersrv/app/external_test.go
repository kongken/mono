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
		}}}
		providers, err := buildExternalProviders(auth, rt)
		if err != nil || rt.calls.Load() != 0 || providers[externalprovider.GoogleName] == nil {
			t.Fatalf("startup assembly: %v", err)
		}
		// A disabled adapter refuses before input validation/network. An enabled
		// one performs input validation independent of closed registration.
		_, err = providers[externalprovider.GoogleName].AuthorizationURL(context.Background(), externalprovider.AuthorizationRequest{})
		if enabled && !errors.Is(err, externalprovider.ErrRejected) {
			t.Fatal("registration disabled the provider")
		}
		if !enabled && !errors.Is(err, externalprovider.ErrDisabled) {
			t.Fatal("provider disabled gate not retained")
		}
		if rt.calls.Load() != 0 {
			t.Fatal("constructor/validation contacted provider")
		}
	}
}
