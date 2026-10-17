package e2e_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

func TestChainListAndRevokeSessions(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()

	a := h.FreshBrowser()
	if _, err := a.Register(ctx, "sess_a", "password1", "A"); err != nil {
		t.Fatal(err)
	}
	b := h.FreshBrowser()
	if err := b.Login(ctx, "sess_a", "password1", b.Origin); err != nil {
		t.Fatal(err)
	}

	listReq := connect.NewRequest(&bffv1.ListSessionsRequest{})
	a.withTripmapHeaders(listReq)
	list, err := a.Auth.ListSessions(ctx, listReq)
	if err != nil || len(list.Msg.GetSessions()) != 2 {
		t.Fatalf("list sessions: %v err=%v", list, err)
	}
	var otherID string
	for _, s := range list.Msg.GetSessions() {
		if s.GetCurrent() {
			if s.GetId() == "" {
				t.Fatal("current session missing id")
			}
		} else {
			otherID = s.GetId()
		}
	}
	if otherID == "" {
		t.Fatal("expected other session")
	}

	stranger := h.FreshBrowser()
	if _, err := stranger.Register(ctx, "sess_b", "password1", "B"); err != nil {
		t.Fatal(err)
	}
	revReq := connect.NewRequest(&bffv1.RevokeSessionRequest{Id: otherID})
	stranger.writeHeaders(revReq, stranger.refreshCSRF(ctx), stranger.Origin)
	if _, err := stranger.Auth.RevokeSession(ctx, revReq); err == nil || connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("stranger revoke: %v", err)
	}

	revReq = connect.NewRequest(&bffv1.RevokeSessionRequest{Id: otherID})
	a.writeHeaders(revReq, a.refreshCSRF(ctx), a.Origin)
	if _, err := a.Auth.RevokeSession(ctx, revReq); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Me(ctx); err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("revoked device should be unauthenticated: %v", err)
	}
	if _, err = a.Me(ctx); err != nil {
		t.Fatalf("current device should stay signed in: %v", err)
	}
}

func TestChainRevokeSessionCSRF(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	b := h.FreshBrowser()
	if _, err := b.Register(ctx, "csrf_sess", "password1", "C"); err != nil {
		t.Fatal(err)
	}
	req := connect.NewRequest(&bffv1.RevokeSessionRequest{Id: "not-used"})
	b.writeHeaders(req, "", b.Origin)
	if _, err := b.Auth.RevokeSession(ctx, req); err == nil || connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("missing csrf on revoke: %v", err)
	}
}
