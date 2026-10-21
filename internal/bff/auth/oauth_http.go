package auth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/kongken/mono/internal/bff/oauth"
	"github.com/kongken/mono/internal/bff/oauth/provider"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/platform/httpx"
)

var oauthStatePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`)

// ServeOAuthHTTP handles top-level navigation only. Every attempt has its own
// host-only binding cookie, so separate tabs never overwrite each other's state.
func (h *Handler) ServeOAuthHTTP(w http.ResponseWriter, r *http.Request, clientIPHeader string) {
	pattern := "/auth/login"
	if r.URL.Path != "/auth/login" {
		pattern = "/auth/callback/:provider"
	}
	httpx.RedactSpan(r, pattern)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	fail := func(status int) { http.Error(w, "登录未完成，请重新发起登录", status) }
	if r.Method != http.MethodGet {
		fail(http.StatusMethodNotAllowed)
		return
	}
	if h.svc.oauthState == nil {
		fail(http.StatusServiceUnavailable)
		return
	}
	ip := httpx.ClientAddr(clientIPHeader, r.Header, r.RemoteAddr)
	if err := h.svc.allow(ctx, "oauth:ip:"+ip, oauthPerIP, oauthWindow); err != nil {
		fail(http.StatusTooManyRequests)
		return
	}
	if r.URL.Path == "/auth/login" {
		h.beginHTTP(w, r)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/auth/callback/")
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q["state"]) != 1 || !oauthStatePattern.MatchString(q.Get("state")) {
		fail(http.StatusBadRequest)
		return
	}
	state := q.Get("state")
	attempt, err := h.svc.oauthState.Consume(ctx, state)
	cookie := h.bindingCookie(state, "", -1)
	http.SetCookie(w, cookie)
	binding, cookieErr := r.Cookie(cookie.Name)
	if err != nil || cookieErr != nil || attempt.BrowserHash == "" || subtle.ConstantTimeCompare([]byte(session.HashToken(binding.Value)), []byte(attempt.BrowserHash)) != 1 || attempt.Provider != name || attempt.RedirectURI != h.svc.cfg.OAuthProviders[name].RedirectURL {
		fail(http.StatusBadRequest)
		return
	}
	app, ok := h.svc.cfg.OAuthApps[attempt.App]
	target, targetErr := app.ReturnURL(strings.TrimPrefix(attempt.ReturnURL, app.Origin))
	if !ok || targetErr != nil || target != attempt.ReturnURL {
		fail(http.StatusBadRequest)
		return
	}
	if q.Get("error") != "" || len(q["code"]) != 1 || q.Get("code") == "" {
		fail(http.StatusBadRequest)
		return
	}
	_, expires, token, err := h.svc.finishOAuth(ctx, name, q.Get("code"), attempt)
	if err != nil {
		fail(http.StatusUnauthorized)
		return
	}
	session.SetSessionCookie(w.Header(), h.svc.cookies, token, expires)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (h *Handler) beginHTTP(w http.ResponseWriter, r *http.Request) {
	fail := func(status int) { http.Error(w, "登录请求无效，请重新发起登录", status) }
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		fail(http.StatusBadRequest)
		return
	}
	for key, values := range q {
		if (key != "app" && key != "provider" && key != "return_to") || len(values) != 1 {
			fail(http.StatusBadRequest)
			return
		}
	}
	app, ok := h.svc.cfg.OAuthApps[q.Get("app")]
	target, err := app.ReturnURL(q.Get("return_to"))
	if !ok || err != nil {
		fail(http.StatusBadRequest)
		return
	}
	name := q.Get("provider")
	if name == "" {
		name = "logto"
	}
	p, err := h.svc.oauthRegistry().Get(name)
	redirect := h.svc.cfg.OAuthProviders[name].RedirectURL
	if err != nil || redirect == "" {
		fail(http.StatusBadRequest)
		return
	}
	binding, err := session.NewCSRF()
	if err != nil {
		fail(500)
		return
	}
	var verifier, challenge string
	pk, pkce := p.(provider.PKCEProvider)
	if pkce {
		verifier, challenge, err = provider.NewPKCE()
		if err != nil {
			fail(500)
			return
		}
	}
	state, err := h.svc.oauthState.CreateAttempt(r.Context(), oauth.OAuthState{Provider: name, CodeVerifier: verifier, App: q.Get("app"), ReturnURL: target, RedirectURI: redirect, BrowserHash: session.HashToken(binding)})
	if err != nil {
		fail(503)
		return
	}
	http.SetCookie(w, h.bindingCookie(state, binding, int(oauth.OAuthStateTTL.Seconds())))
	authorize := p.AuthorizeURL(state)
	if pkce {
		authorize = pk.AuthorizePKCEURL(state, challenge)
	}
	http.Redirect(w, r, authorize, http.StatusFound)
}

func (h *Handler) bindingCookie(state, value string, maxAge int) *http.Cookie {
	name := "kongken_oauth_" + state
	if h.svc.cookies.Secure {
		name = "__Host-" + name
	}
	return &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: h.svc.cookies.Secure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
}
