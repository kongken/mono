package application_test

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/tripmap/ent/user"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

func TestLegacyIslandCloudCompatibility(t *testing.T) {
	e := newEnv(t)
	token := e.register("island_legacy")
	other := e.register("island_other")
	ctx := t.Context()
	u, err := e.store.DB.User.Query().Where(user.Username("island_legacy")).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for code, name := range map[string]string{"tw-09020": "金门县", "tw-09007": "连江县"} {
		_, err := e.store.DB.CityVisit.Create().SetUserID(u.ID).SetRegionCode(code).SetRegionName(name).
			SetProvinceCode("710000").SetColor(green).SetTravelDate("2026-10-12").SetNote("旧旅行记忆").Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	got := snapshot(t, e, token)
	if got.Revision != 0 || len(got.Visits) != 2 {
		t.Fatalf("legacy snapshot = %v", got)
	}
	for _, visit := range got.Visits {
		if visit.Province != "350000" || visit.Note != "旧旅行记忆" {
			t.Fatalf("legacy visit = %v", visit)
		}
		// A canonical replay is idempotent even when the physical row is still old.
		if r, err := upsert(e, token, visit, 0); err != nil || r.Changed || r.Revision != 0 {
			t.Fatalf("canonical replay: %v %v", r, err)
		}
		// Older clients can still send the original province without corrupting ownership.
		visit.Province = "710000"
		if r, err := upsert(e, token, visit, 0); err != nil || r.Changed || r.Visit.Province != "350000" {
			t.Fatalf("legacy replay: %v %v", r, err)
		}
	}
	if got := snapshot(t, e, other); len(got.Visits) != 0 {
		t.Fatal("legacy reads leaked another user's records")
	}
	visit := got.Visits[0]
	visit.Note = "新旅行记忆"
	r, err := e.foot.UpsertVisit(ctx, bearer(token, &tripmapv1.UpsertVisitRequest{Visit: visit, RequireRevision: true}))
	if err != nil || !r.Msg.Changed || r.Msg.Revision != 1 || r.Msg.Visit.Province != "350000" {
		t.Fatalf("edit legacy visit: %v %v", r, err)
	}
	visit.Note = "过期修改"
	_, err = e.foot.UpsertVisit(ctx, bearer(token, &tripmapv1.UpsertVisitRequest{Visit: visit, RequireRevision: true}))
	wantCode(t, err, connect.CodeAborted)
	visit.Province = "330000"
	_, err = upsert(e, token, visit, 0)
	wantCode(t, err, connect.CodeInvalidArgument)
}
