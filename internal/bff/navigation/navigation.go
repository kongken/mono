// Package navigation is the server-side registry that decides where the browser may
// be sent after an account step (sign-in or password confirmation at OzID).
//
// A client only ever names a stable app ID and a canonical pathname. The registry maps
// the ID to an exact origin and an explicit set of allowed paths, so no user input can
// become a redirect target, an origin, or a CORS entry. It reads no account data and
// creates no session: resolving a target proves nothing about the caller.
package navigation

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/kongken/mono/internal/platform/httpx"
	"github.com/kongken/mono/internal/platform/navpath"
	"golang.org/x/net/publicsuffix"
)

// Stable application IDs of the first version. They are not host names: moving an app
// to another domain changes its registered origin, never its ID.
const (
	AppOzID  = "ozid"
	AppAdmin = "admin"
	AppTrip  = "trip-map"
	AppMetro = "metro"
)

// AppIDs lists the IDs the registry accepts.
var AppIDs = []string{AppOzID, AppAdmin, AppTrip, AppMetro}

// ErrNotAllowed is returned for an unknown app, or a path the app does not allow.
var ErrNotAllowed = errors.New("navigation target not allowed")

// App registers one front end.
type App struct {
	// Origin is the exact browser origin of the front end, such as https://app.example.com.
	Origin string `yaml:"origin"`
	// DefaultPath is where the browser returns when the caller names no path. Default "/".
	DefaultPath string `yaml:"default_path"`
	// Paths adds allowed return paths to DefaultPath. An entry is an exact pathname
	// ("/me") or, with a trailing "/*", a prefix ("/metro/*" allows "/metro" and every
	// path below it); "/*" allows the whole app. Nothing else is allowed.
	Paths []string `yaml:"paths"`
}

// Config is the registry configuration (the BFF's `navigation` section). Leaving it
// out disables navigation: ResolveNavigation then answers FAILED_PRECONDITION.
type Config struct {
	// APIOrigin is the canonical public origin of this BFF. Every front end calls it
	// directly with credentials and the session Cookie is host-only on it, so it must be
	// in the same schemeful site as every registered app. It is only used for that check.
	APIOrigin string `yaml:"api_origin"`
	// Apps maps a stable app ID (ozid, admin, trip-map, metro) to its registration.
	// ozid is required once any app is registered.
	Apps map[string]App `yaml:"apps"`
}

// Enabled reports whether any navigation setting is present.
func (c Config) Enabled() bool { return c.APIOrigin != "" || len(c.Apps) > 0 }

// Target is a validated destination.
type Target struct {
	AppID string
	// Path is the accepted pathname (the default path when none was requested).
	Path string
	// URL is the registered origin plus Path.
	URL string
	// OzIDOrigin is the registered origin of the user center.
	OzIDOrigin string
}

type rule struct {
	path   string
	prefix bool
}

func (r rule) allows(p string) bool {
	if !r.prefix {
		return p == r.path
	}
	if r.path == "/" {
		return true
	}
	return p == r.path || strings.HasPrefix(p, r.path+"/")
}

type entry struct {
	origin      string
	defaultPath string
	rules       []rule
}

// Registry answers Resolve from an immutable, validated copy of the configuration.
type Registry struct {
	apps map[string]entry
}

// New validates c and builds the registry. It returns nil, nil when c is empty.
// corsOrigins are the exact origins the BFF answers cross-origin, and secureCookies
// is auth.cookie_secure: with it every origin must be https.
//
// Misconfiguration fails here, at startup, with a message that names the setting.
func New(c Config, corsOrigins []string, secureCookies bool) (*Registry, error) {
	if !c.Enabled() {
		return nil, nil
	}
	if c.APIOrigin == "" {
		return nil, errors.New("navigation.api_origin is required when navigation.apps is set")
	}
	if len(c.Apps) == 0 {
		return nil, errors.New("navigation.apps is required when navigation.api_origin is set")
	}
	if _, ok := c.Apps[AppOzID]; !ok {
		return nil, fmt.Errorf("navigation.apps.%s is required", AppOzID)
	}
	if err := checkOrigin("navigation.api_origin", c.APIOrigin, secureCookies); err != nil {
		return nil, err
	}
	apiSite, err := schemefulSite(c.APIOrigin)
	if err != nil {
		return nil, fmt.Errorf("navigation.api_origin: %w", err)
	}
	reg := &Registry{apps: make(map[string]entry, len(c.Apps))}
	seen := map[string]string{}
	for _, id := range sortedKeys(c.Apps) {
		field := "navigation.apps." + id
		if !slices.Contains(AppIDs, id) {
			return nil, fmt.Errorf("%s: unknown app ID (known: %s)", field, strings.Join(AppIDs, ", "))
		}
		app := c.Apps[id]
		if err := checkOrigin(field+".origin", app.Origin, secureCookies); err != nil {
			return nil, err
		}
		if other, dup := seen[app.Origin]; dup {
			return nil, fmt.Errorf("%s.origin %q is already the origin of %s", field, app.Origin, other)
		}
		seen[app.Origin] = id
		if !slices.Contains(corsOrigins, app.Origin) {
			return nil, fmt.Errorf("%s.origin %q must also be listed in cors_allowed_origins: the front end calls the API cross-origin", field, app.Origin)
		}
		site, err := schemefulSite(app.Origin)
		if err != nil {
			return nil, fmt.Errorf("%s.origin: %w", field, err)
		}
		if site != apiSite {
			return nil, fmt.Errorf("%s.origin %q is not in the same site as navigation.api_origin %q: the host-only session Cookie would not be sent", field, app.Origin, c.APIOrigin)
		}
		e, err := buildEntry(field, app)
		if err != nil {
			return nil, err
		}
		reg.apps[id] = e
	}
	return reg, nil
}

func buildEntry(field string, app App) (entry, error) {
	def := app.DefaultPath
	if def == "" {
		def = "/"
	}
	if err := CheckPath(def); err != nil {
		return entry{}, fmt.Errorf("%s.default_path %q: %w", field, def, err)
	}
	e := entry{origin: app.Origin, defaultPath: def, rules: []rule{{path: def}}}
	for _, raw := range app.Paths {
		r := rule{path: raw}
		if raw == "/*" {
			r = rule{path: "/", prefix: true}
		} else if strings.HasSuffix(raw, "/*") {
			r = rule{path: strings.TrimSuffix(raw, "/*"), prefix: true}
		}
		if err := CheckPath(r.path); err != nil {
			return entry{}, fmt.Errorf("%s.paths %q: %w", field, raw, err)
		}
		e.rules = append(e.rules, r)
	}
	return e, nil
}

// Resolve checks appID and returnPath ("" means the app's default path). Every
// failure is ErrNotAllowed and says nothing about which part was refused.
func (r *Registry) Resolve(appID, returnPath string) (Target, error) {
	if r == nil {
		return Target{}, ErrNotAllowed
	}
	app, ok := r.apps[appID]
	if !ok {
		return Target{}, ErrNotAllowed
	}
	p := returnPath
	if p == "" {
		p = app.defaultPath
	}
	if CheckPath(p) != nil {
		return Target{}, ErrNotAllowed
	}
	if !slices.ContainsFunc(app.rules, func(ru rule) bool { return ru.allows(p) }) {
		return Target{}, ErrNotAllowed
	}
	return Target{AppID: appID, Path: p, URL: app.origin + p, OzIDOrigin: r.apps[AppOzID].origin}, nil
}

// CheckPath reports whether p is a canonical return pathname; the rule is shared with
// usersrv, which stores a validated context with a mail challenge (see navpath.Check).
func CheckPath(p string) error { return navpath.Check(p) }

func checkOrigin(field, origin string, secureCookies bool) error {
	if origin == "" {
		return fmt.Errorf("%s is required", field)
	}
	if err := httpx.CheckOrigins(field, []string{origin}); err != nil {
		return err
	}
	u, _ := url.Parse(origin)
	if u.Scheme == "https" {
		return nil
	}
	if secureCookies {
		return fmt.Errorf("%s: %q must be https when auth.cookie_secure is true", field, origin)
	}
	if !loopback(u.Hostname()) {
		return fmt.Errorf("%s: %q must be https; plain http is only allowed for localhost development", field, origin)
	}
	return nil
}

func loopback(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// schemeful site: scheme plus registrable domain. IP addresses and localhost names
// are their own site.
func schemefulSite(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil {
		return "", err
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) != nil {
		return u.Scheme + "://" + host, nil
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return u.Scheme + "://localhost", nil
	}
	site, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return "", fmt.Errorf("%q has no registrable domain: %w", host, err)
	}
	return u.Scheme + "://" + site, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
