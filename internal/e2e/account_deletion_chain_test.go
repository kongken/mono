package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/ent/accountdeletion"
	"github.com/kongken/mono/internal/bff/ent/schema"
	bffsession "github.com/kongken/mono/internal/bff/ent/session"
	"github.com/kongken/mono/internal/bff/session"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	methodBeginDeletion  = "/usersrv.v1.AccountService/BeginDeletion"
	methodFinishDeletion = "/usersrv.v1.AccountService/FinishDeletion"
)

// dueNow makes every waiting deletion workflow due, as if its backoff had passed.
func (h *ChainHarness) dueNow(ctx context.Context) {
	h.T.Helper()
	if _, err := h.BFFDB.AccountDeletion.Update().SetNextAttemptAt(time.Now().UTC().Add(-time.Second)).Save(ctx); err != nil {
		h.T.Fatal(err)
	}
}

func (h *ChainHarness) workflowOf(ctx context.Context, userID string) *wfSnapshot {
	h.T.Helper()
	wf, err := h.BFFDB.AccountDeletion.Query().Where(accountdeletion.UserID(userID)).Only(ctx)
	if err != nil {
		h.T.Fatal(err)
	}
	return &wfSnapshot{Stage: wf.Stage, Attempts: wf.Attempts, Completed: wf.CompletedProducts, Products: wf.Products, LastError: wf.LastError}
}

type wfSnapshot struct {
	Stage     string
	Attempts  int
	Completed []string
	Products  []string
	LastError string
}

// Spec §14 scenarios 19-21: a failing product keeps the account closed and the
// workflow retryable; once it finishes only the original Cookie replays it, and the
// same address registers a fresh, empty account.
func TestChainAccountDeletionRetryReceiptAndIsolation(t *testing.T) {
	blobs := &faultInjectingBlobs{fail: true}
	h := NewChainHarness(t, blobs)
	ctx := context.Background()

	a, b := h.FreshBrowser(), h.FreshBrowser()
	aID, err := a.Register(ctx, "delete_a", "", "Delete A")
	if err != nil {
		t.Fatal(err)
	}
	bID, err := b.Register(ctx, "delete_b", "", "Delete B")
	if err != nil {
		t.Fatal(err)
	}
	h.TripmapDB.CityVisit.Create().SetUserID(aID).SetRegionCode("330100").SetRegionName("杭州市").SetProvinceCode("330000").SetColor("#4b937d").SaveX(ctx)
	h.TripmapDB.MediaObject.Create().SetUserID(aID).SetObjectKey("owned/photo.jpg").SetContentType("image/jpeg").SetSizeBytes(10).ExecX(ctx)
	h.TripmapDB.CityVisit.Create().SetUserID(bID).SetRegionCode("330200").SetRegionName("宁波市").SetProvinceCode("330000").SetColor("#4b937d").SaveX(ctx)
	if _, err = a.GetFootprints(ctx); err != nil {
		t.Fatal(err)
	}
	aToken := a.SessionCookie()

	req := &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "delete-a-1"}
	if err = a.DeleteAccount(ctx, req, aID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("expected unavailable on S3 failure: %v", err)
	}
	// The account is closed: no session, no sign-in, no mail, no tripmap access.
	if _, err = a.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("me while deleting: %v", err)
	}
	if _, err = a.GetFootprints(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("footprints while deleting: %v", err)
	}
	if err = h.FreshBrowser().Login(ctx, "delete_a", "", ""); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("sign-in while deleting: %v", err)
	}
	mails := h.Inbox.Total()
	if _, err = h.FreshBrowser().RequestPasswordReset(ctx, "delete_a@example.com", ""); err != nil {
		t.Fatal(err)
	}
	h.DeliverMail()
	if h.Inbox.Total() != mails {
		t.Fatal("a deleting account received a reset mail")
	}
	// Progress is readable by the Cookie that started it, and nothing else.
	d, err := a.GetAccountDeletion(ctx)
	if err != nil || d.GetState() != bffv1.AccountDeletionState_ACCOUNT_DELETION_STATE_PENDING || d.GetMutationId() != "delete-a-1" ||
		len(d.GetProducts()) != 1 || len(d.GetCompletedProducts()) != 0 {
		t.Fatalf("pending deletion: %v %v", d, err)
	}
	if wf := h.workflowOf(ctx, aID); wf.Stage != schema.DeletionStageCleanup || wf.Attempts != 1 || wf.LastError == "" {
		t.Fatalf("workflow: %+v", wf)
	}
	if _, err = h.FreshBrowser().GetAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a stranger read the deletion: %v", err)
	}
	if _, err = b.GetAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("another user read the deletion: %v", err)
	}
	// A different request cannot replace the one in progress.
	if err = a.DeleteAccount(ctx, &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "someone-else"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("a second mutation id during a deletion: %v", err)
	}

	blobs.fail = false
	d, err = a.RetryAccountDeletion(ctx)
	if err != nil || d.GetState() != bffv1.AccountDeletionState_ACCOUNT_DELETION_STATE_COMPLETED || d.GetReceiptExpiresAt() == nil {
		t.Fatalf("retry: %v %v", d, err)
	}
	if len(blobs.keys) < 1 {
		t.Fatalf("expected S3 deletes, got %v", blobs.keys)
	}
	if a.SessionCookie() != aToken || a.RecoveryCookie() != "" {
		t.Fatal("the deletion RPCs must leave the session Cookie alone and remove the finished generation's recovery Cookie")
	}
	// A lost success response: the same credential replays the same result, once more.
	a.SetRecoveryCookie(aToken)
	if err = a.DeleteAccount(ctx, req, aID); err != nil {
		t.Fatalf("receipt replay: %v", err)
	}
	if a.SessionCookie() != aToken {
		t.Fatal("the replay rewrote the session Cookie")
	}
	if d, err = a.GetAccountDeletion(ctx); err != nil || d.GetState() != bffv1.AccountDeletionState_ACCOUNT_DELETION_STATE_COMPLETED {
		t.Fatalf("receipt read: %v %v", d, err)
	}
	if _, err = a.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("me after deletion: %v", err)
	}
	// The workflow ID is not a credential: the receipt opens only for its Cookie.
	wfID := h.BFFDB.AccountDeletion.Query().OnlyX(ctx).ID
	stranger := h.FreshBrowser()
	stranger.SetSessionCookie(wfID)
	if _, err = stranger.GetAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("the workflow ID opened a deletion: %v", err)
	}
	if _, err = stranger.RetryAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("the workflow ID retried a deletion: %v", err)
	}
	if err = stranger.DeleteAccount(ctx, req, aID); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a stranger replayed the receipt: %v", err)
	}

	// Everything of A is gone, nothing of B moved.
	if h.TripmapDB.CityVisit.Query().CountX(ctx) != 1 {
		t.Fatal("other user's visit removed")
	}
	if h.BFFDB.Session.Query().Where(bffsession.UserID(aID)).CountX(ctx) != 0 {
		t.Fatal("bff sessions for A should be gone")
	}
	if _, err = b.Me(ctx); err != nil {
		t.Fatalf("another user's session was affected: %v", err)
	}
	if _, err = b.GetFootprints(ctx); err != nil {
		t.Fatal(err)
	}

	// The same address registers again as a new account with no history and no roles.
	fresh := h.FreshBrowser()
	newID, err := fresh.Register(ctx, "delete_a", "", "Delete A again")
	if err != nil || newID == aID {
		t.Fatalf("re-registration: %q %v", newID, err)
	}
	fp, err := fresh.GetFootprints(ctx)
	if err != nil || len(fp.Visits) != 0 || len(fp.ProvinceMarks) != 0 {
		t.Fatalf("the new account is not empty: %v %v", fp, err)
	}
	me, err := fresh.MeFull(ctx)
	if err != nil || len(me.GetRoles()) != 0 {
		t.Fatalf("the new account has history: %v %v", me, err)
	}
}

// Each stage can lose its response or fail and be resumed by the same mutation ID,
// by the background runner, or both.
func TestChainAccountDeletionResumesAfterEveryStageFailure(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	del := func(id string) *bffv1.DeleteAccountRequest {
		return &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: id}
	}
	tokens := map[*Browser]string{}
	register := func(label string) (*Browser, string) {
		b := h.FreshBrowser()
		id, err := b.Register(ctx, label, "", label)
		if err != nil {
			t.Fatal(err)
		}
		tokens[b] = b.SessionCookie()
		return b, id
	}
	completed := func(b *Browser) {
		t.Helper()
		// The Cookie was cleared when the deletion finished; the receipt answers a
		// browser that still holds the old one.
		b.SetSessionCookie(tokens[b])
		d, err := b.GetAccountDeletion(ctx)
		if err != nil || d.GetState() != bffv1.AccountDeletionState_ACCOUNT_DELETION_STATE_COMPLETED {
			t.Fatalf("deletion not completed: %v %v", d, err)
		}
	}

	// The response of begin is lost: usersrv already closed the account, the workflow
	// is still at begin, and the same mutation ID resumes it. Sign-in is closed.
	b, id := register("stage-begin")
	h.LoseUsersrvResponse(methodBeginDeletion)
	if err := b.DeleteAccount(ctx, del("m-begin")); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("lost begin response: %v", err)
	}
	if wf := h.workflowOf(ctx, id); wf.Stage != schema.DeletionStageBegin {
		t.Fatalf("workflow after a lost begin: %+v", wf)
	}
	if err := h.FreshBrowser().Login(ctx, "stage-begin", "", ""); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("sign-in after a lost begin: %v", err)
	}
	// The background runner does not start a deletion nobody confirmed again...
	h.dueNow(ctx)
	if n, err := h.BFF.RunDeletionsOnce(ctx); err != nil || n != 0 {
		t.Fatalf("the runner touched a workflow at begin: %d %v", n, err)
	}
	// ...the owner's retry does.
	if err := b.DeleteAccount(ctx, del("m-begin")); err != nil {
		t.Fatal(err)
	}
	completed(b)

	// Begin refused for good by an outage: nothing is committed anywhere, the session
	// still works, and a retry starts clean.
	b, id = register("stage-begin-down")
	h.FailUsersrv(func(m string) error {
		if m == methodBeginDeletion {
			return status.Error(codes.Unavailable, "down")
		}
		return nil
	})
	if err := b.DeleteAccount(ctx, del("m-down")); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("begin during an outage: %v", err)
	}
	h.FailUsersrv(nil)
	if _, err := b.Me(ctx); err != nil {
		t.Fatalf("the account was closed by a refused begin: %v", err)
	}
	if err := b.DeleteAccount(ctx, del("m-down")); err != nil {
		t.Fatal(err)
	}
	completed(b)

	// Finish fails (usersrv outage after the products are cleaned): the workflow waits
	// at finish, with the products recorded, and the background runner completes it
	// without the browser.
	b, id = register("stage-finish")
	h.FailUsersrv(func(m string) error {
		if m == methodFinishDeletion {
			return status.Error(codes.Unavailable, "down")
		}
		return nil
	})
	if err := b.DeleteAccount(ctx, del("m-finish")); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("finish during an outage: %v", err)
	}
	if wf := h.workflowOf(ctx, id); wf.Stage != schema.DeletionStageFinish || len(wf.Completed) != len(wf.Products) || wf.Attempts != 1 {
		t.Fatalf("workflow waiting at finish: %+v", wf)
	}
	if n, err := h.BFF.RunDeletionsOnce(ctx); err != nil || n != 0 {
		t.Fatalf("the runner ran before the backoff: %d %v", n, err)
	}
	h.FailUsersrv(nil)
	h.dueNow(ctx)
	if n, err := h.BFF.RunDeletionsOnce(ctx); err != nil || n != 1 {
		t.Fatalf("the runner did not complete the deletion: %d %v", n, err)
	}
	completed(b)
	if wf := h.workflowOf(ctx, id); wf.Stage != schema.DeletionStageDone {
		t.Fatalf("workflow after the runner: %+v", wf)
	}

	// The response of finish is lost: usersrv already removed the account.
	b, id = register("stage-finish-lost")
	h.LoseUsersrvResponse(methodFinishDeletion)
	if err := b.DeleteAccount(ctx, del("m-lost")); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("lost finish response: %v", err)
	}
	if d, err := b.RetryAccountDeletion(ctx); err != nil || d.GetState() != bffv1.AccountDeletionState_ACCOUNT_DELETION_STATE_COMPLETED {
		t.Fatalf("retry after a lost finish: %v %v", d, err)
	}

	// A crash after the local stage began: the stage is repeated with nothing to do.
	b, id = register("stage-local")
	if err := b.DeleteAccount(ctx, del("m-local")); err != nil {
		t.Fatal(err)
	}
	h.BFFDB.AccountDeletion.Update().Where(accountdeletion.UserID(id)).
		SetStage(schema.DeletionStageLocal).ClearCompletedAt().ClearReceiptExpiresAt().ExecX(ctx)
	h.dueNow(ctx)
	if n, err := h.BFF.RunDeletionsOnce(ctx); err != nil || n != 1 {
		t.Fatalf("local stage repeat: %d %v", n, err)
	}
	completed(b)
}

// An expired receipt answers nothing, and is purged.
func TestChainDeletionReceiptExpires(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	b := h.FreshBrowser()
	id, err := b.Register(ctx, "receipt-exp", "", "R")
	if err != nil {
		t.Fatal(err)
	}
	token := b.SessionCookie()
	if err = b.DeleteAccount(ctx, &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "m-exp"}); err != nil {
		t.Fatal(err)
	}
	h.BFFDB.AccountDeletion.Update().Where(accountdeletion.UserID(id)).SetReceiptExpiresAt(time.Now().UTC().Add(-time.Minute)).ExecX(ctx)
	b.SetSessionCookie(token)
	if _, err = b.GetAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("an expired receipt answered: %v", err)
	}
	if _, err = h.BFF.RunDeletionsOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := h.BFFDB.AccountDeletion.Query().CountX(ctx); n != 0 {
		t.Fatalf("%d expired receipts left", n)
	}
}

// DeleteAccount calls DeleteAccount as the shared client library does: when the BFF answers that
// it has just handed out the recovery Cookie, the same call is repeated once, with the Cookie.
func (b *Browser) DeleteAccount(ctx context.Context, msg *bffv1.DeleteAccountRequest, expected ...string) error {
	err := b.DeleteAccountOnce(ctx, msg, expected...)
	if isRecoveryNotStored(err) {
		err = b.DeleteAccountOnce(ctx, msg, expected...)
	}
	return err
}

// DeleteAccountOnce is a single DeleteAccount call, with no repeat.
func (b *Browser) DeleteAccountOnce(ctx context.Context, msg *bffv1.DeleteAccountRequest, expected ...string) error {
	csrf := b.refreshCSRF(ctx)
	req := connect.NewRequest(msg)
	b.writeHeaders(req, csrf, b.Origin)
	if len(expected) > 0 {
		req.Header().Set("X-Kongken-User", expected[0])
	}
	_, err := b.Auth.DeleteAccount(ctx, req)
	return err
}

func isRecoveryNotStored(err error) bool {
	var ce *connect.Error
	return errors.As(err, &ce) && ce.Code() == connect.CodeFailedPrecondition && strings.Contains(ce.Message(), "recovery cookie")
}

// recoveryCookies are the deletion recovery Cookies (one per generation) in the jar, by name.
func (b *Browser) recoveryCookies() map[string]string {
	out := map[string]string{}
	u, err := url.Parse(b.Origin)
	if err != nil {
		return out
	}
	for _, c := range b.HTTP.Jar.Cookies(u) {
		if strings.HasPrefix(c.Name, session.RecoveryCookiePrefix) || c.Name == session.LegacyRecoveryCookieName {
			out[c.Name] = c.Value
		}
	}
	return out
}

// RecoveryCookie is the one deletion recovery credential in the jar, or "". More than one fails the
// test: use RecoveryCookies where several generations are expected.
func (b *Browser) RecoveryCookie() string {
	tokens := b.RecoveryCookies()
	if len(tokens) > 1 {
		panic(fmt.Sprintf("%d recovery Cookies in the jar", len(tokens)))
	}
	if len(tokens) == 0 {
		return ""
	}
	return tokens[0]
}

// RecoveryCookies are the values of the generation recovery Cookies in the jar, sorted by name.
func (b *Browser) RecoveryCookies() []string {
	all := b.recoveryCookies()
	names := make([]string, 0, len(all))
	for n := range all {
		if strings.HasPrefix(n, session.RecoveryCookiePrefix) {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	tokens := make([]string, 0, len(names))
	for _, n := range names {
		tokens = append(tokens, all[n])
	}
	return tokens
}

// SetRecoveryCookie puts the recovery credential of token's generation into the jar, under its name.
func (b *Browser) SetRecoveryCookie(token string) {
	u, _ := url.Parse(b.Origin)
	b.HTTP.Jar.SetCookies(u, []*http.Cookie{{Name: session.RecoveryCookieName(token), Value: token, Path: "/"}})
}

// SetLegacyRecoveryCookie puts the fixed-name Cookie of the earlier build into the jar.
func (b *Browser) SetLegacyRecoveryCookie(token string) {
	u, _ := url.Parse(b.Origin)
	b.HTTP.Jar.SetCookies(u, []*http.Cookie{{Name: session.LegacyRecoveryCookieName, Value: token, Path: "/"}})
}

// LegacyRecoveryCookie is the fixed-name Cookie of the earlier build in the jar, or "".
func (b *Browser) LegacyRecoveryCookie() string {
	return b.recoveryCookies()[session.LegacyRecoveryCookieName]
}

// DropRecoveryCookie removes every recovery Cookie from the jar, as a browser that refuses it
// (or lost the response that carried it) would never have stored it.
func (b *Browser) DropRecoveryCookie() {
	u, _ := url.Parse(b.Origin)
	for n := range b.recoveryCookies() {
		b.HTTP.Jar.SetCookies(u, []*http.Cookie{{Name: n, Value: "", Path: "/", MaxAge: -1}})
	}
}
