package externalprovider

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

func validateClientConfiguration(provider ProviderName, clientID, secret, redirect string, version int64, allowLoopbackHTTP bool) error {
	if strings.TrimSpace(clientID) != clientID || clientID == "" || len(clientID) > 1024 || strings.ContainsAny(clientID, "\r\n\x00") ||
		strings.TrimSpace(secret) != secret || secret == "" || len(secret) > 8192 || strings.ContainsAny(secret, "\r\n\x00") || version < 1 {
		return fmt.Errorf("%s provider requires a client ID, secret and positive config version", provider)
	}
	u, err := url.Parse(redirect)
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		u.RawPath != "" || u.Path != "/api/identity/callback/"+string(provider) || u.String() != redirect {
		return fmt.Errorf("%s provider requires an exact callback URL", provider)
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && allowLoopbackHTTP && loopback) {
		return fmt.Errorf("%s callback requires HTTPS (loopback HTTP needs development opt-in)", provider)
	}
	return nil
}
