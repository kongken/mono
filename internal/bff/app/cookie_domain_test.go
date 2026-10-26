package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kongken/mono/internal/bff/navigation"
	"github.com/kongken/mono/internal/bff/session"
)

func TestPublicAPIWiresConfiguredCookieDomain(t *testing.T) {
	for _, domain := range []string{"", "example.com"} {
		t.Run("domain="+domain, func(t *testing.T) {
			h, clients := publicAPIFixture(t, navigation.Config{
				APIOrigin: "https://api.example.com",
				Apps:      map[string]navigation.App{"ozid": {Origin: "https://id.example.com"}},
			})
			h.cfg.Auth.CookieDomain = domain
			handler, err := h.publicAPI(clients)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/bff.v1.AuthService/IssueCSRFToken", strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Connect-Protocol-Version", "1")
			req.Header.Set("Origin", "https://id.example.com")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("CSRF request failed: HTTP %d", rr.Code)
			}
			found := false
			for _, cookie := range rr.Result().Cookies() {
				if cookie.Name == session.CSRFCookieName && cookie.MaxAge > 0 {
					found = true
					if cookie.Domain != domain || !cookie.Secure || cookie.HttpOnly {
						t.Fatal("public API lost its Cookie domain/security configuration")
					}
				}
			}
			if !found {
				t.Fatal("CSRF Cookie missing")
			}
		})
	}
}
