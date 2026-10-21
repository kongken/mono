package config

import (
	"errors"
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
)

var oauthName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// OAuthApp fixes all return destinations on the server. AllowedPaths are exact,
// canonical paths without query strings, percent escapes or fragments.
type OAuthApp struct {
	Origin       string   `yaml:"origin"`
	DefaultPath  string   `yaml:"default_path"`
	AllowedPaths []string `yaml:"allowed_paths"`
}

func validReturnPath(p string) bool {
	for _, r := range p {
		if r <= 32 || r == 127 {
			return false
		}
	}
	return strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") && path.Clean(p) == p && !strings.ContainsAny(p, "%\\?#\r\n\t ")
}

func (a OAuthApp) ReturnURL(p string) (string, error) {
	if p == "" {
		p = a.DefaultPath
	}
	if !validReturnPath(p) || !slices.Contains(a.AllowedPaths, p) {
		return "", errors.New("return path is not allowed")
	}
	return a.Origin + p, nil
}

func validOAuthOrigin(raw string, dev bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || strings.ContainsAny(raw, "*\\?#") {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	return dev && u.Scheme == "http" && loopbackHost(u.Hostname())
}

func validCallback(raw, provider string, dev bool) bool {
	u, err := url.Parse(raw)
	if err != nil || strings.ContainsAny(raw, "?#\\") || u.Path != "/auth/callback/"+provider || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return false
	}
	return validOAuthOrigin(u.Scheme+"://"+u.Host, dev) && u.User == nil
}

func loopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || ip != nil && ip.IsLoopback()
}
