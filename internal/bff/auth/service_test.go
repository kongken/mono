package auth_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/auth"
	"github.com/kongken/mono/internal/bff/config"
	entsession "github.com/kongken/mono/internal/bff/ent/session"
	"github.com/kongken/mono/internal/bff/ratelimit"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/bff/testutil"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

func TestRegisterLoginMeLogout(t *testing.T) {
	db, rdb, prefix := testutil.Backing(t)
	cookies := session.CookieConfig{Path: "/", SameSite: http.SameSiteLaxMode}
	svc := auth.NewService(&config.AuthConfig{SessionTTL: time.Hour}, cookies, db, ratelimit.New(rdb, prefix), nil, auth.OAuthDeps{})
	h := auth.NewHandler(svc, nil, time.Hour)
	ctx := context.Background()

	reg, exp, tok, err := svc.Register(ctx, "127.0.0.1", &bffv1.RegisterRequest{Username: "alice", Password: "password1", DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	if reg.User.Username != "alice" || tok == "" || exp.Before(time.Now()) {
		t.Fatalf("register: %+v token=%q", reg, tok)
	}
	hdr := http.Header{}
	hdr.Set("Cookie", session.CookieName+"="+tok)
	bs, err := h.Authenticate(ctx, hdr)
	if err != nil || bs.UserID != reg.User.Id {
		t.Fatalf("session: %+v err=%v", bs, err)
	}
	me, err := svc.Me(ctx, bs.UserID)
	if err != nil || me.User.Username != "alice" {
		t.Fatal(err)
	}
	if err = svc.Logout(ctx, bs.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = h.Authenticate(ctx, hdr); err == nil {
		t.Fatal("expected revoked session to fail")
	}
}

func TestListAndRevokeSessions(t *testing.T) {
	db, rdb, prefix := testutil.Backing(t)
	cookies := session.CookieConfig{Path: "/", SameSite: http.SameSiteLaxMode}
	svc := auth.NewService(&config.AuthConfig{SessionTTL: time.Hour}, cookies, db, ratelimit.New(rdb, prefix), nil, auth.OAuthDeps{})
	ctx := context.Background()

	reg, _, tok1, err := svc.Register(ctx, "127.0.0.1", &bffv1.RegisterRequest{Username: "erin", Password: "password1", DisplayName: "Erin"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, tok2, err := svc.Login(ctx, "127.0.0.1", &bffv1.LoginRequest{Username: "erin", Password: "password1"})
	if err != nil {
		t.Fatal(err)
	}
	h := auth.NewHandler(svc, nil, time.Hour)
	hdr1 := http.Header{}
	hdr1.Set("Cookie", session.CookieName+"="+tok1)
	bs1, err := h.Authenticate(ctx, hdr1)
	if err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListSessions(ctx, bs1.UserID, bs1.SessionID)
	if err != nil || len(list.Sessions) != 2 {
		t.Fatalf("list: %+v err=%v", list, err)
	}
	var otherID string
	for _, s := range list.Sessions {
		if !s.Current {
			otherID = s.Id
		}
	}
	if otherID == "" {
		t.Fatal("expected non-current session")
	}
	stranger, _, _, err := svc.Register(ctx, "127.0.0.1", &bffv1.RegisterRequest{Username: "mallory", Password: "password1", DisplayName: "M"})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.RevokeSession(ctx, stranger.User.Id, otherID); err == nil || connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("stranger revoke: %v", err)
	}
	hdr2 := http.Header{}
	hdr2.Set("Cookie", session.CookieName+"="+tok2)
	bs2, err := h.Authenticate(ctx, hdr2)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.RevokeSession(ctx, bs2.UserID, otherID); err != nil {
		t.Fatal(err)
	}
	if _, err = h.Authenticate(ctx, hdr2); err == nil {
		t.Fatal("revoked session should fail auth")
	}
	if _, err = h.Authenticate(ctx, hdr1); err != nil {
		t.Fatal("current session should stay valid")
	}
	if n, _ := db.Session.Query().Where(entsession.Revoked(false)).Count(ctx); n != 2 {
		t.Fatalf("active sessions = %d", n)
	}
	_ = reg
}

func TestCSRFRejectsLogin(t *testing.T) {
	if err := session.CheckWrite(http.Header{}, nil, "localhost"); err == nil {
		t.Fatal("missing csrf should fail")
	}
	h := http.Header{}
	h.Set(session.CSRFHeader, "a")
	h.Set("Cookie", session.CSRFCookieName+"=b")
	if err := session.CheckWrite(h, nil, ""); err == nil {
		t.Fatal("mismatched csrf should fail")
	}
	_ = connect.CodePermissionDenied
}
