package navigation_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/kongken/mono/internal/bff/navigation"
)

var cors = []string{"https://id.example.com", "https://admin.example.com", "https://trip.example.com", "https://metro.example.com"}

func config() navigation.Config {
	return navigation.Config{
		APIOrigin: "https://api.example.com",
		Apps: map[string]navigation.App{
			"ozid":     {Origin: "https://id.example.com"},
			"admin":    {Origin: "https://admin.example.com", DefaultPath: "/users", Paths: []string{"/metro/*", "/audit/users"}},
			"trip-map": {Origin: "https://trip.example.com"},
			"metro":    {Origin: "https://metro.example.com", Paths: []string{"/me", "/city/*", "/topic/*"}},
		},
	}
}

func registry(t *testing.T) *navigation.Registry {
	t.Helper()
	r, err := navigation.New(config(), cors, true)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestResolveAcceptsRegisteredTargets(t *testing.T) {
	r := registry(t)
	for _, c := range []struct{ app, path, wantPath, wantURL string }{
		{"trip-map", "", "/", "https://trip.example.com/"},
		{"trip-map", "/", "/", "https://trip.example.com/"},
		{"metro", "/me", "/me", "https://metro.example.com/me"},
		{"metro", "/city/shanghai", "/city/shanghai", "https://metro.example.com/city/shanghai"},
		{"metro", "/city", "/city", "https://metro.example.com/city"},
		{"metro", "/city/a/b/", "/city/a/b/", "https://metro.example.com/city/a/b/"},
		{"admin", "", "/users", "https://admin.example.com/users"},
		{"admin", "/metro/lines", "/metro/lines", "https://admin.example.com/metro/lines"},
		{"admin", "/audit/users", "/audit/users", "https://admin.example.com/audit/users"},
		{"ozid", "", "/", "https://id.example.com/"},
	} {
		got, err := r.Resolve(c.app, c.path)
		if err != nil {
			t.Fatalf("%s %q: %v", c.app, c.path, err)
		}
		if got.Path != c.wantPath || got.URL != c.wantURL || got.AppID != c.app || got.OzIDOrigin != "https://id.example.com" {
			t.Fatalf("%s %q: got %+v", c.app, c.path, got)
		}
	}
}

func TestResolveRefusesEverythingElse(t *testing.T) {
	r := registry(t)
	cases := []struct{ name, app, path string }{
		{"unknown app", "other", "/"},
		{"empty app", "", "/"},
		{"app is a url", "https://evil.example", "/"},
		{"app is a host", "trip.example.com", "/"},
		{"absolute url", "metro", "https://evil.example/me"},
		{"scheme relative", "metro", "//evil.example/me"},
		{"no leading slash", "metro", "me"},
		{"backslash", "metro", "/\\evil.example"},
		{"slash backslash", "metro", "/me\\..\\x"},
		{"double slash inside", "metro", "/city//x"},
		{"dot segment", "metro", "/city/./x"},
		{"dot dot segment", "metro", "/city/../me"},
		{"dot dot to unregistered", "admin", "/metro/../secret"},
		{"encoded dot dot", "metro", "/city/%2e%2e/me"},
		{"encoded slash", "metro", "/city%2fx"},
		{"encoded percent", "metro", "/city/%252e"},
		{"query", "metro", "/me?x=1"},
		{"fragment", "metro", "/me#x"},
		{"control char", "metro", "/me\n"},
		{"tab", "metro", "/me\t"},
		{"nul", "metro", "/me\x00"},
		{"space", "metro", "/city/a b"},
		{"unicode", "metro", "/city/上海"},
		{"colon", "metro", "/city/a:b"},
		{"at sign", "metro", "/@evil.example"},
		{"unregistered path", "metro", "/admin"},
		{"prefix without boundary", "metro", "/cityx"},
		{"prefix of exact", "metro", "/me/x"},
		{"exact rule is not a prefix", "admin", "/audit/users/x"},
		{"too long", "metro", "/city/" + strings.Repeat("a", 300)},
		{"another app's path", "trip-map", "/me"},
	}
	// trip-map registered only its default path.
	for _, c := range cases {
		if got, err := r.Resolve(c.app, c.path); !errors.Is(err, navigation.ErrNotAllowed) {
			t.Errorf("%s: %q on %q resolved to %+v", c.name, c.path, c.app, got)
		}
	}
}

func TestNilRegistryRefuses(t *testing.T) {
	var r *navigation.Registry
	if _, err := r.Resolve("ozid", ""); !errors.Is(err, navigation.ErrNotAllowed) {
		t.Fatal(err)
	}
}

func TestEmptyConfigDisablesNavigation(t *testing.T) {
	r, err := navigation.New(navigation.Config{}, nil, true)
	if r != nil || err != nil {
		t.Fatalf("got %v, %v", r, err)
	}
}

func TestWholeAppRule(t *testing.T) {
	c := config()
	c.Apps["trip-map"] = navigation.App{Origin: "https://trip.example.com", Paths: []string{"/*"}}
	r, err := navigation.New(c, cors, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve("trip-map", "/anything/at/all"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve("trip-map", "/a/../b"); err == nil {
		t.Fatal("dot segments must stay refused under /*")
	}
}

func TestConfigurationErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(c *navigation.Config, cors *[]string)
		secure bool
		want   string
	}{
		{"missing api origin", func(c *navigation.Config, _ *[]string) { c.APIOrigin = "" }, true, "navigation.api_origin is required"},
		{"missing apps", func(c *navigation.Config, _ *[]string) { c.Apps = nil }, true, "navigation.apps is required"},
		{"missing ozid", func(c *navigation.Config, _ *[]string) { delete(c.Apps, "ozid") }, true, "navigation.apps.ozid is required"},
		{"unknown app", func(c *navigation.Config, _ *[]string) {
			c.Apps["billing"] = navigation.App{Origin: "https://billing.example.com"}
		}, true, "unknown app ID"},
		{"missing origin", func(c *navigation.Config, _ *[]string) { c.Apps["metro"] = navigation.App{} }, true, "navigation.apps.metro.origin is required"},
		{"origin with path", func(c *navigation.Config, _ *[]string) {
			c.Apps["metro"] = navigation.App{Origin: "https://metro.example.com/app"}
		}, true, "must be an origin"},
		{"wildcard origin", func(c *navigation.Config, _ *[]string) {
			c.Apps["metro"] = navigation.App{Origin: "https://*.example.com"}
		}, true, "must be an origin"},
		{"http with secure cookies", func(c *navigation.Config, cors *[]string) {
			c.Apps["metro"] = navigation.App{Origin: "http://metro.example.com"}
			*cors = append(*cors, "http://metro.example.com")
		}, true, "must be https"},
		{"http outside localhost", func(c *navigation.Config, cors *[]string) {
			c.Apps["metro"] = navigation.App{Origin: "http://metro.example.com"}
			*cors = append(*cors, "http://metro.example.com")
		}, false, "plain http is only allowed for localhost"},
		{"duplicate origin", func(c *navigation.Config, _ *[]string) {
			c.Apps["metro"] = navigation.App{Origin: "https://trip.example.com"}
		}, true, "already the origin of"},
		{"not in cors", func(c *navigation.Config, cors *[]string) { *cors = (*cors)[:3] }, true, "must also be listed in cors_allowed_origins"},
		{"other site", func(c *navigation.Config, cors *[]string) {
			c.Apps["metro"] = navigation.App{Origin: "https://metro.other.test"}
			*cors = append(*cors, "https://metro.other.test")
		}, true, "not in the same site"},
		{"api on other site", func(c *navigation.Config, _ *[]string) { c.APIOrigin = "https://api.other.test" }, true, "not in the same site"},
		{"bad default path", func(c *navigation.Config, _ *[]string) {
			c.Apps["metro"] = navigation.App{Origin: "https://metro.example.com", DefaultPath: "/a/../b"}
		}, true, "default_path"},
		{"bad rule", func(c *navigation.Config, _ *[]string) {
			c.Apps["metro"] = navigation.App{Origin: "https://metro.example.com", Paths: []string{"/a?b"}}
		}, true, "navigation.apps.metro.paths"},
		{"relative rule", func(c *navigation.Config, _ *[]string) {
			c.Apps["metro"] = navigation.App{Origin: "https://metro.example.com", Paths: []string{"me"}}
		}, true, "navigation.apps.metro.paths"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, origins := config(), append([]string(nil), cors...)
			c.mutate(&cfg, &origins)
			_, err := navigation.New(cfg, origins, c.secure)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}
}

func TestLocalhostDevelopment(t *testing.T) {
	cfg := navigation.Config{
		APIOrigin: "http://localhost:18081",
		Apps: map[string]navigation.App{
			"ozid":  {Origin: "http://localhost:5175"},
			"metro": {Origin: "http://localhost:5174", Paths: []string{"/me"}},
		},
	}
	r, err := navigation.New(cfg, []string{"http://localhost:5175", "http://localhost:5174"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.Resolve("metro", "/me"); err != nil || got.URL != "http://localhost:5174/me" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := navigation.New(cfg, []string{"http://localhost:5175", "http://localhost:5174"}, true); err == nil {
		t.Fatal("secure cookies need https")
	}
}

func TestResolveIgnoresMutationOfConfig(t *testing.T) {
	cfg := config()
	r, err := navigation.New(cfg, cors, true)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Apps["metro"] = navigation.App{Origin: "https://evil.example.com"}
	if got, _ := r.Resolve("metro", "/me"); got.URL != "https://metro.example.com/me" {
		t.Fatalf("registry follows the config map: %+v", got)
	}
}
