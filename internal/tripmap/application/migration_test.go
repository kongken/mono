package application_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"connectrpc.com/connect"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

// everyFootprint builds one mutation per city and per province mark in the
// region index: the largest set a local-to-cloud migration can send.
func everyFootprint(t *testing.T) []*tripmapv1.FootprintMutation {
	t.Helper()
	data, err := os.ReadFile("../regions/regions.json")
	if err != nil {
		t.Fatal(err)
	}
	var list []struct {
		Code   string `json:"code"`
		Cities []struct {
			Code string `json:"code"`
			Name string `json:"name"`
		} `json:"cities"`
	}
	if err = json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	var out []*tripmapv1.FootprintMutation
	for _, p := range list {
		out = append(out, &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertMark{UpsertMark: &tripmapv1.ProvinceMark{Province: p.Code, Color: "#4b937d"}}})
		for _, c := range p.Cities {
			out = append(out, &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertVisit{UpsertVisit: &tripmapv1.Visit{Code: c.Code, Name: c.Name, Province: p.Code, Color: "#e2af5c", Date: "2026-10-01", Note: "本地迁移"}}})
		}
	}
	return out
}

func TestMigrationOfEveryFootprintIsOneAtomicBatch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("migrate_a")
	all := everyFootprint(t)
	if len(all) <= 100 || len(all) > 2000 {
		t.Fatalf("unexpected region index size %d", len(all))
	}
	req := &tripmapv1.SyncChangesRequest{MutationId: "migrate-all", BaseRevision: 0, Mutations: all}
	first, err := e.foot.SyncChanges(ctx, bearer(a, req))
	if err != nil {
		t.Fatal(err)
	}
	if int(first.Msg.ChangedCount) != len(all) || first.Msg.Revision != int64(len(all)) {
		t.Fatalf("applied %d of %d, revision %d", first.Msg.ChangedCount, len(all), first.Msg.Revision)
	}
	// A lost response is retried with the same ID and does not write again.
	again, err := e.foot.SyncChanges(ctx, bearer(a, req))
	if err != nil || !again.Msg.Replayed || again.Msg.Revision != first.Msg.Revision {
		t.Fatalf("replay %v %v", again, err)
	}
	snap, err := e.foot.GetFootprints(ctx, bearer(a, &tripmapv1.GetFootprintsRequest{}))
	if err != nil || len(snap.Msg.Visits)+len(snap.Msg.ProvinceMarks) != len(all) {
		t.Fatalf("snapshot %v %v", snap, err)
	}
}

func TestOversizedBatchIsRejectedWithoutWrites(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("migrate_big")
	all := everyFootprint(t)
	for len(all) <= 2000 {
		all = append(all, all...)
	}
	_, err := e.foot.SyncChanges(ctx, bearer(a, &tripmapv1.SyncChangesRequest{MutationId: "too-big", Mutations: all[:2001]}))
	wantCode(t, err, connect.CodeInvalidArgument)
	snap, err := e.foot.GetFootprints(ctx, bearer(a, &tripmapv1.GetFootprintsRequest{}))
	if err != nil || len(snap.Msg.Visits) != 0 || snap.Msg.Revision != 0 {
		t.Fatalf("oversized batch wrote data: %v %v", snap, err)
	}
}

func TestStaleMigrationLeavesCloudUntouched(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("migrate_stale")
	seed := &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertVisit{UpsertVisit: &tripmapv1.Visit{Code: "330100", Name: "杭州市", Province: "330000", Color: "#4b937d", Date: "2026-10-01", Note: "other device"}}}
	if _, err := e.foot.SyncChanges(ctx, bearer(a, &tripmapv1.SyncChangesRequest{MutationId: "other-device", Mutations: []*tripmapv1.FootprintMutation{seed}})); err != nil {
		t.Fatal(err)
	}
	// The migration was planned against revision 0, but another device wrote first.
	_, err := e.foot.SyncChanges(ctx, bearer(a, &tripmapv1.SyncChangesRequest{MutationId: "migrate-stale", BaseRevision: 0, Mutations: everyFootprint(t)}))
	wantCode(t, err, connect.CodeAborted)
	snap, err := e.foot.GetFootprints(ctx, bearer(a, &tripmapv1.GetFootprintsRequest{}))
	if err != nil || len(snap.Msg.Visits) != 1 || snap.Msg.Revision != 1 {
		t.Fatalf("stale migration left partial writes: %v %v", snap, err)
	}
}
