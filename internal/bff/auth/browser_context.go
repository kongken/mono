package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/kongken/mono/internal/bff/ent"
	"github.com/kongken/mono/internal/bff/ent/browsercontext"
	entsession "github.com/kongken/mono/internal/bff/ent/session"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/platform/rpcauth"
)

const browserCookie = "kongken_browser"

func (s *Service) browserLifetime() time.Duration { return max(30*24*time.Hour, s.SessionLifetime()) }

func uniqueCookie(h http.Header, name string) string {
	r := &http.Request{Header: h}
	value := ""
	n := 0
	for _, c := range r.Cookies() {
		if c.Name == name {
			value = c.Value
			n++
		}
	}
	if n != 1 || len(value) > 256 {
		return ""
	}
	return value
}
func privateCookieName(cfg session.CookieConfig, name string) string {
	if cfg.Secure {
		return "__Host-" + name
	}
	return name
}
func setPrivateCookie(h http.Header, cfg session.CookieConfig, name, value string, expires time.Time) {
	h.Add("Set-Cookie", (&http.Cookie{Name: privateCookieName(cfg, name), Value: value, Path: "/", HttpOnly: true, Secure: cfg.Secure, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: int(time.Until(expires).Seconds())}).String())
}
func (s *Service) browserID(h http.Header) string {
	token := uniqueCookie(h, privateCookieName(s.cookies, browserCookie))
	if token == "" {
		return ""
	}
	return session.HashToken(token)
}
func (s *Service) issueBrowser(ctx context.Context, h http.Header) (string, error) {
	id := s.browserID(h)
	if id != "" {
		if ok, err := s.db.BrowserContext.Query().Where(browsercontext.ID(id), browsercontext.ExpiresAtGT(s.now())).Exist(ctx); err != nil {
			return "", internal(err)
		} else if ok {
			return "", nil
		}
	}
	if err := s.allow(ctx, "external:browser:"+rpcauth.CallFrom(ctx).ClientIP, 60, 15*time.Minute); err != nil {
		return "", err
	}
	token, hash, err := session.NewToken()
	if err != nil {
		return "", internal(err)
	}
	_, err = s.db.BrowserContext.Create().SetID(hash).SetExpiresAt(s.now().Add(s.browserLifetime())).Save(ctx)
	if err != nil {
		return "", internal(err)
	}
	return token, nil
}

func (s *Service) advanceBrowser(ctx context.Context, h http.Header) error {
	id := s.browserID(h)
	if id == "" {
		return nil
	}
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return internal(err)
	}
	defer tx.Rollback()
	b, err := tx.BrowserContext.Query().Where(browsercontext.ID(id)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return internal(err)
	}
	var row *ent.Session
	if token := session.TokenFromRequest(h); token != "" {
		row, err = tx.Session.Query().Where(entsession.TokenHash(session.HashToken(token))).Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return internal(err)
		}
	}
	if row != nil {
		if row.BrowserContextID != nil && (*row.BrowserContextID != id || row.BrowserGeneration == nil || *row.BrowserGeneration != b.Generation) {
			return nil
		}
		if row.BrowserContextID == nil && b.Generation != 0 {
			return nil
		}
	} else {
		// An anonymous logout cancels outstanding anonymous flows, but a delayed
		// anonymous request cannot invalidate a newly established live session.
		active, err := tx.Session.Query().Where(entsession.BrowserContextID(id), entsession.BrowserGeneration(b.Generation), entsession.Revoked(false), entsession.ExpiresAtGT(s.now())).Exist(ctx)
		if err != nil {
			return internal(err)
		}
		if active {
			return nil
		}
	}
	if err = tx.BrowserContext.UpdateOne(b).AddGeneration(1).Exec(ctx); err != nil {
		return internal(err)
	}
	return tx.Commit()
}
func (s *Service) validBrowserGeneration(ctx context.Context, row *ent.Session) error {
	if row.BrowserContextID == nil {
		return nil
	}
	if row.BrowserGeneration == nil {
		return errUnauth
	}
	ok, err := s.db.BrowserContext.Query().Where(browsercontext.ID(*row.BrowserContextID), browsercontext.Generation(*row.BrowserGeneration), browsercontext.ExpiresAtGT(s.now())).Exist(ctx)
	if err != nil {
		return internal(err)
	}
	if !ok {
		return errUnauth
	}
	return nil
}

var errStateInvalid = errors.New("STATE_INVALID")
