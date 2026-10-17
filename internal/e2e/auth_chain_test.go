package e2e_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	bffsession "github.com/kongken/mono/internal/bff/ent/session"
	"github.com/kongken/mono/internal/bff/session"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestChainCookieAuthAndFootprints(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()

	a := h.FreshBrowser()
	aID, err := a.Register(ctx, "alice", "password1", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if a.SessionCookie() == "" || !strings.HasPrefix(a.SessionCookie(), "bf_") {
		t.Fatal("expected session cookie after register")
	}
	me, err := a.Me(ctx)
	if err != nil || me.GetId() != aID {
		t.Fatalf("me after register: %v err=%v", me, err)
	}
	// Same jar simulates refresh.
	me2, err := a.Me(ctx)
	if err != nil || me2.GetUsername() != "alice" {
		t.Fatal(err)
	}
	if err = a.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Me(ctx); err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("me after logout: %v", err)
	}

	b := h.FreshBrowser()
	bID, err := b.Register(ctx, "bob", "password1", "Bob")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.TripmapDB.CityVisit.Create().
		SetUserID(bID).
		SetRegionCode("330100").SetRegionName("杭州市").SetProvinceCode("330000").SetColor("#4b937d").
		Save(ctx); err != nil {
		t.Fatal(err)
	}
	bFoot, err := b.GetFootprints(ctx)
	if err != nil || len(bFoot.GetVisits()) == 0 {
		t.Fatalf("bob footprints: %v err=%v", bFoot, err)
	}
	a2 := h.FreshBrowser()
	if _, err = a2.Register(ctx, "alice2", "password1", "A2"); err != nil {
		t.Fatal(err)
	}
	aFoot, err := a2.GetFootprints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range aFoot.GetVisits() {
		if v.GetCode() == "330100" {
			t.Fatal("user A must not read user B city visit")
		}
	}
}

func TestChainCSRFAndOriginRejected(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	b := h.FreshBrowser()

	req := connect.NewRequest(&bffv1.LoginRequest{Username: "x", Password: "password1"})
	b.writeHeaders(req, "", "")
	if _, err := b.Auth.Login(ctx, req); err == nil || connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("missing csrf: %v", err)
	}

	csrf := b.refreshCSRF(ctx)
	req = connect.NewRequest(&bffv1.LoginRequest{Username: "x", Password: "password1"})
	b.writeHeaders(req, csrf, "https://evil.example.com")
	if _, err := b.Auth.Login(ctx, req); err == nil || connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("bad origin: %v", err)
	}

	req = connect.NewRequest(&bffv1.LoginRequest{Username: "x", Password: "password1"})
	b.writeHeaders(req, "not-the-cookie", b.Origin)
	if _, err := b.Auth.Login(ctx, req); err == nil || connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("bad csrf: %v", err)
	}
}

func TestChainSessionStatesRejected(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	b := h.FreshBrowser()
	_, err := b.Register(ctx, "carol", "password1", "Carol")
	if err != nil {
		t.Fatal(err)
	}
	tok := b.SessionCookie()

	past := time.Now().UTC().Add(-time.Hour)
	hash := session.HashToken(tok)
	if _, err = h.BFFDB.Session.Update().Where(bffsession.TokenHash(hash)).SetExpiresAt(past).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Me(ctx); err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("expired session: %v", err)
	}

	b = h.FreshBrowser()
	_, _ = b.Register(ctx, "dave", "password1", "Dave")
	hash = session.HashToken(b.SessionCookie())
	if _, err = h.BFFDB.Session.Update().Where(bffsession.TokenHash(hash)).SetRevoked(true).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Me(ctx); err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("revoked session: %v", err)
	}

	b = h.FreshBrowser()
	uid, _ := b.Register(ctx, "eve", "password1", "Eve")
	if err = h.BFFDB.User.UpdateOneID(uid).SetDisabled(true).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Me(ctx); err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("disabled user: %v", err)
	}

	// Deletion in progress: session remains for Me (spec exception).
	b = h.FreshBrowser()
	now := time.Now().UTC()
	uid, _ = b.Register(ctx, "frank", "password1", "Frank")
	if err = h.BFFDB.User.UpdateOneID(uid).SetDisabled(true).SetDeletionRequestedAt(now).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Me(ctx); err != nil {
		t.Fatalf("deletion pending should allow Me: %v", err)
	}
}

func TestChainRegisterResponseHasNoSessionToken(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	b := h.FreshBrowser()
	csrf := b.refreshCSRF(ctx)
	req := connect.NewRequest(&bffv1.RegisterRequest{Username: "gina", Password: "password1", DisplayName: "Gina"})
	b.writeHeaders(req, csrf, b.Origin)
	res, err := b.Auth.Register(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := protojson.Marshal(res.Msg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "token") {
		t.Fatalf("response must not contain session token: %s", raw)
	}
	// Cookie holds the session; body must not echo bf_ prefix.
	if strings.Contains(string(raw), "bf_") {
		t.Fatalf("response leaked bearer-like secret: %s", raw)
	}
}

func TestChainLoginViaPasswordAfterRegister(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	b := h.FreshBrowser()
	if _, err := b.Register(ctx, "helen", "password1", "Helen"); err != nil {
		t.Fatal(err)
	}
	if err := b.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	b2 := h.FreshBrowser()
	if err := b2.Login(ctx, "helen", "password1", b2.Origin); err != nil {
		t.Fatal(err)
	}
	if _, err := b2.Me(ctx); err != nil {
		t.Fatal(err)
	}
}
