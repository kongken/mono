package app

import (
	"net/http"

	"github.com/kongken/mono/internal/usersrv/config"
	"github.com/kongken/mono/internal/usersrv/externalprovider"
)

// buildExternalProviders is local assembly only; construction/startup performs
// no discovery/JWKS requests. Configuration replacement must rebuild this
// snapshot and the later challenge layer must reject obsolete config versions.
func buildExternalProviders(auth config.AuthConfig, transport http.RoundTripper) (map[externalprovider.ProviderName]externalprovider.ExternalProvider, error) {
	p := auth.External.Providers[string(externalprovider.GoogleName)]
	cfg := externalprovider.GoogleConfig{Enabled: auth.External.ProviderEnabled(string(externalprovider.GoogleName)),
		ClientID: p.ClientID, RedirectURI: p.RedirectURI, ConfigVersion: p.ConfigVersion, AllowLoopbackHTTP: auth.AllowLoopbackHTTP}
	if cfg.Enabled {
		secret, err := p.ClientSecretValue()
		if err != nil {
			return nil, err
		}
		cfg.ClientSecret = secret
	}
	g, err := externalprovider.NewGoogle(cfg, transport)
	if err != nil {
		return nil, err
	}
	p = auth.External.Providers[string(externalprovider.GitHubName)]
	ghCfg := externalprovider.GitHubConfig{Enabled: auth.External.ProviderEnabled(string(externalprovider.GitHubName)),
		ClientID: p.ClientID, RedirectURI: p.RedirectURI, ConfigVersion: p.ConfigVersion, AllowLoopbackHTTP: auth.AllowLoopbackHTTP}
	if ghCfg.Enabled {
		secret, err := p.ClientSecretValue()
		if err != nil {
			return nil, err
		}
		ghCfg.ClientSecret = secret
	}
	gh, err := externalprovider.NewGitHub(ghCfg, transport)
	if err != nil {
		return nil, err
	}
	return map[externalprovider.ProviderName]externalprovider.ExternalProvider{externalprovider.GoogleName: g, externalprovider.GitHubName: gh}, nil
}
