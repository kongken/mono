package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/ent/accountdeletion"
	"github.com/kongken/mono/internal/bff/session"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

// The deletion recovery Cookie (kongken_del_<generation>) is the credential to see, refresh and retry
// an account deletion. It is separate from the session Cookie: signing in, signing out and ending
// sessions never touch it, and no deletion RPC ever writes the session Cookie (the server revoked the
// session, that is what ends it). There is one immutable Cookie per recovery generation: its value is
// the random token of the session that began the deletion (the workflow is bound to its digest), its
// name is derived from that value, a response only ever sets, renews or clears the generation it is
// about, and an answer about a deletion that does not exist writes nothing. Nothing authenticates with it.

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
	a.DropRecoveryCookie()    // what a browser has that began the deletion under the previous release:
	a.SetSessionCookie(token) // only the (revoked) session Cookie, still stored
	if d, err := a.GetAccountDeletion(ctx); err != nil || d.GetState() != pendingState {
		t.Fatalf("status through the session Cookie: %v %v", d, err)
	}
	if a.RecoveryCookie() != token {
		t.Fatalf("the status read must store the recovery Cookie: %q", a.RecoveryCookie())
	}
	if a.SessionCookie() != token {
		t.Fatal("the status read must not rewrite the session Cookie")
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

// A recovery Cookie that opens nothing is inert and is never written by the answer: it may belong to
// a deletion that has not begun yet (its confirmation is in the browser, its begin still on the way),
// and a late answer must not take that credential away. It leaves with its own Max-Age.
func TestChainRecoveryCookieThatOpensNothingIsInertAndNeverCleared(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	tap := &cookieTap{match: "AccountDeletion"}
	a, aID, _ := s.account("exp_a")
	tap.wrap(a)
	if err := a.DeleteAccount(ctx, closeReq("exp-a-1"), aID); err != nil {
		t.Fatal(err)
	}
	token := a.SessionCookie()
	if token == "" || a.RecoveryCookie() != "" {
		t.Fatalf("a finished deletion leaves the session Cookie alone and clears its recovery Cookie: %q %q", token, a.RecoveryCookie())
	}
	// An unknown credential opens nothing and is not cleared.
	a.SetSessionCookie("") // its own receipt would open through the session Cookie
	a.SetRecoveryCookie("bf_00")
	tap.reset()
	if _, err := a.GetAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("an unknown recovery Cookie: %v", err)
	}
	if a.RecoveryCookie() != "bf_00" || len(tap.seen()) != 0 {
		t.Fatalf("an answer about nothing must write nothing: %q %v", a.RecoveryCookie(), tap.seen())
	}
	// A prepared but never begun one is the same: the answer writes nothing.
	b, bID, _ := s.account("exp_b")
	tap.wrap(b)
	if err := b.DeleteAccountOnce(ctx, closeReq("exp-b-1"), bID); !isRecoveryNotStored(err) {
		t.Fatal(err)
	}
	prepared := b.RecoveryCookie()
	if prepared == "" {
		t.Fatal("no recovery Cookie")
	}
	tap.reset()
	if _, err := b.GetAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("status for a Cookie that began nothing: %v", err)
	}
	if b.RecoveryCookie() != prepared || len(tap.seen()) != 0 {
		t.Fatalf("a status read before the begin must not take the confirmed credential away: %q %v", b.RecoveryCookie(), tap.seen())
	}
	// A finished deletion whose receipt has expired: nothing opens, nothing is written; the Cookie
	// lingers (inert) until its own expiry, because the answer cannot tell it from a prepared one.
	c, cID, _ := s.account("exp_c")
	tap.wrap(c)
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
	tap.reset()
	if _, err := c.GetAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("an expired receipt: %v", err)
	}
	if c.RecoveryCookie() != cToken || len(tap.seen()) != 0 {
		t.Fatalf("an expired receipt answer must write nothing: %q %v", c.RecoveryCookie(), tap.seen())
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

// cookieTap watches the Set-Cookie headers of the responses whose path contains match, and can hold
// them back (hold) so the browser applies them later, as a response delayed in the network would be.
type cookieTap struct {
	match string

	mu    sync.Mutex
	names []string
	hold  bool
	held  []string
}

func (c *cookieTap) wrap(b *Browser) {
	next := b.HTTP.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	b.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		res, err := next.RoundTrip(r)
		if err != nil || !strings.Contains(r.URL.Path, c.match) {
			return res, err
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, sc := range res.Header.Values("Set-Cookie") {
			c.names = append(c.names, strings.SplitN(sc, "=", 2)[0])
		}
		if c.hold {
			c.hold = false
			c.held = append(c.held, res.Header.Values("Set-Cookie")...)
			res.Header.Del("Set-Cookie")
		}
		return res, err
	})
}

// holdNext keeps the Set-Cookie headers of the next matching response from the browser.
func (c *cookieTap) holdNext() {
	c.mu.Lock()
	c.hold = true
	c.mu.Unlock()
}

// release hands the held headers to the browser now, late.
func (c *cookieTap) release(b *Browser) {
	c.mu.Lock()
	held := c.held
	c.held = nil
	c.mu.Unlock()
	h := http.Header{}
	for _, sc := range held {
		h.Add("Set-Cookie", sc)
	}
	u, _ := url.Parse(b.Origin)
	b.HTTP.Jar.SetCookies(u, (&http.Response{Header: h}).Cookies())
}

func (c *cookieTap) heldCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.held)
}

// seen are the names of the cookies the matching responses set or cleared since the last reset.
func (c *cookieTap) seen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.names...)
}

func (c *cookieTap) reset() {
	c.mu.Lock()
	c.names = nil
	c.mu.Unlock()
}

func (c *cookieTap) wroteSession() bool {
	for _, n := range c.seen() {
		if n == session.CookieName {
			return true
		}
	}
	return false
}

// S1: the answer to a status read made before the deletion began arrives after it began. Such an
// answer writes nothing, so the confirmed credential cannot be taken away; and a late answer that is
// held back and applied after the begin changes nothing either.
func TestChainLateStatusAnswerCannotTakeTheRecoveryCredentialAway(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	a, aID, aKey := s.account("s1_a")
	tap := &cookieTap{match: "GetAccountDeletion"}
	tap.wrap(a)
	s.failMetro.Store(true)

	if err := a.DeleteAccountOnce(ctx, closeReq("s1-a-1"), aID); !isRecoveryNotStored(err) {
		t.Fatal(err)
	}
	token := a.RecoveryCookie()
	tap.holdNext() // the status read goes out now, its answer arrives after the begin
	if _, err := a.GetAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("status before the begin: %v", err)
	}
	if err := a.DeleteAccountOnce(ctx, closeReq("s1-a-1"), aID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("begin: %v", err)
	}
	tap.release(a)
	if tap.heldCount() != 0 || a.RecoveryCookie() != token {
		t.Fatalf("the late answer took the recovery credential away: %q", a.RecoveryCookie())
	}
	if d, err := a.GetAccountDeletion(ctx); err != nil || d.GetState() != pendingState || d.GetMutationId() != "s1-a-1" {
		t.Fatalf("the deletion cannot be seen: %v %v", d, err)
	}
	s.failMetro.Store(false)
	if d, err := a.RetryAccountDeletion(ctx); err != nil || d.GetState() != completedState {
		t.Fatalf("retry: %v %v", d, err)
	}
	if s.hasObject(aKey) {
		t.Fatal("media survived")
	}
}

// S2/R2: no deletion RPC rewrites the session Cookie, whatever its answer is, so an answer delayed
// past a later sign-in cannot sign the browser out of the account signed in since.
func TestChainDeletionRPCsNeverWriteTheSessionCookie(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	_, r2bID, _ := s.account("r2_b")
	a, aID, _ := s.account("r2_a")
	tap := &cookieTap{match: "AccountDeletion"}
	tap.wrap(a)
	del := &cookieTap{match: "DeleteAccount"}
	del.wrap(a)
	s.failMetro.Store(true)

	token := a.SessionCookie()
	if err := a.DeleteAccount(ctx, closeReq("r2-a-1"), aID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("begin: %v", err)
	}
	// The failed answer, the repeat, the read, the retry (failing and finishing), the replay.
	step := func(name string, err error, want connect.Code) {
		t.Helper()
		if want == 0 && err != nil || want != 0 && connect.CodeOf(err) != want {
			t.Fatalf("%s: %v", name, err)
		}
	}
	a.SetSessionCookie(token) // a stale session Cookie is what a delayed answer would have overwritten
	_, err := a.GetAccountDeletion(ctx)
	step("read", err, 0)
	// The scenario: a read is on its way with the stale session Cookie, another account signs in
	// meanwhile, then the read's answer arrives.
	tap.holdNext()
	_, err = a.GetAccountDeletion(ctx)
	step("held read", err, 0)
	if err := a.Login(ctx, "r2_b", "", ""); err != nil {
		t.Fatal(err)
	}
	tap.release(a)
	if me, err := a.Me(ctx); err != nil || me.GetId() != r2bID {
		t.Fatalf("the late answer signed the other account out: %v %v", me, err)
	}
	_, err = a.RetryAccountDeletion(ctx)
	step("retry", err, connect.CodeUnavailable)
	err = a.DeleteAccountOnce(ctx, closeReq("r2-a-1"), aID)
	step("repeat while another account is signed in", err, connect.CodeUnauthenticated)
	err = a.DeleteAccountOnce(ctx, closeReq("r2-other"), r2bID)
	step("another mutation", err, connect.CodeFailedPrecondition)
	s.failMetro.Store(false)
	d, err := a.RetryAccountDeletion(ctx)
	if err != nil || d.GetState() != completedState {
		t.Fatalf("finish: %v %v", d, err)
	}
	a.SetRecoveryCookie(token) // a lost completion answer: the receipt is read once more
	if d, err = a.GetAccountDeletion(ctx); err != nil || d.GetState() != completedState {
		t.Fatalf("receipt: %v %v", d, err)
	}
	if tap.wroteSession() || del.wroteSession() {
		t.Fatalf("a deletion RPC wrote the session Cookie: %v %v", tap.seen(), del.seen())
	}
	if me, err := a.Me(ctx); err != nil || me.GetId() != r2bID {
		t.Fatalf("the other account is no longer signed in: %v %v", me, err)
	}
}

// R3/R4: answers about an older generation (a completion, a replay, a renewal) that arrive after a
// newer deletion began cannot set, renew or clear the newer generation's Cookie.
func TestChainLateAnswersAboutAnOlderGenerationLeaveTheNewOneAlone(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	_, bID, _ := s.account("r3_b")
	a, aID, _ := s.account("r3_a")
	tap := &cookieTap{match: "AccountDeletion"}
	tap.wrap(a)
	s.failMetro.Store(true)
	if err := a.DeleteAccount(ctx, closeReq("r3-a-1"), aID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("W1: %v", err)
	}
	g1 := a.RecoveryCookie()

	// A renewal of G1 (a status read answer) is held back; then W1 finishes and its completion answer
	// is held back as well.
	tap.holdNext()
	if _, err := a.GetAccountDeletion(ctx); err != nil {
		t.Fatal(err)
	}
	renewal := append([]string(nil), tap.held...)
	tap.mu.Lock()
	tap.held = nil
	tap.mu.Unlock()
	s.failMetro.Store(false)
	tap.holdNext()
	if d, err := a.RetryAccountDeletion(ctx); err != nil || d.GetState() != completedState {
		t.Fatalf("W1 retry: %v %v", d, err)
	}
	completion := append([]string(nil), tap.held...)
	tap.mu.Lock()
	tap.held = nil
	tap.mu.Unlock()
	if len(renewal) != 1 || len(completion) != 1 || a.RecoveryCookie() != g1 {
		t.Fatalf("setup: %v %v %q", renewal, completion, a.RecoveryCookie())
	}

	// Another account signs in and begins its own deletion (W2, G2) while those answers are late.
	if err := a.Login(ctx, "r3_b", "", ""); err != nil {
		t.Fatal(err)
	}
	s.failMetro.Store(true)
	if err := a.DeleteAccount(ctx, closeReq("r3-b-1"), bID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("W2: %v", err)
	}
	tokens := a.RecoveryCookies()
	if len(tokens) != 2 {
		t.Fatalf("expected the generations of W1 and W2: %v", tokens)
	}
	g2 := tokens[0]
	if g2 == g1 {
		g2 = tokens[1]
	}

	// The late answers arrive, in the worst order: the renewal of G1 and the completion of W1.
	for _, late := range [][]string{renewal, completion} {
		h := http.Header{}
		for _, sc := range late {
			h.Add("Set-Cookie", sc)
		}
		u, _ := url.Parse(a.Origin)
		a.HTTP.Jar.SetCookies(u, (&http.Response{Header: h}).Cookies())
		if !slices.Contains(a.RecoveryCookies(), g2) {
			t.Fatal("a late answer about W1 removed the recovery Cookie of W2")
		}
	}
	// W2 is what the browser sees, retries and finishes; W1's receipt (or its inert Cookie) does not
	// get in the way.
	if d, err := a.GetAccountDeletion(ctx); err != nil || d.GetState() != pendingState || d.GetMutationId() != "r3-b-1" {
		t.Fatalf("W2 cannot be seen: %v %v", d, err)
	}
	s.failMetro.Store(false)
	if d, err := a.RetryAccountDeletion(ctx); err != nil || d.GetState() != completedState || d.GetMutationId() != "r3-b-1" {
		t.Fatalf("W2 retry: %v %v", d, err)
	}
	if slices.Contains(a.RecoveryCookies(), g2) {
		t.Fatal("the finished W2 left its Cookie")
	}
}

// A deletion that began under the earlier build kept one mutable fixed-name Cookie. It is still
// honoured, in every call, and the answer moves the browser to the generation Cookie and removes
// the old one; a fixed-name Cookie whose value opens nothing is inert.
func TestChainLegacyFixedNameRecoveryCookieIsAdoptedAndRetired(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	a, aID, aKey := s.account("leg_a")
	s.failMetro.Store(true)
	if err := a.DeleteAccount(ctx, closeReq("leg-a-1"), aID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal(err)
	}
	token := a.RecoveryCookie()
	a.DropRecoveryCookie()
	a.SetLegacyRecoveryCookie(token)
	if d, err := a.GetAccountDeletion(ctx); err != nil || d.GetState() != pendingState {
		t.Fatalf("read through the fixed-name Cookie: %v %v", d, err)
	}
	if a.RecoveryCookie() != token || a.LegacyRecoveryCookie() != "" {
		t.Fatalf("the answer must move the browser to the generation Cookie: %q %q", a.RecoveryCookie(), a.LegacyRecoveryCookie())
	}

	// A fixed-name Cookie that was set while the begin was still to come (the old confirmation) lets it begin.
	b, bID, bKey := s.account("leg_b")
	b.SetLegacyRecoveryCookie(b.SessionCookie())
	if err := b.DeleteAccountOnce(ctx, closeReq("leg-b-1"), bID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("begin with the fixed-name confirmation: %v", err)
	}
	if b.RecoveryCookie() != a.SessionCookie() && b.RecoveryCookie() == "" || b.LegacyRecoveryCookie() != "" {
		t.Fatalf("after the begin: %q %q", b.RecoveryCookie(), b.LegacyRecoveryCookie())
	}

	// A fixed-name Cookie with a value that opens nothing is inert, and is left alone.
	c := s.FreshBrowser()
	c.SetLegacyRecoveryCookie("bf_nothing")
	if _, err := c.GetAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated || c.LegacyRecoveryCookie() != "bf_nothing" {
		t.Fatalf("an inert fixed-name Cookie: %v %q", err, c.LegacyRecoveryCookie())
	}
	s.failMetro.Store(false)
	for _, x := range []struct {
		b   *Browser
		key string
	}{{a, aKey}, {b, bKey}} {
		if d, err := x.b.RetryAccountDeletion(ctx); err != nil || d.GetState() != completedState {
			t.Fatalf("retry: %v %v", d, err)
		}
		if s.hasObject(x.key) {
			t.Fatal("media survived")
		}
	}
}

// Several credentials in one browser are never mixed up: the call that names a mutation reaches that
// deletion, an unnamed one the oldest unfinished, and a Cookie whose name belongs to another value, or
// that is not a generation Cookie at all, opens nothing.
func TestChainSeveralRecoveryCookiesInOneBrowser(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	a1, a1ID, _ := s.account("multi_a")
	a2, a2ID, _ := s.account("multi_b")
	s.failMetro.Store(true)
	if err := a1.DeleteAccount(ctx, closeReq("multi-a"), a1ID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := a2.DeleteAccount(ctx, closeReq("multi-b"), a2ID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal(err)
	}
	tokenA, tokenB := a1.RecoveryCookie(), a2.RecoveryCookie()
	a1.SetRecoveryCookie(tokenB) // the browser now holds both generations

	if d, err := a1.GetAccountDeletion(ctx); err != nil || d.GetMutationId() != "multi-a" {
		t.Fatalf("the oldest unfinished deletion is the unnamed answer: %v %v", d, err)
	}
	if err := a1.DeleteAccountOnce(ctx, closeReq("multi-b"), a2ID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("a call naming the second mutation must reach it: %v", err)
	}
	if got := a1.RecoveryCookies(); len(got) != 2 || !slices.Contains(got, tokenA) || !slices.Contains(got, tokenB) {
		t.Fatalf("both Cookies must stay while both are unfinished: %v", got)
	}
	// A name that does not belong to its value opens nothing, and neither does a plain name.
	forged := s.FreshBrowser()
	u, _ := url.Parse(forged.Origin)
	forged.HTTP.Jar.SetCookies(u, []*http.Cookie{
		{Name: session.RecoveryCookieName(tokenA), Value: tokenB, Path: "/"},
		{Name: session.RecoveryCookiePrefix + "0000000000000000", Value: tokenA, Path: "/"},
		{Name: "kongken_del_x", Value: tokenA, Path: "/"},
	})
	if _, err := forged.GetAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a Cookie standing for another generation: %v", err)
	}
	s.failMetro.Store(false)
	for _, want := range []string{"multi-a", "multi-b"} {
		d, err := a1.RetryAccountDeletion(ctx)
		if err != nil || d.GetState() != completedState || d.GetMutationId() != want {
			t.Fatalf("retry for %s: %v %v", want, d, err)
		}
	}
	if len(a1.RecoveryCookies()) != 0 {
		t.Fatalf("finished deletions left Cookies: %v", a1.RecoveryCookies())
	}
}
