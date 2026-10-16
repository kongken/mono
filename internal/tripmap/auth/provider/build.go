package provider

import "github.com/kongken/mono/internal/tripmap/config"

// BuildRegistry constructs a Registry from the configured providers. Providers
// with missing credentials are skipped. It returns an empty registry when
// OAuth is not configured.
func BuildRegistry(cfg config.AuthConfig) *Registry {
	reg := NewRegistry()
	for name, pc := range cfg.OAuthProviders {
		if !pc.Enabled() {
			continue
		}
		switch name {
		case "github":
			if p := NewGitHub(GitHubConfig{ClientID: pc.ClientID, ClientSecret: pc.ClientSecret, RedirectURL: pc.RedirectURL, Scopes: pc.Scopes, DisplayName: pc.DisplayName}); p != nil {
				reg.Register(p)
			}
		case "google":
			if p := NewGoogle(GoogleConfig{ClientID: pc.ClientID, ClientSecret: pc.ClientSecret, RedirectURL: pc.RedirectURL, Scopes: pc.Scopes, DisplayName: pc.DisplayName}); p != nil {
				reg.Register(p)
			}
		case "logto":
			if p := NewLogto(LogtoConfig{IssuerURL: pc.IssuerURL, ClientID: pc.ClientID, ClientSecret: pc.ClientSecret, RedirectURL: pc.RedirectURL, Scopes: pc.Scopes, DisplayName: pc.DisplayName}); p != nil {
				reg.Register(p)
			}
		}
	}
	return reg
}
