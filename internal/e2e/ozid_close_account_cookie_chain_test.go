package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/ent/accountdeletion"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

// The deletion recovery Cookie (kongken_del) is the credential to see, refresh and retry an account
// deletion. It is separate from the session Cookie: signing in, signing out and ending sessions
// never touch it, and the finished deletion removes it without signing anyone else out. Its value is
// the random token of the session that began the deletion (the workflow is bound to its digest),
// and nothing authenticates with it.

const pendingState = bffv1.AccountDeletionState_ACCOUNT_DELETION_STATE_PENDING
const completedState = bffv1.AccountDeletionState_ACCOUNT_DELETION_STATE_COMPLETED

func closeReq(id string) *bffv1.DeleteAccountRequest {
	return &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: id}
}

func persisted(s *closeAccountStack, userID string) int {
	return s.BFFDB.AccountDeletion.Query().Where(accountdeletion.UserID(userID)).CountX(context.Background())
}

// Nothing irreversible starts before the browser has handed back the recovery Cookie: the first
// call only sets it and is refused, with nothing persisted; a browser that does not keep it is
// refused again and again; with it the same call begins.
func TestChainRecoveryCookieIsConfirmedBeforeTheDeletionBegins(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	a, aID, aKey := s.account("conf_a")
	s.failMetro.Store(true)

	err := a.DeleteAccountOnce(ctx, closeReq("conf-a-1"), aID)
	if !isRecoveryNotStored(err) {
		t.Fatalf("a browser without the recovery Cookie must be refused: %v", err)
	}
	if persisted(s, aID) != 0 || !s.hasObject(aKey) {
		t.Fatal("something began before the recovery Cookie was confirmed")
	}
	if token := a.SessionCookie(); a.RecoveryCookie() != token || token == "" {
		t.Fatalf("the refusal must hand out the recovery Cookie (the session's random token): %q", a.RecoveryCookie())
	}
	// The recovery Cookie authenticates nothing: with the session Cookie gone it is just a value.
	token := a.SessionCookie()
	a.SetSessionCookie("")
	if _, err = a.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("the recovery Cookie must not authenticate: %v", err)
	}
	a.SetSessionCookie(token)

	// A browser that does not keep the Cookie never gets past the refusal.
	for range 2 {
		a.DropRecoveryCookie()
		if err = a.DeleteAccountOnce(ctx, closeReq("conf-a-1"), aID); !isRecoveryNotStored(err) {
			t.Fatalf("without the Cookie coming back: %v", err)
		}
		if persisted(s, aID) != 0 {
			t.Fatal("a deletion began for a browser that does not return the recovery Cookie")
		}
	}
	// With the Cookie stored, the same call begins.
	if err = a.DeleteAccountOnce(ctx, closeReq("conf-a-1"), aID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("with the recovery Cookie: %v", err)
	}
	if wf := s.workflowOf(ctx, aID); wf.Stage != "cleanup" {
		t.Fatalf("workflow: %+v", wf)
	}
	if a.RecoveryCookie() != token {
		t.Fatal("the recovery Cookie changed when the deletion began")
	}
}

// The start response is lost: the browser never sees whether it began. Because the Cookie was
// confirmed before, it is there anyway, and with the session Cookie gone and another account
// signed in the deletion can still be viewed, repeated with the same key and retried to its end.
func TestChainRecoveryAfterTheStartResponseIsLost(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	a, aID, aKey := s.account("lost_a")
	s.account("lost_b")
	drop := &dropResponse{match: "/DeleteAccount"}
	drop.wrap(a)
	s.failMetro.Store(true)

	if err := a.DeleteAccountOnce(ctx, closeReq("lost-a-1"), aID); !isRecoveryNotStored(err) {
		t.Fatal(err)
	}
	drop.arm()
	if err := a.DeleteAccountOnce(ctx, closeReq("lost-a-1"), aID); err == nil || !strings.Contains(err.Error(), "response lost") {
		t.Fatalf("the response must be lost: %v", err)
	}
	if wf := s.workflowOf(ctx, aID); wf.Stage != "cleanup" {
		t.Fatalf("the deletion began although the answer was lost: %+v", wf)
	}
	// Whatever else happens in the browser: the session Cookie goes, another account signs in.
	token := a.RecoveryCookie()
	a.SetSessionCookie("")
	if err := a.Login(ctx, "lost_b", "", ""); err != nil {
		t.Fatal(err)
	}
	if a.RecoveryCookie() != token {
		t.Fatal("signing in changed the recovery Cookie")
	}
	if d, err := a.GetAccountDeletion(ctx); err != nil || d.GetState() != pendingState || d.GetMutationId() != "lost-a-1" {
		t.Fatalf("read: %v %v", d, err)
	}
	// A repeat of the close call for the first account is refused while someone else is signed in
	// (the page names the account it is for); it neither starts a deletion for the other account
	// nor touches the one that began. Retry is the way to continue it.
	if err := a.DeleteAccountOnce(ctx, closeReq("lost-a-1"), aID); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("repeat of the close call while another account is signed in: %v", err)
	}
	if n := s.BFFDB.AccountDeletion.Query().CountX(ctx); n != 1 {
		t.Fatalf("%d deletions exist", n)
	}
	s.failMetro.Store(false)
	d, err := a.RetryAccountDeletion(ctx)
	if err != nil || d.GetState() != completedState {
		t.Fatalf("retry: %v %v", d, err)
	}
	if s.hasObject(aKey) {
		t.Fatal("media survived")
	}
}

// Another account signing in, out and being revoked in the same browser leaves the recovery Cookie
// alone; status and retry answer for the deletion that began, not for whoever is signed in; the
// finished deletion removes the recovery Cookie and does not sign the other account out.
func TestChainRecoverySurvivesAnotherAccountAndCompletionLeavesItSignedIn(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	a, aID, aKey := s.account("iso_a")
	_, bID, _ := s.account("iso_b")
	s.failMetro.Store(true)
	if err := a.DeleteAccount(ctx, closeReq("iso-a-1"), aID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("deletion: %v", err)
	}
	token := a.RecoveryCookie()
	if token == "" {
		t.Fatal("no recovery Cookie")
	}

	// The same jar signs in as b, ends that session, signs in again: the recovery Cookie is untouched.
	if err := a.Login(ctx, "iso_b", "", ""); err != nil {
		t.Fatalf("another account must be able to sign in: %v", err)
	}
	bToken := a.SessionCookie()
	if a.RecoveryCookie() != token || bToken == token {
		t.Fatal("signing in disturbed the recovery Cookie")
	}
	if err := a.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	if a.RecoveryCookie() != token {
		t.Fatal("signing out changed the recovery Cookie")
	}
	if err := a.Login(ctx, "iso_b", "", ""); err != nil {
		t.Fatal(err)
	}
	bToken = a.SessionCookie()
	sessions, err := a.ListSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, ss := range sessions {
		if ss.GetCurrent() {
			if err = a.RevokeSession(ctx, ss.GetId()); err != nil {
				t.Fatal(err)
			}
		}
	}
	if a.RecoveryCookie() != token {
		t.Fatal("revoking the current session changed the recovery Cookie")
	}
	if err = a.Login(ctx, "iso_b", "", ""); err != nil {
		t.Fatal(err)
	}
	bToken = a.SessionCookie()

	// Status and retry are about a's deletion, while b is signed in.
	if me, err := a.Me(ctx); err != nil || me.GetId() != bID {
		t.Fatalf("b is signed in: %v %v", me, err)
	}
	if d, err := a.GetAccountDeletion(ctx); err != nil || d.GetState() != pendingState || d.GetMutationId() != "iso-a-1" {
		t.Fatalf("status: %v %v", d, err)
	}
	if _, err = a.RetryAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("retry while metro is down: %v", err)
	}
	// b cannot begin its own deletion here while a's is unfinished: that would replace the only
	// credential of a's. Nothing is persisted for b and the Cookie stays.
	if err = a.DeleteAccount(ctx, closeReq("iso-b-1"), bID); connect.CodeOf(err) != connect.CodeFailedPrecondition || isRecoveryNotStored(err) {
		t.Fatalf("a second deletion in the browser must be refused: %v", err)
	}
	if persisted(s, bID) != 0 || a.RecoveryCookie() != token {
		t.Fatal("the refused deletion disturbed the recovery credential")
	}
	// A browser that was never a's cannot see or continue it.
	other := s.FreshBrowser()
	if _, err = other.GetAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("another browser: %v", err)
	}
	if _, err = other.RetryAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("another browser retry: %v", err)
	}

	s.failMetro.Store(false)
	d, err := a.RetryAccountDeletion(ctx)
	if err != nil || d.GetState() != completedState {
		t.Fatalf("final retry: %v %v", d, err)
	}
	if s.hasObject(aKey) {
		t.Fatal("media survived")
	}
	if a.RecoveryCookie() != "" {
		t.Fatal("the finished deletion must remove the recovery Cookie")
	}
	if a.SessionCookie() != bToken {
		t.Fatal("the finished deletion cleared the session Cookie of the account signed in since")
	}
	if me, err := a.Me(ctx); err != nil || me.GetId() != bID {
		t.Fatalf("b must stay signed in after a's deletion finished: %v %v", me, err)
	}
	// A replay of the receipt (a lost completion answer) does not touch b either.
	a.SetRecoveryCookie(token)
	if d, err = a.GetAccountDeletion(ctx); err != nil || d.GetState() != completedState {
		t.Fatalf("receipt replay: %v %v", d, err)
	}
	if a.RecoveryCookie() != "" || a.SessionCookie() != bToken {
		t.Fatal("the receipt replay cleared the wrong Cookie")
	}
}

// A deletion that began before the recovery Cookie existed is bound to the session Cookie's token
// only: status, retry and a repeat of the begin call still find it through that Cookie, and the
// first of them stores the recovery Cookie, so the browser moves to the new credential by itself.
func TestChainRecoveryCookieIsAdoptedForADeletionThatBeganWithoutIt(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	a, aID, _ := s.account("old_a")
	s.failMetro.Store(true)
	if err := a.DeleteAccount(ctx, closeReq("old-a-1"), aID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal(err)
	}
	token := a.RecoveryCookie()
	a.DropRecoveryCookie() // what a browser has that began the deletion under the previous release:
	a.SetSessionCookie(token) // only the (revoked) session Cookie, still stored
	if d, err := a.GetAccountDeletion(ctx); err != nil || d.GetState() != pendingState {
		t.Fatalf("status through the session Cookie: %v %v", d, err)
	}
	if a.RecoveryCookie() != token {
		t.Fatalf("the status read must store the recovery Cookie: %q", a.RecoveryCookie())
	}
	if a.SessionCookie() != "" {
		t.Fatal("the status read of a deletion past its begin step must remove the revoked session Cookie")
	}
	a.DropRecoveryCookie()
	a.SetSessionCookie(token)
	if err := a.DeleteAccountOnce(ctx, closeReq("old-a-1"), aID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("repeat of the begin call: %v", err)
	}
	if a.RecoveryCookie() != token {
		t.Fatal("the repeat must store the recovery Cookie")
	}
}

// A recovery Cookie that opens nothing is removed when it is read: after a receipt expired and
// when it never began anything.
func TestChainRecoveryCookieIsRemovedOnceItOpensNothing(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	a, aID, _ := s.account("exp_a")
	if err := a.DeleteAccount(ctx, closeReq("exp-a-1"), aID); err != nil {
		t.Fatal(err)
	}
	token := a.SessionCookie()
	if token != "" {
		t.Fatalf("the finished deletion must clear the session Cookie of the account it deleted: %q", token)
	}
	// The receipt expired: nothing opens, and the Cookie goes.
	a.SetRecoveryCookie("bf_00")
	if _, err := a.GetAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("an unknown recovery Cookie: %v", err)
	}
	if a.RecoveryCookie() != "" {
		t.Fatal("a recovery Cookie that opens nothing must be removed")
	}
	// A prepared but never begun one is removed the same way.
	b, bID, _ := s.account("exp_b")
	if err := b.DeleteAccountOnce(ctx, closeReq("exp-b-1"), bID); !isRecoveryNotStored(err) {
		t.Fatal(err)
	}
	if b.RecoveryCookie() == "" {
		t.Fatal("no recovery Cookie")
	}
	if _, err := b.GetAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("status for a Cookie that began nothing: %v", err)
	}
	if b.RecoveryCookie() != "" {
		t.Fatal("a recovery Cookie that began nothing must be removed when read")
	}
	// And a finished deletion whose receipt has expired.
	c, cID, _ := s.account("exp_c")
	s.failMetro.Store(true)
	if err := c.DeleteAccount(ctx, closeReq("exp-c-1"), cID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal(err)
	}
	cToken := c.RecoveryCookie()
	s.failMetro.Store(false)
	if d, err := c.RetryAccountDeletion(ctx); err != nil || d.GetState() != completedState {
		t.Fatalf("%v %v", d, err)
	}
	s.BFFDB.AccountDeletion.Update().Where(accountdeletion.UserID(cID)).SetReceiptExpiresAt(time.Now().UTC().Add(-time.Minute)).ExecX(ctx)
	c.SetRecoveryCookie(cToken)
	if _, err := c.GetAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("an expired receipt: %v", err)
	}
	if c.RecoveryCookie() != "" {
		t.Fatal("the recovery Cookie of an expired receipt must be removed")
	}
}

// A sign-out that arrives after the deletion began, a stale session and the like never touch the
// recovery Cookie; a stale session cannot begin a deletion.
func TestChainRecoveryCookieIsNotTouchedBySessionCalls(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	a, aID, _ := s.account("paths_a")
	stale := a.SessionCookie()
	s.failMetro.Store(true)
	if err := a.DeleteAccount(ctx, closeReq("paths-a-1"), aID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("deletion: %v", err)
	}
	token := a.RecoveryCookie()
	calls := []struct {
		name string
		call func() error
	}{
		{"logout", func() error { return a.Logout(ctx) }},
		{"revoke session", func() error { return a.RevokeSession(ctx, "any") }},
		{"change password", func() error { return a.ChangePassword(ctx, E2EPassword, "another-password-0123") }},
		{"login of the same (closed) account", func() error { return a.Login(ctx, "paths_a", "", "") }},
		{"me", func() error { _, err := a.Me(ctx); return err }},
	}
	for _, c := range calls {
		_ = c.call() // a refusal is fine; the recovery Cookie is what must stay
		if a.RecoveryCookie() != token {
			t.Fatalf("%s changed the recovery Cookie", c.name)
		}
	}
	if d, err := a.GetAccountDeletion(ctx); err != nil || d.GetState() != pendingState {
		t.Fatalf("read: %v %v", d, err)
	}

	// A session that was revoked (the sign-out came first) cannot begin a deletion at all.
	b, bID, _ := s.account("paths_b")
	staleB := b.SessionCookie()
	if err := b.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	b.SetSessionCookie(staleB)
	if err := b.DeleteAccountOnce(ctx, closeReq("paths-b-1"), bID); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a revoked session must not begin a deletion: %v", err)
	}
	if persisted(s, bID) != 0 {
		t.Fatal("a workflow was persisted for a revoked session")
	}
	_ = stale
}

// Ending the session while the deletion begins, in any of the ways, can no longer orphan it: the
// recovery Cookie was confirmed before, and nothing the session calls do changes it.
func TestChainConcurrentSessionEndNeverOrphansADeletion(t *testing.T) {
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
	const rounds = 8
	for _, k := range kinds {
		began := 0
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
			mutation := fmt.Sprintf("race-%d-%d", len(k.name), i)
			if err = b.DeleteAccountOnce(ctx, closeReq(mutation), id); !isRecoveryNotStored(err) {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_ = b.DeleteAccountOnce(ctx, closeReq(mutation), id)
			}()
			go func() {
				defer wg.Done()
				_ = k.end(b, sessionID)
			}()
			wg.Wait()
			if persisted(s, id) == 0 {
				continue
			}
			began++
			if b.RecoveryCookie() == "" {
				t.Fatalf("%s round %d: a deletion began and its recovery Cookie is gone", k.name, i)
			}
			if d, err := b.GetAccountDeletion(ctx); err != nil || d.GetMutationId() != mutation {
				t.Fatalf("%s round %d: the deletion cannot be read: %v %v", k.name, i, d, err)
			}
		}
		t.Logf("%s: %d of %d rounds began a deletion, each one readable by its recovery Cookie", k.name, began, rounds)
	}
}

// A restarted BFF finishes the deletion from persisted state, and the browser's recovery Cookie
// then shows the receipt.
func TestChainRecoveryCookieShowsTheResultAfterARestart(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	a, aID, aKey := s.account("rst_a")
	s.failMetro.Store(true)
	if err := a.DeleteAccount(ctx, closeReq("rst-a-1"), aID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal(err)
	}
	token := a.RecoveryCookie()
	s.failMetro.Store(false)
	s.dueNow(ctx)
	restarted := s.RestartBFF(nil)
	if n, err := restarted.RunDeletionsOnce(ctx); err != nil || n != 1 {
		t.Fatalf("runner after the restart: %d %v", n, err)
	}
	if s.hasObject(aKey) {
		t.Fatal("media survived")
	}
	if a.RecoveryCookie() != token {
		t.Fatal("the restart changed the browser's recovery Cookie")
	}
	if d, err := a.GetAccountDeletion(ctx); err != nil || d.GetState() != completedState {
		t.Fatalf("receipt after the restart: %v %v", d, err)
	}
	if a.RecoveryCookie() != "" {
		t.Fatal("reading the receipt must remove the recovery Cookie")
	}
}

// dropResponse loses the answer of the next call whose path contains match: the server handles
// the request, the client never hears and the jar never sees its Set-Cookie.
type dropResponse struct {
	match string
	armed atomic.Bool
}

func (d *dropResponse) arm() { d.armed.Store(true) }

func (d *dropResponse) wrap(b *Browser) {
	next := b.HTTP.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	b.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		res, err := next.RoundTrip(r)
		if err == nil && strings.Contains(r.URL.Path, d.match) && d.armed.CompareAndSwap(true, false) {
			_ = res.Body.Close()
			return nil, errors.New("response lost")
		}
		return res, err
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
