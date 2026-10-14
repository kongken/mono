package notification

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// PublicIP reports whether addr is a routable public unicast address. Push
// endpoints come from browsers, so the server only ever connects to such
// addresses: a crafted endpoint can't reach loopback, private networks, cloud
// metadata services or other internal hosts.
func PublicIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsValid() && addr.IsGlobalUnicast() && !addr.IsPrivate() && !addr.IsLoopback() &&
		!addr.IsLinkLocalUnicast() && !addr.IsMulticast() && !addr.IsUnspecified() && !cgnat.Contains(addr)
}

// ValidPushEndpoint checks the URL shape of a push endpoint: https, a host name
// (not an IP literal), default port, no credentials, bounded length.
func ValidPushEndpoint(raw string) error {
	if len(raw) == 0 || len(raw) > 2048 {
		return errors.New("push endpoint must be 1-2048 characters")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return errors.New("push endpoint must be an https URL")
	}
	if p := u.Port(); p != "" && p != "443" {
		return errors.New("push endpoint must use the default https port")
	}
	if _, err := netip.ParseAddr(u.Hostname()); err == nil || u.Hostname() == "localhost" {
		return errors.New("push endpoint must use a public host name")
	}
	return nil
}

// guardedClient connects only to public addresses (checked on the resolved
// address at dial time, so DNS rebinding cannot slip past), never follows
// redirects, ignores proxy environment variables and times out.
func guardedClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || !PublicIP(ip) {
				return errors.New("destination is not a public address")
			}
			return nil
		},
	}
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: 5 * time.Second,
			MaxIdleConns:        10,
			IdleConnTimeout:     30 * time.Second,
		},
	}
}
