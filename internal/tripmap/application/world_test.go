package application_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/ent/footprintchange"
	"github.com/kongken/mono/internal/tripmap/ent/syncreceipt"
	"github.com/kongken/mono/internal/tripmap/ent/worlddocument"
	"github.com/kongken/mono/internal/tripmap/ent/worldreceipt"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/proto"
)

func tokyoDoc() *tripmapv1.WorldDocument {
	return &tripmapv1.WorldDocument{
		Kind: "world", Version: 1,
		Countries: []*tripmapv1.WorldCountry{{Code: "JP", Color: "#719fc6"}},
		Places: []*tripmapv1.WorldPlace{{
			Id: "p1", Name: "东京", Country: "JP", LatE6: 35_676_200, LngE6: 139_650_300,
			Trips: []*tripmapv1.WorldTrip{{Id: "t1", Date: "2026-04-01", Note: "樱花季"}},
		}},
	}
}

func replaceWorld(e *env, user string, base int64, id string, doc *tripmapv1.WorldDocument) (*tripmapv1.ReplaceWorldResponse, error) {
	res, err := e.world.ReplaceWorld(context.Background(), bearer(user, &tripmapv1.ReplaceWorldRequest{BaseRevision: base, RequestId: id, Document: doc}))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func TestWorldServiceRequiresAKnownUser(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	_, err := e.world.GetWorld(ctx, connect.NewRequest(&tripmapv1.GetWorldRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)
	_, err = replaceWorld(e, "", 0, "r1", tokyoDoc())
	wantCode(t, err, connect.CodeUnauthenticated)
	// A session for a user tripmap has no row for is not a user: nothing is created for it.
	_, err = e.world.GetWorld(ctx, bearer("00000000-0000-0000-0000-000000000000", &tripmapv1.GetWorldRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)
	_, err = replaceWorld(e, "00000000-0000-0000-0000-000000000000", 0, "r1", tokyoDoc())
	wantCode(t, err, connect.CodeUnauthenticated)
	if n := e.store.DB.WorldDocument.Query().CountX(ctx) + e.store.DB.WorldReceipt.Query().CountX(ctx); n != 0 {
		t.Fatalf("%d rows for an unknown user", n)
	}

	alice := e.register("alice_world")
	got, err := e.world.GetWorld(ctx, bearer(alice, &tripmapv1.GetWorldRequest{}))
	if err != nil || got.Msg.GetRevision() != 0 || !proto.Equal(got.Msg.GetDocument(), &tripmapv1.WorldDocument{Kind: "world", Version: 1}) {
		t.Fatalf("new user: %v %v", got, err)
	}
	if got.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("a world read must not be cached")
	}
}

// The world is its own store: writing it touches neither the footprint tables nor their receipts
// and revision, and the footprint API cannot see it.
func TestWorldWritesStayOutOfFootprintData(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.register("alice_world_apart")

	if _, err := replaceWorld(e, alice, 0, "r1", tokyoDoc()); err != nil {
		t.Fatal(err)
	}
	u := e.store.DB.User.GetX(ctx, alice)
	if u.Revision != 0 {
		t.Fatalf("the footprint revision moved to %d", u.Revision)
	}
	if n := e.store.DB.SyncReceipt.Query().Where(syncreceipt.UserID(alice)).CountX(ctx); n != 0 {
		t.Fatalf("%d footprint receipts", n)
	}
	if n := e.store.DB.FootprintChange.Query().Where(footprintchange.UserID(alice)).CountX(ctx); n != 0 {
		t.Fatalf("%d footprint changes", n)
	}
	if n := e.store.DB.CityVisit.Query().CountX(ctx) + e.store.DB.Trip.Query().CountX(ctx) + e.store.DB.Snapshot.Query().CountX(ctx) + e.store.DB.Share.Query().CountX(ctx); n != 0 {
		t.Fatalf("%d footprint, snapshot or share rows", n)
	}
	fp, err := e.foot.GetFootprints(ctx, bearer(alice, &tripmapv1.GetFootprintsRequest{}))
	if err != nil || len(fp.Msg.GetVisits()) != 0 || len(fp.Msg.GetProvinceMarks()) != 0 {
		t.Fatalf("footprints: %v %v", fp, err)
	}
}

func TestWorldReceiptsKeepTheNewestThirtyTwoInOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.register("alice_world_receipts")
	bob := e.register("bob_world_receipts")
	other := func(n int) *tripmapv1.WorldDocument {
		d := tokyoDoc()
		d.Places[0].LatE6 = int32(n)
		return d
	}
	// 40 changing writes and, every tenth time, a conflict that must not take part.
	for n := 1; n <= 40; n++ {
		if res, err := replaceWorld(e, alice, int64(n-1), fmt.Sprintf("r-%02d", n), other(n)); err != nil || !res.Applied || res.Revision != int64(n) {
			t.Fatalf("write %d: %v %v", n, res, err)
		}
		if n%10 != 0 {
			continue
		}
		if res, err := replaceWorld(e, alice, 0, "stale", other(0)); err != nil || res.Applied {
			t.Fatalf("a stale write at %d: %v %v", n, res, err)
		}
	}
	if _, err := replaceWorld(e, bob, 0, "r-01", tokyoDoc()); err != nil {
		t.Fatal(err)
	}

	rows := e.store.DB.WorldReceipt.Query().Where(worldreceipt.UserID(alice)).Order(ent.Asc(worldreceipt.FieldSeq)).AllX(ctx)
	if len(rows) != 32 {
		t.Fatalf("%d receipts", len(rows))
	}
	for i, r := range rows { // r-09 .. r-40, each with its own result revision, digests are hex SHA-256
		want := 9 + i
		if r.RequestID != fmt.Sprintf("r-%02d", want) || r.Revision != int64(want) || r.Seq != int64(want) || len(r.Digest) != 64 {
			t.Fatalf("receipt %d: %+v", i, r)
		}
	}
	// Bob's receipts are not Alice's.
	if n := e.store.DB.WorldReceipt.Query().Where(worldreceipt.UserID(bob)).CountX(ctx); n != 1 {
		t.Fatalf("bob has %d receipts", n)
	}
	// The row is one per user, holds counts, and never the document itself in the receipts.
	row := e.store.DB.WorldDocument.Query().Where(worlddocument.UserID(alice)).OnlyX(ctx)
	if row.Revision != 40 || row.PlaceCount != 1 || row.TripCount != 1 {
		t.Fatalf("document row: %+v", row)
	}
}

// A refused first write (the per-user unique row was created by someone else first) is retried
// against what is stored now, and never surfaces as an internal error.
func TestWorldFirstCreationRaceNeverReturnsInternalError(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.register("alice_world_race")

	var failures atomic.Int32
	failures.Store(1)
	e.store.DB.WorldDocument.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if m.Op().Is(ent.OpCreate) && failures.Add(-1) >= 0 {
				return nil, &ent.ConstraintError{}
			}
			return next.Mutate(ctx, m)
		})
	})
	res, err := replaceWorld(e, alice, 0, "race-1", tokyoDoc())
	if err != nil || !res.Applied || res.Revision != 1 {
		t.Fatalf("after one lost race: %v %v", res, err)
	}
	if e.store.DB.WorldDocument.Query().CountX(ctx) != 1 || e.store.DB.WorldReceipt.Query().CountX(ctx) != 1 {
		t.Fatal("a retry left extra rows")
	}

	// If the constraint keeps refusing, the caller is told to retry; it is not an internal error.
	bob := e.register("bob_world_race")
	failures.Store(1000)
	_, err = replaceWorld(e, bob, 0, "race-2", tokyoDoc())
	wantCode(t, err, connect.CodeAborted)
	failures.Store(0)
	if n := e.store.DB.WorldReceipt.Query().Where(worldreceipt.UserID(bob)).CountX(ctx); n != 0 {
		t.Fatalf("a refused write left %d receipts", n)
	}
}

func TestWorldUnreadableStoredPayloadIsAnInternalError(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.register("alice_world_damaged")
	if _, err := replaceWorld(e, alice, 0, "r1", tokyoDoc()); err != nil {
		t.Fatal(err)
	}
	e.store.DB.WorldDocument.Update().Where(worlddocument.UserID(alice)).SetPayload([]byte("damaged")).ExecX(ctx)
	_, err := e.world.GetWorld(ctx, bearer(alice, &tripmapv1.GetWorldRequest{}))
	wantCode(t, err, connect.CodeInternal)
	if strings.Contains(err.Error(), "gzip") {
		t.Fatalf("the error leaks storage details: %v", err)
	}
	// Writing over a damaged row is not guessed at either.
	_, err = replaceWorld(e, alice, 1, "r2", tokyoDoc())
	wantCode(t, err, connect.CodeInternal)
}

func TestWorldIsRemovedWithTheAccount(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.register("alice_world_cleanup")
	bob := e.register("bob_world_cleanup")
	for _, u := range []string{alice, bob} {
		if _, err := replaceWorld(e, u, 0, "r1", tokyoDoc()); err != nil {
			t.Fatal(err)
		}
	}
	e.mustCleanupAccount(ctx, alice)
	if n := e.store.DB.WorldDocument.Query().Where(worlddocument.UserID(alice)).CountX(ctx) + e.store.DB.WorldReceipt.Query().Where(worldreceipt.UserID(alice)).CountX(ctx); n != 0 {
		t.Fatalf("%d world rows left", n)
	}
	if e.store.DB.WorldDocument.Query().Where(worlddocument.UserID(bob)).CountX(ctx) != 1 || e.store.DB.WorldReceipt.Query().Where(worldreceipt.UserID(bob)).CountX(ctx) != 1 {
		t.Fatal("another user's world was removed")
	}
	// Cleanup can be repeated.
	e.mustCleanupAccount(ctx, alice)
}

func TestWorldWritesAreRateLimitedPerUser(t *testing.T) {
	e := newEnv(t)
	alice := e.register("alice_world_limit")
	bob := e.register("bob_world_limit")
	limited := -1
	for i := 1; i <= 70; i++ {
		_, err := replaceWorld(e, alice, 99, fmt.Sprintf("l-%d", i), tokyoDoc()) // conflicts are requests too
		if err != nil {
			wantCode(t, err, connect.CodeResourceExhausted)
			limited = i
			break
		}
	}
	if limited != 61 {
		t.Fatalf("limited at request %d, want 61 (60 per minute)", limited)
	}
	if _, err := replaceWorld(e, bob, 0, "l-1", tokyoDoc()); err != nil {
		t.Fatalf("another user is limited too: %v", err)
	}
}
