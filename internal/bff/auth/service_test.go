package auth_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/auth"
	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/ratelimit"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/bff/testutil"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

func TestRegisterLoginMeLogout(t *testing.T) {
	db, rdb, prefix := testutil.Backing(t)
	cookies := session.CookieConfig{Path: "/", SameSite: http.SameSiteLaxMode}
	svc := auth.NewService(&config.AuthConfig{SessionTTL: time.Hour}, cookies, db, ratelimit.New(rdb, prefix), nil)
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
