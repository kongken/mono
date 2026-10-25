package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	bffconfig "github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/ent/accountdeletion"
	"github.com/kongken/mono/internal/bff/ent/schema"
	"github.com/kongken/mono/internal/metro/ent/riderline"
	"github.com/kongken/mono/internal/tripmap/blobstore"
	"github.com/kongken/mono/internal/tripmap/ent/cityvisit"
	"github.com/kongken/mono/internal/tripmap/ent/mediaobject"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// flakyBlobs wraps an object store so that deleting can be made to fail, as an S3 outage would.
type flakyBlobs struct {
	blobstore.Store
	failDelete atomic.Bool
}

func (f *flakyBlobs) Delete(ctx context.Context, key string) error {
	if f.failDelete.Load() {
		return errors.New("object store unavailable")
	}
	return f.Store.Delete(ctx, key)
}

// closeAccountStack is the product chain the OzID "close account" page works on: tripmap
// with media in an object store and metro, the metro cleanup failing while `failMetro` is set
// and the object store's deletes failing while `store.failDelete` is set.
type closeAccountStack struct {
	*ChainHarness
	store     *flakyBlobs
	failMetro *atomic.Bool
}

// newCloseAccountStack runs on an in-memory object store.
func newCloseAccountStack(t *testing.T, extras ...any) *closeAccountStack {
	t.Helper()
	return newCloseAccountStackOn(t, newChainMediaBlobs(), extras...)
}

func newCloseAccountStackOn(t *testing.T, store blobstore.Store, extras ...any) *closeAccountStack {
	t.Helper()
	failMetro := &atomic.Bool{}
	inject := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		if info.FullMethod == "/metro.v1.AccountCleanupService/CleanupUserData" && failMetro.Load() {
			return nil, status.Error(codes.Unavailable, "injected")
		}
		return next(ctx, req)
	}
	flaky := &flakyBlobs{Store: store}
	h := NewChainHarness(t, append([]any{ChainMetro{}, ChainMetroUnary{inject}, blobstore.Store(flaky)}, extras...)...)
	ctx := context.Background()
	sh := h.MetroDB.City.Create().SetSlug("shanghai").SetName("上海").SaveX(ctx)
	h.MetroDB.Line.Create().SetCode("sh-1").SetCityID(sh.ID).SetName("1号线").SetColor("#E4002B").SaveX(ctx)
	return &closeAccountStack{ChainHarness: h, store: flaky, failMetro: failMetro}
}

func thumbOf(key string) string { return key + ".thumb" }

// account registers a user with a tripmap visit, a photo and its thumbnail (rows and objects) and a
// metro ride. The object keys are unique to the user.
func (s *closeAccountStack) account(subject string) (*Browser, string, string) {
	s.T.Helper()
	ctx := context.Background()
	b := s.FreshBrowser()
	id, err := b.Register(ctx, subject, "", subject)
	if err != nil {
		s.T.Fatal(err)
	}
	key := "owned/" + subject + "/photo.jpg"
	for _, k := range []string{key, thumbOf(key)} {
		if err = s.store.Put(ctx, k, bytes.NewReader([]byte("jpeg")), 4, "image/jpeg"); err != nil {
			s.T.Fatal(err)
		}
	}
	s.TripmapDB.MediaObject.Create().SetUserID(id).SetObjectKey(key).SetThumbnailKey(thumbOf(key)).SetContentType("image/jpeg").SetSizeBytes(4).ExecX(ctx)
	s.TripmapDB.CityVisit.Create().SetUserID(id).SetRegionCode("330100").SetRegionName("杭州市").SetProvinceCode("330000").SetColor("#4b937d").ExecX(ctx)
	if _, err = b.MetroRider.MarkLine(ctx, riderWrite(b, &bffmetrov1.MarkLineRequest{LineCode: "sh-1"})); err != nil {
		s.T.Fatal(err)
	}
	return b, id, key
}

func (s *closeAccountStack) hasObject(key string) bool {
	s.T.Helper()
	r, err := s.store.Get(context.Background(), key)
	if err == nil {
		_ = r.Close()
		return true
	}
	if !errors.Is(err, blobstore.ErrNotFound) {
		s.T.Fatalf("get %s: %v", key, err)
	}
	return false
}

// A restart with a changed configuration never skips a product of a deletion that began: the
// products are fixed when it starts, a missing cleaner holds it (the account stays closed, the
// last product's data stays) and a later correct configuration finishes it, media included.
func TestChainCloseAccountRestartAndConfigChangeNeverSkipAProduct(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	a, aID, aKey := s.account("close_a")
	_, _, bKey := s.account("close_b")

	s.failMetro.Store(true)
	req := &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "close-a-1"}
	if err := a.DeleteAccount(ctx, req, aID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("a failing product must fail the deletion: %v", err)
	}
	// Tripmap, first in the list, is cleaned with its media; metro is not.
	if s.hasObject(aKey) || s.hasObject(thumbOf(aKey)) {
		t.Fatal("the media of the first product survived its cleanup")
	}
	if wf := s.workflowOf(ctx, aID); wf.Stage != schema.DeletionStageCleanup || !slices.Equal(wf.Products, []string{"tripmap", "metro"}) || !slices.Equal(wf.Completed, []string{"tripmap"}) {
		t.Fatalf("workflow after the failing product: %+v", wf)
	}
	aToken := a.RecoveryCookie()

	// The process restarts with metro gone from the configuration (not deployed any more).
	s.dueNow(ctx)
	reduced := s.RestartBFF(func(c *bffconfig.Config) {
		c.Auth.AccountCleanup = []string{"tripmap"}
		delete(c.Upstreams, "metro")
		delete(c.GRPC.Services, "metro")
	})
	if _, err := reduced.RunDeletionsOnce(ctx); err != nil {
		t.Fatal(err)
	}
	wf := s.workflowOf(ctx, aID)
	if wf.Stage != schema.DeletionStageCleanup || !slices.Equal(wf.Completed, []string{"tripmap"}) || wf.LastError == "" {
		t.Fatalf("a missing cleaner must hold the deletion, not skip the product: %+v", wf)
	}
	if n := s.MetroDB.RiderLine.Query().CountX(ctx); n != 2 {
		t.Fatalf("metro data changed while the product was not configured: %d rows", n)
	}
	// Still closed, still readable by the Cookie that started it, and the Cookie is still there.
	if _, err := a.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a held deletion reopened the account: %v", err)
	}
	if d, err := a.GetAccountDeletion(ctx); err != nil || d.GetState() != bffv1.AccountDeletionState_ACCOUNT_DELETION_STATE_PENDING || len(d.GetProducts()) != 2 || len(d.GetCompletedProducts()) != 1 {
		t.Fatalf("pending deletion read: %v %v", d, err)
	}
	if a.RecoveryCookie() != aToken || aToken == "" {
		t.Fatal("the recovery Cookie changed while the deletion was held")
	}

	// A restart with the full configuration and a healthy product finishes it, from the persisted state alone.
	s.failMetro.Store(false)
	s.dueNow(ctx)
	full := s.RestartBFF(nil)
	if n, err := full.RunDeletionsOnce(ctx); err != nil || n != 1 {
		t.Fatalf("restart runner: %d %v", n, err)
	}
	if wf = s.workflowOf(ctx, aID); wf.Stage != schema.DeletionStageDone || !slices.Equal(wf.Completed, []string{"tripmap", "metro"}) {
		t.Fatalf("workflow after the restart: %+v", wf)
	}
	if d, err := a.GetAccountDeletion(ctx); err != nil || d.GetState() != bffv1.AccountDeletionState_ACCOUNT_DELETION_STATE_COMPLETED {
		t.Fatalf("receipt after the restart: %v %v", d, err)
	}
	// All of A is gone, nothing of B moved.
	if n := s.MetroDB.RiderLine.Query().CountX(ctx); n != 1 {
		t.Fatalf("%d metro rows left, want only B's", n)
	}
	if n := s.TripmapDB.CityVisit.Query().CountX(ctx); n != 1 {
		t.Fatalf("%d tripmap visits left, want only B's", n)
	}
	if n := s.TripmapDB.MediaObject.Query().CountX(ctx); n != 1 || !s.hasObject(bKey) || !s.hasObject(thumbOf(bKey)) || s.hasObject(aKey) || s.hasObject(thumbOf(aKey)) {
		t.Fatalf("media: %d rows, B's objects %v/%v, A's objects %v/%v", n, s.hasObject(bKey), s.hasObject(thumbOf(bKey)), s.hasObject(aKey), s.hasObject(thumbOf(aKey)))
	}
}

// While a deletion is unfinished the recovery Cookie is its only credential: nothing the browser or a
// page can call clears it except the finished deletion; the workflow ID opens nothing. The session
// Cookie is a different credential and signing out clears it as usual.
func TestChainCloseAccountKeepsTheRecoveryCookieWhilePending(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	a, aID, aKey := s.account("keep_a")

	s.failMetro.Store(true)
	req := &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "keep-a-1"}
	if err := a.DeleteAccount(ctx, req, aID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("deletion: %v", err)
	}
	token := a.RecoveryCookie()
	if token == "" {
		t.Fatal("no recovery Cookie after the deletion began")
	}
	if a.SessionCookie() == "" {
		t.Fatal("no deletion RPC rewrites the session Cookie: the revoked one stays and is dead on the server")
	}
	if err := a.Logout(ctx); err != nil {
		t.Fatalf("logout of a closed account must be harmless: %v", err)
	}
	if _, err := a.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("me: %v", err)
	}
	if a.RecoveryCookie() != token {
		t.Fatal("signing out or reading the account cleared the recovery Cookie of an unfinished deletion")
	}
	// It still shows and continues the deletion.
	if d, err := a.GetAccountDeletion(ctx); err != nil || d.GetState() != bffv1.AccountDeletionState_ACCOUNT_DELETION_STATE_PENDING {
		t.Fatalf("read: %v %v", d, err)
	}
	if _, err := a.RetryAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("retry while metro is down: %v", err)
	}
	if a.RecoveryCookie() != token {
		t.Fatal("a failed retry cleared the recovery Cookie")
	}
	// The workflow ID is not a credential, as session or recovery Cookie alike.
	wfID := s.BFFDB.AccountDeletion.Query().Where(accountdeletion.UserID(aID)).OnlyX(ctx).ID
	stranger := s.FreshBrowser()
	stranger.SetRecoveryCookie(wfID) // no generation of any credential: it carries a name of its own
	if _, err := stranger.GetAccountDeletion(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("the workflow ID opened a deletion: %v", err)
	}
	s.failMetro.Store(false)
	d, err := a.RetryAccountDeletion(ctx)
	if err != nil || d.GetState() != bffv1.AccountDeletionState_ACCOUNT_DELETION_STATE_COMPLETED {
		t.Fatalf("final retry: %v %v", d, err)
	}
	if a.RecoveryCookie() != "" {
		t.Fatal("the finished deletion did not clear the recovery Cookie")
	}
	if s.hasObject(aKey) {
		t.Fatal("media survived the deletion")
	}
}

// A platform administrator cannot close the account on the web: the account stays open and
// nothing is persisted that a later role change could turn into a deletion.
func TestChainCloseAccountRefusesAPlatformAdministrator(t *testing.T) {
	s := newCloseAccountStack(t)
	ctx := context.Background()
	admin := s.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "close_admin", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	err := admin.DeleteAccount(ctx, &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "admin-1"})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("a platform administrator closed the account on the web: %v", err)
	}
	if n := s.BFFDB.AccountDeletion.Query().CountX(ctx); n != 0 {
		t.Fatalf("a refused deletion left %d workflow rows", n)
	}
	if _, err = admin.Me(ctx); err != nil {
		t.Fatalf("the account was closed by the refusal: %v", err)
	}
}

// closeAccountSeed is what the browser scenario seeded for one isolated test account.
type closeAccountSeed struct {
	ID  string
	Key string
}

// control is the browser scenario's window into the stack behind the front: it reports what is
// left of an isolated test account in the product databases, the object store and the BFF's
// workflow table, and switches the injected metro outage. It only ever touches the accounts
// seeded for the scenario, in the harness's own throwaway schemas and in-memory object store.
func (s *closeAccountStack) control(seeds map[string]closeAccountSeed) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/fail-metro", func(w http.ResponseWriter, r *http.Request) {
		s.failMetro.Store(r.URL.Query().Get("on") == "1")
		w.WriteHeader(http.StatusNoContent)
	})
	// The background runner's pass, for a scenario that must show a deletion finishing while the
	// browser is not looking. With restart=1 the pass is a freshly started BFF's: no memory of the
	// one that began the deletion, only the persisted state.
	mux.HandleFunc("/run-deletions", func(w http.ResponseWriter, r *http.Request) {
		s.dueNow(r.Context())
		runner := s.BFF
		if r.URL.Query().Get("restart") == "1" {
			runner = s.RestartBFF(nil)
		}
		n, err := runner.RunDeletionsOnce(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]int{"ran": n})
	})
	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		seed, ok := seeds[r.URL.Query().Get("subject")]
		if !ok {
			http.Error(w, "unknown test account", http.StatusNotFound)
			return
		}
		ctx := r.Context()
		stage := "none"
		if wf, err := s.BFFDB.AccountDeletion.Query().Where(accountdeletion.UserID(seed.ID)).Only(ctx); err == nil {
			stage = wf.Stage
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"mediaRows": s.TripmapDB.MediaObject.Query().Where(mediaobject.UserID(seed.ID)).CountX(ctx),
			"object":    s.hasObject(seed.Key) || s.hasObject(thumbOf(seed.Key)),
			"visits":    s.TripmapDB.CityVisit.Query().Where(cityvisit.UserID(seed.ID)).CountX(ctx),
			"metroRows": s.MetroDB.RiderLine.Query().Where(riderline.UserID(seed.ID)).CountX(ctx),
			"workflow":  stage,
		})
	})
	return mux
}

// The same recovery with the object store failing, through the real S3 of TRIPMAP_TEST_S3_* when
// it is configured (CI's Backend job, where E2E_REQUIRE_REAL_S3=1 makes a missing configuration a
// failure) and an in-memory store otherwise: an S3 outage keeps the
// deletion open with the media still there, a restart of the BFF keeps holding it, and once the
// store is back the background runner finishes it with every object of the account gone and
// another user's objects untouched. The object keys are unique to the two accounts.
func TestChainCloseAccountRecoversFromAnObjectStoreOutage(t *testing.T) {
	store := objectStoreForChain(t)
	// Registered before anything is written, so a failure half-way still removes every object this
	// test may have made (the keys are fixed by the account names; deleting a missing one is fine).
	t.Cleanup(func() {
		for _, subject := range []string{"s3out_a", "s3out_b"} {
			key := "owned/" + subject + "/photo.jpg"
			for _, k := range []string{key, thumbOf(key)} {
				_ = store.Delete(context.Background(), k)
			}
		}
	})
	s := newCloseAccountStackOn(t, store)
	ctx := context.Background()
	a, aID, aKey := s.account("s3out_a")
	_, _, bKey := s.account("s3out_b")
	for _, k := range []string{aKey, thumbOf(aKey), bKey, thumbOf(bKey)} {
		if !s.hasObject(k) {
			t.Fatalf("object %s is missing before the deletion", k)
		}
	}

	s.store.failDelete.Store(true)
	if err := a.DeleteAccount(ctx, &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "s3out-1"}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("an object store outage must keep the deletion open: %v", err)
	}
	if !s.hasObject(aKey) || !s.hasObject(thumbOf(aKey)) {
		t.Fatal("objects vanished although the store refused the deletes")
	}
	if _, err := a.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("the account stayed open: %v", err)
	}
	if wf := s.workflowOf(ctx, aID); wf.Stage != schema.DeletionStageCleanup || wf.LastError == "" {
		t.Fatalf("workflow during the outage: %+v", wf)
	}

	// A restarted BFF keeps trying and keeps holding it while the outage lasts.
	s.dueNow(ctx)
	restarted := s.RestartBFF(nil)
	if _, err := restarted.RunDeletionsOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if wf := s.workflowOf(ctx, aID); wf.Stage != schema.DeletionStageCleanup {
		t.Fatalf("workflow after a restart during the outage: %+v", wf)
	}
	if !s.hasObject(aKey) {
		t.Fatal("the media went away during the outage")
	}

	s.store.failDelete.Store(false)
	s.dueNow(ctx)
	if n, err := restarted.RunDeletionsOnce(ctx); err != nil || n != 1 {
		t.Fatalf("runner after the outage: %d %v", n, err)
	}
	if wf := s.workflowOf(ctx, aID); wf.Stage != schema.DeletionStageDone {
		t.Fatalf("workflow after the outage: %+v", wf)
	}
	if d, err := a.GetAccountDeletion(ctx); err != nil || d.GetState() != bffv1.AccountDeletionState_ACCOUNT_DELETION_STATE_COMPLETED {
		t.Fatalf("receipt: %v %v", d, err)
	}
	if s.hasObject(aKey) || s.hasObject(thumbOf(aKey)) {
		t.Fatal("an object of the deleted account is still in the store")
	}
	if !s.hasObject(bKey) || !s.hasObject(thumbOf(bKey)) {
		t.Fatal("an object of another account was deleted")
	}
	if n := s.TripmapDB.MediaObject.Query().CountX(ctx); n != 1 {
		t.Fatalf("%d media rows left, want only the other user's", n)
	}
}
