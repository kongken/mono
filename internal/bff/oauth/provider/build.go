package provider

import "github.com/kongken/mono/internal/bff/config"

// BuildRegistry registers only Logto. Social connections belong to Logto,
// never to parallel application-owned identity providers.
func BuildRegistry(cfg config.AuthConfig) *Registry {
	reg := NewRegistry()
	pc := cfg.OAuthProviders["logto"]
	if pc.Enabled() {
		if p := NewLogto(LogtoConfig{IssuerURL: pc.IssuerURL, ClientID: pc.ClientID, ClientSecret: pc.ClientSecret, RedirectURL: pc.RedirectURL, Scopes: pc.Scopes, DisplayName: pc.DisplayName}); p != nil {
			reg.Register(p)
		}
	}
	return reg
}
