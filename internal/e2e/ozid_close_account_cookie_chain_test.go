package e2e_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/ent/accountdeletion"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

// While a deletion is unfinished, the session Cookie is the only credential to see, refresh and
// retry it. These tests cover every request that can replace or clear that Cookie: sign-in,
// sign-out, ending the current session and changing the password.

// F1: signing in as another account from the browser that holds the Cookie of an unfinished
// deletion must not replace that Cookie. The sign-in is refused, the deletion can still be read,
// refreshed and retried to its end, and the other account signs in once it is done.
func TestChainCloseAccountCookieSurvivesAnotherAccountSigningIn(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	a, aID, aKey := s.account("over_a")
	s.account("over_b")

	s.failMetro.Store(true)
	req := &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "over-a-1"}
	if err := a.DeleteAccount(ctx, req, aID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("deletion: %v", err)
	}
	token := a.SessionCookie()

	// Same jar, another account.
	err := a.Login(ctx, "over_b", "", "")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("signing in over an unfinished deletion must be refused: %v", err)
	}
	if a.SessionCookie() != token {
		t.Fatal("the sign-in replaced the Cookie of an unfinished deletion")
	}
	// A wrong password gets the same answer: the refusal is about the browser's state, so it is
	// no oracle for anyone's credentials.
	if _, err = a.LoginWithEmail(ctx, emailFor("over_b"), "wrong-password-0123"); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("a wrong password: %v", err)
	}
	if a.SessionCookie() != token {
		t.Fatal("a failed sign-in replaced the Cookie")
	}

	if d, err := a.GetAccountDeletion(ctx); err != nil || d.GetState() != bffv1.AccountDeletionState_ACCOUNT_DELETION_STATE_PENDING {
		t.Fatalf("read after the refused sign-in: %v %v", d, err)
	}
	if _, err = a.RetryAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("retry while metro is down: %v", err)
	}
	if a.SessionCookie() != token {
		t.Fatal("a retry changed the Cookie")
	}
	s.failMetro.Store(false)
	d, err := a.RetryAccountDeletion(ctx)
	if err != nil || d.GetState() != bffv1.AccountDeletionState_ACCOUNT_DELETION_STATE_COMPLETED {
		t.Fatalf("final retry: %v %v", d, err)
	}
	if s.hasObject(aKey) {
		t.Fatal("media survived the deletion")
	}
	if a.SessionCookie() != "" {
		t.Fatal("the finished deletion did not clear the Cookie")
	}
	// Only the finished deletion frees the browser.
	if err = a.Login(ctx, "over_b", "", ""); err != nil {
		t.Fatalf("the other account must sign in once the deletion is done: %v", err)
	}
}

// Signing out and signing in from a browser with a pending deletion keep the Cookie, whatever
// the answer. (Ending the current session and changing the password need a live session, which
// a closed account no longer has; the race tests below reach them with a request that was
// accepted just before the deletion began.)
func TestChainCloseAccountCookieModifyingCallsLeaveAPendingDeletionAlone(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	a, aID, _ := s.account("paths_a")
	s.failMetro.Store(true)
	if err := a.DeleteAccount(ctx, &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "paths-a-1"}, aID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("deletion: %v", err)
	}
	token := a.SessionCookie()
	calls := []struct {
		name string
		call func() error
	}{
		{"logout", func() error { return a.Logout(ctx) }},
		{"revoke session", func() error { return a.RevokeSession(ctx, "any") }},
		{"change password", func() error { return a.ChangePassword(ctx, E2EPassword, "another-password-0123") }},
		{"login of the same account", func() error { return a.Login(ctx, "paths_a", "", "") }},
	}
	for _, c := range calls {
		_ = c.call() // a refusal is fine; the Cookie is what must stay
		if a.SessionCookie() != token {
			t.Fatalf("%s changed the Cookie of an unfinished deletion", c.name)
		}
	}
	if d, err := a.GetAccountDeletion(ctx); err != nil || d.GetState() != bffv1.AccountDeletionState_ACCOUNT_DELETION_STATE_PENDING {
		t.Fatalf("read: %v %v", d, err)
	}
}

// Server-side order, sign-out first: the session is revoked, so the deletion cannot start any
// more. Nothing is persisted and the Cookie goes with the sign-out; no Cookie is orphaned.
func TestChainCloseAccountCannotStartAfterTheSignOutRevokedTheSession(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	a, aID, _ := s.account("order_a")
	stale := a.SessionCookie()
	if err := a.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	// A page of the same browser whose jar still held the old Cookie (the sign-out response
	// reached it late) tries to start the deletion with it.
	a.SetSessionCookie(stale)
	err := a.DeleteAccount(ctx, &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "order-a-1"}, aID)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a revoked session must not start a deletion: %v", err)
	}
	if n := s.BFFDB.AccountDeletion.Query().Where(accountdeletion.UserID(aID)).CountX(ctx); n != 0 {
		t.Fatalf("%d workflows persisted for a revoked session", n)
	}
}

// Server-side order, deletion first: a sign-out that arrives after the deletion was persisted
// (a late request) is answered without touching the Cookie.
func TestChainCloseAccountLateSignOutRequestKeepsTheCookie(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	a, aID, _ := s.account("late_a")
	s.failMetro.Store(true)
	if err := a.DeleteAccount(ctx, &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "late-a-1"}, aID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("deletion: %v", err)
	}
	token := a.SessionCookie()
	if err := a.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	if a.SessionCookie() != token {
		t.Fatal("a sign-out request that arrived after the deletion cleared its Cookie")
	}
}

// The remaining server-side interleaving: a sign-out (or any call that revokes the session) that
// was accepted just before the deletion began, and a deletion that was accepted just before the
// session was revoked, run at the same time. Each side writes first and reads the other's write
// second, so at least one of them sees the other: either the deletion is not persisted (it
// answers UNAUTHENTICATED) or the clearing call leaves the Cookie alone. Many concurrent rounds
// of each call must never end with a persisted deletion and no Cookie.
func TestChainCloseAccountConcurrentSessionEndNeverOrphansADeletion(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	s.failMetro.Store(true)
	kinds := []struct {
		name string
		end  func(b *Browser, sessionID string) error
	}{
		{"logout", func(b *Browser, _ string) error { return b.Logout(ctx) }},
		{"revoke current session", func(b *Browser, id string) error { return b.RevokeSession(ctx, id) }},
		{"change password", func(b *Browser, _ string) error { return b.ChangePassword(ctx, E2EPassword, "another-password-0123") }},
	}
	const rounds = 10
	for _, k := range kinds {
		persisted := 0
		for i := range rounds {
			subject := fmt.Sprintf("race_%d_%d", len(k.name), i)
			b := s.FreshBrowser()
			id, err := b.Register(ctx, subject, "", subject)
			if err != nil {
				t.Fatal(err)
			}
			var sessionID string
			sessions, err := b.ListSessions(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, ss := range sessions {
				if ss.GetCurrent() {
					sessionID = ss.GetId()
				}
			}
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_ = b.DeleteAccount(ctx, &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: fmt.Sprintf("race-%d-%d", len(k.name), i)}, id)
			}()
			go func() {
				defer wg.Done()
				_ = k.end(b, sessionID)
			}()
			wg.Wait()
			if s.BFFDB.AccountDeletion.Query().Where(accountdeletion.UserID(id)).CountX(ctx) == 0 {
				continue
			}
			persisted++
			if b.SessionCookie() == "" {
				t.Fatalf("%s round %d: a persisted deletion lost its Cookie", k.name, i)
			}
			if _, err = b.GetAccountDeletion(ctx); err != nil {
				t.Fatalf("%s round %d: a persisted deletion cannot be read: %v", k.name, i, err)
			}
		}
		t.Logf("%s: %d of %d rounds persisted a deletion, none without its Cookie", k.name, persisted, rounds)
	}
}
