// Package tiles serves Mapbox raster basemap tiles to anonymous browsers. The
// access token stays on the server: browsers call tripmap (through the BFF) and
// tripmap calls Mapbox's fixed Static Tiles API.
package tiles

import (
	"fmt"
	"regexp"
	"time"
)

const (
	// upstreamHost is the only place tiles are fetched from. It is deliberately not
	// configurable: the route must never become a proxy for arbitrary addresses.
	upstreamHost = "api.mapbox.com"
	// tileSize is fixed so the tiles match the 256 px Leaflet layer in the front end.
	tileSize = 256
	// hardMaxZoom is the highest zoom the Static Tiles API serves.
	hardMaxZoom = 22

	defaultMaxZoom          = 18
	defaultRatePerMinute    = 600
	defaultUpstreamTimeout  = 5 * time.Second
	defaultMaxResponseBytes = 512 << 10

	maxRatePerMinute    = 100000
	maxUpstreamTimeout  = 30 * time.Second
	minResponseBytes    = 1 << 10
	maxResponseBytesCap = 4 << 20
)

var (
	// Only public ("pk.") tokens are accepted: they are read-scoped by design, and the
	// proxy never needs a secret ("sk.") token's extra privileges. A Mapbox token is
	// the prefix followed by two base64url segments.
	tokenPattern = regexp.MustCompile(`^pk\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}$`)
	// Usernames and style IDs become URL path segments, so only safe characters pass.
	namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// Config is the optional Mapbox proxy. It is off by default; deployments that do
// not set it keep their pure-local map and any custom tile source unchanged.
type Config struct {
	// Enabled switches the proxy on. While false the route answers 404 and never
	// contacts Mapbox.
	Enabled bool `yaml:"enabled"`
	// Token is a Mapbox public access token ("pk.") limited to the styles:tiles scope.
	// Secret ("sk.") tokens are refused. Rotate it by
	// changing this value and restarting; no front-end rebuild is involved.
	Token string `yaml:"token"`
	// Username owns the style, and StyleID names it (for example "mapbox" and
	// "light-v11"). Both are required when the proxy is enabled.
	Username string `yaml:"username"`
	StyleID  string `yaml:"style_id"`
	// MaxZoom is the highest zoom served, 1 to 22; zero keeps the default 18.
	MaxZoom int `yaml:"max_zoom"`
	// RatePerMinute limits requests per client address; zero keeps the default.
	RatePerMinute int `yaml:"rate_per_minute"`
	// UpstreamTimeout bounds one Mapbox request including its body; zero keeps the default.
	UpstreamTimeout time.Duration `yaml:"upstream_timeout"`
	// MaxResponseBytes caps a tile read from Mapbox; zero keeps the default.
	MaxResponseBytes int64 `yaml:"max_response_bytes"`
}

// String and GoString keep the token out of anything that prints the configuration.
func (c Config) String() string   { return c.redacted() }
func (c Config) GoString() string { return c.redacted() }

func (c Config) redacted() string {
	token := ""
	if c.Token != "" {
		token = "[redacted]"
	}
	return fmt.Sprintf("tiles.Config{Enabled:%t Token:%q Username:%q StyleID:%q}", c.Enabled, token, c.Username, c.StyleID)
}

func (c Config) maxZoom() int {
	if c.MaxZoom == 0 {
		return defaultMaxZoom
	}
	return c.MaxZoom
}

func (c Config) ratePerMinute() int64 {
	if c.RatePerMinute == 0 {
		return defaultRatePerMinute
	}
	return int64(c.RatePerMinute)
}

func (c Config) upstreamTimeout() time.Duration {
	if c.UpstreamTimeout == 0 {
		return defaultUpstreamTimeout
	}
	return c.UpstreamTimeout
}

func (c Config) maxResponseBytes() int64 {
	if c.MaxResponseBytes == 0 {
		return defaultMaxResponseBytes
	}
	return c.MaxResponseBytes
}

// Validate rejects an enabled configuration that cannot work. Errors name the
// field only and never repeat a value, so a startup failure cannot leak the token.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Token == "" || c.Username == "" || c.StyleID == "" {
		return fmt.Errorf("mapbox_tiles: token, username and style_id are required when enabled")
	}
	if !tokenPattern.MatchString(c.Token) || len(c.Token) > 512 {
		return fmt.Errorf("mapbox_tiles.token is not a Mapbox access token")
	}
	if !namePattern.MatchString(c.Username) {
		return fmt.Errorf("mapbox_tiles.username must be 1-64 letters, digits, '-' or '_'")
	}
	if !namePattern.MatchString(c.StyleID) {
		return fmt.Errorf("mapbox_tiles.style_id must be 1-64 letters, digits, '-' or '_'")
	}
	if c.MaxZoom < 0 || c.MaxZoom > hardMaxZoom {
		return fmt.Errorf("mapbox_tiles.max_zoom must be between 0 and %d", hardMaxZoom)
	}
	if c.RatePerMinute < 0 || c.RatePerMinute > maxRatePerMinute {
		return fmt.Errorf("mapbox_tiles.rate_per_minute must be between 0 and %d", maxRatePerMinute)
	}
	if c.UpstreamTimeout < 0 || c.UpstreamTimeout > maxUpstreamTimeout {
		return fmt.Errorf("mapbox_tiles.upstream_timeout must be between 0 and %s", maxUpstreamTimeout)
	}
	if c.MaxResponseBytes != 0 && (c.MaxResponseBytes < minResponseBytes || c.MaxResponseBytes > maxResponseBytesCap) {
		return fmt.Errorf("mapbox_tiles.max_response_bytes must be between %d and %d", minResponseBytes, maxResponseBytesCap)
	}
	return nil
}
