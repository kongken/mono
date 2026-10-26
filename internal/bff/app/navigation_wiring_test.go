package app

import (
	"strings"
	"testing"

	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/navigation"
	"github.com/kongken/mono/internal/bff/upstream"
	"github.com/kongken/mono/internal/platform/testkit"
)

// publicAPIFixture is a configuration whose account wiring is reached (resources and a usersrv
// connection exist) with the given navigation section. The connection is lazy and never used.
func publicAPIFixture(t *testing.T, nav navigation.Config) (*Handlers, *upstream.Clients) {
	t.Helper()
	keys := testkit.NewKeys(t)
	cfg := &config.Config{
		CORSAllowedOrigins: []string{"https://id.example.com"},
		Navigation:         nav,
		InternalAuth:       config.InternalAuth{Issuer: "bff", KID: "k1", PrivateKeyFile: keys.PrivateFile},
		GRPC: config.GRPCConfig{Services: map[string]config.GRPCService{
			"usersrv": {Address: "127.0.0.1:1", Transport: "h2c"},
		}},
	}
	cfg.Auth.CookieSecure = true
	h := New(cfg)
	h.SetResources(&Resources{})
	clients, err := upstream.Dial(cfg, []string{"usersrv"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clients.Close() })
	return h, clients
}

// The account wiring does not continue without the navigation registry it was configured with:
// the build fails and nothing is mounted or stored. Bootstrap returns publicAPI's error before it
// stores the public handler, so the BFF does not start serving an API whose sign-in return flow
// would silently refuse everything.
func TestPublicAPIFailsWhenTheNavigationRegistryCannotBeBuilt(t *testing.T) {
	// ozid is not in cors_allowed_origins, which navigation.New refuses.
	bad := navigation.Config{
		APIOrigin: "https://api.example.com",
		Apps:      map[string]navigation.App{"ozid": {Origin: "https://unlisted.example.com"}},
	}
	h, clients := publicAPIFixture(t, bad)

	handler, err := h.publicAPI(clients)
	if err == nil {
		t.Fatal("publicAPI built a handler although the navigation registry is unusable")
	}
	if !strings.Contains(err.Error(), "navigation") || !strings.Contains(err.Error(), "cors_allowed_origins") {
		t.Fatalf("the error does not name the setting: %v", err)
	}
	if handler != nil {
		t.Fatal("a handler came back with the error")
	}
	if h.auth.Load() != nil {
		t.Fatal("the account handler was stored before the registry was known to be usable")
	}
}

// The same wiring with a usable section still builds, so the failure above is the registry's.
func TestPublicAPIBuildsWithAUsableNavigationRegistry(t *testing.T) {
	good := navigation.Config{
		APIOrigin: "https://api.example.com",
		Apps:      map[string]navigation.App{"ozid": {Origin: "https://id.example.com"}},
	}
	h, clients := publicAPIFixture(t, good)

	handler, err := h.publicAPI(clients)
	if err != nil {
		t.Fatal(err)
	}
	if handler == nil || h.auth.Load() == nil {
		t.Fatal("the account API was not mounted")
	}
}
