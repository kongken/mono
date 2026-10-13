package application_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"connectrpc.com/connect"
	"go.orx.me/apps/trip-map/internal/ent/accountdeletionreceipt"
	"go.orx.me/apps/trip-map/internal/ent/session"
	"go.orx.me/apps/trip-map/internal/ent/user"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
)

func putCity(code, name string) *tripmapv1.FootprintMutation {
	return &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertVisit{UpsertVisit: &tripmapv1.Visit{Code: code, Name: name, Province: "330000", Color: "#4b937d", Date: "2026-10-13"}}}
}
func TestBatchDeltaAtomicityAndReplay(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.register("delta_a"), e.register("delta_b")
	batch := &tripmapv1.SyncChangesRequest{MutationId: "batch-one", BaseRevision: 0, Mutations: []*tripmapv1.FootprintMutation{putCity("330100", "杭州市"), putCity("330200", "宁波市")}}
	first, err := e.foot.SyncChanges(ctx, bearer(a, batch))
	if err != nil {
		t.Fatal(err)
	}
	if first.Msg.Revision != 2 || first.Msg.ChangedCount != 2 {
		t.Fatal(first.Msg)
	}
	page, err := e.foot.GetChanges(ctx, bearer(a, &tripmapv1.GetChangesRequest{Limit: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if !page.Msg.HasMore || page.Msg.NextRevision != 1 || page.Msg.CurrentRevision != 2 || len(page.Msg.Changes) != 1 {
		t.Fatal(page.Msg)
	}
	other, err := e.foot.GetChanges(ctx, bearer(b, &tripmapv1.GetChangesRequest{}))
	if err != nil || len(other.Msg.Changes) != 0 {
		t.Fatalf("cross user delta: %v %v", other, err)
	}
	if _, err = e.foot.DeleteVisit(ctx, bearer(a, &tripmapv1.DeleteVisitRequest{Code: "330100", BaseRevision: 2, RequireRevision: true})); err != nil {
		t.Fatal(err)
	}
	replay, err := e.foot.SyncChanges(ctx, bearer(a, batch))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Revision != 2 {
		t.Fatalf("replay %v %v", replay, err)
	}
	snap, err := e.foot.GetFootprints(ctx, bearer(a, &tripmapv1.GetFootprintsRequest{}))
	if err != nil || len(snap.Msg.Visits) != 1 || snap.Msg.Revision != 3 {
		t.Fatalf("receipt overwrote newer data: %v %v", snap, err)
	}
	batch.Mutations = []*tripmapv1.FootprintMutation{putCity("330300", "温州市")}
	_, err = e.foot.SyncChanges(ctx, bearer(a, batch))
	wantCode(t, err, connect.CodeAlreadyExists)
	_, err = e.foot.SyncChanges(ctx, bearer(a, &tripmapv1.SyncChangesRequest{MutationId: "invalid", BaseRevision: 3, Mutations: []*tripmapv1.FootprintMutation{putCity("330100", "杭州市"), putCity("bogus", "非法城市")}}))
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = e.foot.SyncChanges(ctx, bearer(a, &tripmapv1.SyncChangesRequest{MutationId: "stale", BaseRevision: 2, Mutations: []*tripmapv1.FootprintMutation{putCity("330100", "杭州市")}}))
	wantCode(t, err, connect.CodeAborted)
	snap, err = e.foot.GetFootprints(ctx, bearer(a, &tripmapv1.GetFootprintsRequest{}))
	if err != nil || len(snap.Msg.Visits) != 1 || snap.Msg.Revision != 3 {
		t.Fatal("failed batch left partial writes")
	}
	page, err = e.foot.GetChanges(ctx, bearer(a, &tripmapv1.GetChangesRequest{SinceRevision: 2}))
	if err != nil || len(page.Msg.Changes) != 1 || page.Msg.Changes[0].Mutation.GetDeleteVisit() != "330100" {
		t.Fatalf("legacy tombstone %v %v", page, err)
	}
}
func TestDeltaRequiresSnapshotForLegacyHistory(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	token := e.register("old_history")
	u, err := e.store.DB.User.Query().Where(user.UsernameEQ("old_history")).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.store.DB.User.UpdateOneID(u.ID).SetRevision(10).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := e.foot.GetChanges(ctx, bearer(token, &tripmapv1.GetChangesRequest{}))
	if err != nil || !page.Msg.ResyncRequired {
		t.Fatalf("missing history: %v %v", page, err)
	}
	if _, err = e.foot.UpsertVisit(ctx, bearer(token, &tripmapv1.UpsertVisitRequest{Visit: putCity("330100", "杭州市").GetUpsertVisit(), BaseRevision: 10, RequireRevision: true})); err != nil {
		t.Fatal(err)
	}
	page, err = e.foot.GetChanges(ctx, bearer(token, &tripmapv1.GetChangesRequest{SinceRevision: 10}))
	if err != nil || page.Msg.ResyncRequired || page.Msg.NextRevision != 11 {
		t.Fatalf("new history: %v %v", page, err)
	}
}

type deletionStore struct {
	fail bool
	keys []string
}

func (s *deletionStore) Put(context.Context, string, io.ReadSeeker, int64, string) error { return nil }
func (s *deletionStore) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("unused")
}
func (s *deletionStore) Delete(_ context.Context, key string) error {
	s.keys = append(s.keys, key)
	if s.fail {
		return errors.New("S3 unavailable")
	}
	return nil
}
func TestAccountDeletionVerificationCleanupAndLostReply(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.register("delete_a"), e.register("delete_b")
	_, err := e.auth.DeleteAccount(ctx, bearer(a, &tripmapv1.DeleteAccountRequest{Confirmation: "DELETE", Password: "wrong"}))
	wantCode(t, err, connect.CodePermissionDenied)
	_, err = e.auth.DeleteAccount(ctx, bearer(a, &tripmapv1.DeleteAccountRequest{Password: "correct horse battery"}))
	wantCode(t, err, connect.CodeInvalidArgument)
	u, err := e.store.DB.User.Query().Where(user.UsernameEQ("delete_a")).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.foot.SyncChanges(ctx, bearer(a, &tripmapv1.SyncChangesRequest{MutationId: "before-delete", Mutations: []*tripmapv1.FootprintMutation{putCity("330100", "杭州市")}})); err != nil {
		t.Fatal(err)
	}
	if err = e.store.DB.MediaObject.Create().SetUserID(u.ID).SetObjectKey("owned/photo.jpg").SetContentType("image/jpeg").SetSizeBytes(10).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	blobs := &deletionStore{fail: true}
	e.store.Blobs = blobs
	req := &tripmapv1.DeleteAccountRequest{Confirmation: "DELETE", Password: "correct horse battery"}
	_, err = e.auth.DeleteAccount(ctx, bearer(a, req))
	wantCode(t, err, connect.CodeUnavailable)
	_, err = e.foot.GetFootprints(ctx, bearer(a, &tripmapv1.GetFootprintsRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)
	pending, err := e.auth.Login(ctx, connect.NewRequest(&tripmapv1.LoginRequest{Username: "delete_a", Password: "correct horse battery"}))
	if err != nil || !pending.Msg.User.DeletionPending {
		t.Fatalf("pending reauthentication %v %v", pending, err)
	}
	blobs.fail = false
	if _, err = e.auth.DeleteAccount(ctx, bearer(pending.Msg.Token, req)); err != nil {
		t.Fatal(err)
	}
	if len(blobs.keys) != 2 || blobs.keys[1] != "owned/photo.jpg" {
		t.Fatal(blobs.keys)
	}
	// The very same deleting token can recover a lost successful reply.
	if _, err = e.auth.DeleteAccount(ctx, bearer(pending.Msg.Token, req)); err != nil {
		t.Fatal("deletion receipt", err)
	}
	_, err = e.auth.Me(ctx, bearer(pending.Msg.Token, &tripmapv1.MeRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)
	_, err = e.auth.Login(ctx, connect.NewRequest(&tripmapv1.LoginRequest{Username: "delete_a", Password: "correct horse battery"}))
	wantCode(t, err, connect.CodeUnauthenticated)
	for _, count := range []int{e.store.DB.CityVisit.Query().CountX(ctx), e.store.DB.FootprintChange.Query().CountX(ctx), e.store.DB.SyncReceipt.Query().CountX(ctx), e.store.DB.MediaObject.Query().CountX(ctx)} {
		if count != 0 {
			t.Fatalf("owned records remain: %d", count)
		}
	}
	if e.store.DB.Session.Query().Where(session.UserID(u.ID)).CountX(ctx) != 0 {
		t.Fatal("sessions remain")
	}
	if _, err = e.auth.Me(ctx, bearer(b, &tripmapv1.MeRequest{})); err != nil {
		t.Fatal("other account affected", err)
	}
	receipt := e.store.DB.AccountDeletionReceipt.Query().FirstX(ctx)
	if _, err = e.store.DB.AccountDeletionReceipt.Delete().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err = e.store.DB.AccountDeletionReceipt.Create().SetTokenHash(receipt.TokenHash).SetExpiresAt(time.Now().Add(-time.Hour)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if e.store.DB.AccountDeletionReceipt.Query().Where(accountdeletionreceipt.ExpiresAtGT(time.Now())).CountX(ctx) != 0 {
		t.Fatal("receipt expiry")
	}
	_, err = e.auth.DeleteAccount(ctx, bearer(pending.Msg.Token, req))
	wantCode(t, err, connect.CodeUnauthenticated)
}
