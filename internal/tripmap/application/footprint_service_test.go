package application_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

const (
	green  = "#4b937d"
	blue   = "#719fc6"
	orange = "#d88370"
)

func hangzhou(extra ...func(*tripmapv1.Visit)) *tripmapv1.Visit {
	v := &tripmapv1.Visit{Code: "330100", Name: "杭州市", Province: "330000", Color: green, Date: "2026-10-01", Note: "西湖"}
	for _, f := range extra {
		f(v)
	}
	return v
}

func upsert(e *env, token string, v *tripmapv1.Visit, base int64) (*tripmapv1.UpsertVisitResponse, error) {
	res, err := e.foot.UpsertVisit(context.Background(), bearer(token, &tripmapv1.UpsertVisitRequest{Visit: v, BaseRevision: base}))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func snapshot(t *testing.T, e *env, token string) *tripmapv1.GetFootprintsResponse {
	t.Helper()
	res, err := e.foot.GetFootprints(context.Background(), bearer(token, &tripmapv1.GetFootprintsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg
}

func TestRequiredInitialRevision(t *testing.T) {
	e := newEnv(t)
	token := e.register("initial_revision")
	ctx := t.Context()
	first := hangzhou()
	request := func(v *tripmapv1.Visit) (*tripmapv1.UpsertVisitResponse, error) {
		r, err := e.foot.UpsertVisit(ctx, bearer(token, &tripmapv1.UpsertVisitRequest{
			Visit: v, BaseRevision: 0, RequireRevision: true,
		}))
		if err != nil {
			return nil, err
		}
		return r.Msg, nil
	}
	if _, err := request(first); err != nil {
		t.Fatal(err)
	}
	// An acknowledged request can be safely replayed against the old revision.
	if r, err := request(first); err != nil || r.Changed || r.Revision != 1 {
		t.Fatalf("idempotent retry: %v %v", r, err)
	}
	changed := hangzhou(func(v *tripmapv1.Visit) { v.Note = "another device" })
	_, err := request(changed)
	wantCode(t, err, connect.CodeAborted)
	if got := snapshot(t, e, token); got.Revision != 1 || got.Visits[0].Note != first.Note {
		t.Fatalf("stale zero revision changed data: %v", got)
	}
	_, err = e.foot.DeleteVisit(ctx, bearer(token, &tripmapv1.DeleteVisitRequest{
		Code: first.Code, RequireRevision: true,
	}))
	wantCode(t, err, connect.CodeAborted)
	_, err = e.foot.UpsertProvinceMark(ctx, bearer(token, &tripmapv1.UpsertProvinceMarkRequest{
		Mark: &tripmapv1.ProvinceMark{Province: "330000", Color: first.Color}, RequireRevision: true,
	}))
	wantCode(t, err, connect.CodeAborted)
	// Legacy callers without the new flag still retain their old semantics.
	if _, err := upsert(e, token, changed, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.foot.UpsertProvinceMark(ctx, bearer(token, &tripmapv1.UpsertProvinceMarkRequest{
		Mark: &tripmapv1.ProvinceMark{Province: "330000", Color: first.Color},
	})); err != nil {
		t.Fatal(err)
	}
	_, err = e.foot.DeleteProvinceMark(ctx, bearer(token, &tripmapv1.DeleteProvinceMarkRequest{
		Province: "330000", RequireRevision: true,
	}))
	wantCode(t, err, connect.CodeAborted)
}

func TestFootprintsRoundTrip(t *testing.T) {
	e := newEnv(t)
	token := e.register("alice")
	if got := snapshot(t, e, token); len(got.Visits) != 0 || len(got.ProvinceMarks) != 0 || got.Revision != 0 {
		t.Fatalf("new account is not empty: %v", got)
	}
	res, err := upsert(e, token, hangzhou(), 0)
	if err != nil || !res.Changed || res.Revision != 1 || res.Visit.UpdatedAt == nil {
		t.Fatalf("upsert: %v %v", res, err)
	}
	got := snapshot(t, e, token)
	if len(got.Visits) != 1 || got.Visits[0].Name != "杭州市" || got.Visits[0].Note != "西湖" || got.Visits[0].Date != "2026-10-01" || got.Revision != 1 {
		t.Fatalf("snapshot: %v", got)
	}
	// Empty date and note are valid and stored as given.
	if _, err = upsert(e, token, &tripmapv1.Visit{Code: "330200", Name: "宁波市", Province: "330000", Color: blue}, 0); err != nil {
		t.Fatal(err)
	}
	if got = snapshot(t, e, token); len(got.Visits) != 2 || got.Revision != 2 {
		t.Fatalf("snapshot: %v", got)
	}
}

func TestUsersCannotSeeOrChangeEachOthersFootprints(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	a, b := e.register("usera"), e.register("userb")
	if _, err := upsert(e, a, hangzhou(), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.foot.UpsertProvinceMark(ctx, bearer(a, &tripmapv1.UpsertProvinceMarkRequest{Mark: &tripmapv1.ProvinceMark{Province: "110000", Color: blue}})); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(t, e, b); len(got.Visits) != 0 || len(got.ProvinceMarks) != 0 || got.Revision != 0 {
		t.Fatalf("user B sees user A's data: %v", got)
	}
	// B writing the same city creates B's own record and leaves A's untouched.
	if _, err := upsert(e, b, hangzhou(func(v *tripmapv1.Visit) { v.Color = orange; v.Note = "B" }), 0); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(t, e, a); got.Visits[0].Color != green || got.Visits[0].Note != "西湖" {
		t.Fatalf("user B overwrote user A: %v", got)
	}
	// B deleting the city does not delete A's.
	if _, err := e.foot.DeleteVisit(ctx, bearer(b, &tripmapv1.DeleteVisitRequest{Code: "330100"})); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(t, e, a); len(got.Visits) != 1 || len(got.ProvinceMarks) != 1 {
		t.Fatalf("user B deleted user A's data: %v", got)
	}
}

func TestWritesAreIdempotent(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	token := e.register("alice")
	first, _ := upsert(e, token, hangzhou(), 0)
	retry, err := upsert(e, token, hangzhou(), 0)
	if err != nil || retry.Changed || retry.Revision != first.Revision {
		t.Fatalf("identical upsert must not change anything: %v %v", retry, err)
	}
	changed, err := upsert(e, token, hangzhou(func(v *tripmapv1.Visit) { v.Note = "更新" }), 0)
	if err != nil || !changed.Changed || changed.Revision != first.Revision+1 {
		t.Fatalf("update: %v %v", changed, err)
	}
	del, err := e.foot.DeleteVisit(ctx, bearer(token, &tripmapv1.DeleteVisitRequest{Code: "330100"}))
	if err != nil || !del.Msg.Deleted || del.Msg.Revision != changed.Revision+1 {
		t.Fatalf("delete: %v %v", del, err)
	}
	again, err := e.foot.DeleteVisit(ctx, bearer(token, &tripmapv1.DeleteVisitRequest{Code: "330100"}))
	if err != nil || again.Msg.Deleted || again.Msg.Revision != del.Msg.Revision {
		t.Fatalf("repeated delete must be a no-op: %v %v", again, err)
	}
}

func TestStaleBaseRevisionIsReportedAsConflict(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	token := e.register("alice")
	first, _ := upsert(e, token, hangzhou(), 0) // revision 1
	if _, err := upsert(e, token, &tripmapv1.Visit{Code: "330200", Name: "宁波市", Province: "330000", Color: blue}, first.Revision); err != nil {
		t.Fatalf("matching base revision rejected: %v", err) // revision 2
	}
	_, err := upsert(e, token, hangzhou(func(v *tripmapv1.Visit) { v.Note = "stale" }), first.Revision)
	wantCode(t, err, connect.CodeAborted)
	var detail *tripmapv1.RevisionConflict
	for _, d := range err.(*connect.Error).Details() {
		if v, derr := d.Value(); derr == nil {
			detail, _ = v.(*tripmapv1.RevisionConflict)
		}
	}
	if detail == nil || detail.CurrentRevision != 2 {
		t.Fatalf("conflict detail = %v", detail)
	}
	if got := snapshot(t, e, token); got.Revision != 2 || got.Visits[0].Note != "西湖" {
		t.Fatalf("rejected write leaked: %v", got)
	}
	// A retry of an already-applied write is not a conflict even with an old base.
	if res, err := upsert(e, token, hangzhou(), first.Revision); err != nil || res.Changed {
		t.Fatalf("applied write retried with stale base: %v %v", res, err)
	}
	// Deletes and marks honour the base revision too.
	_, err = e.foot.DeleteVisit(ctx, bearer(token, &tripmapv1.DeleteVisitRequest{Code: "330100", BaseRevision: first.Revision}))
	wantCode(t, err, connect.CodeAborted)
	_, err = e.foot.UpsertProvinceMark(ctx, bearer(token, &tripmapv1.UpsertProvinceMarkRequest{Mark: &tripmapv1.ProvinceMark{Province: "330000", Color: blue}, BaseRevision: first.Revision}))
	wantCode(t, err, connect.CodeAborted)
	_, err = upsert(e, token, hangzhou(), -1)
	wantCode(t, err, connect.CodeInvalidArgument)
}

func TestInvalidFootprintsAreRejected(t *testing.T) {
	e := newEnv(t)
	token := e.register("alice")
	for name, v := range map[string]*tripmapv1.Visit{
		"unknown region":    hangzhou(func(v *tripmapv1.Visit) { v.Code = "999999" }),
		"wrong name":        hangzhou(func(v *tripmapv1.Visit) { v.Name = "宁波市" }),
		"wrong province":    hangzhou(func(v *tripmapv1.Visit) { v.Province = "110000" }),
		"bad color":         hangzhou(func(v *tripmapv1.Visit) { v.Color = "#ff0000" }),
		"impossible date":   hangzhou(func(v *tripmapv1.Visit) { v.Date = "2026-02-30" }),
		"malformed date":    hangzhou(func(v *tripmapv1.Visit) { v.Date = "2026-1-5" }),
		"note too long":     hangzhou(func(v *tripmapv1.Visit) { v.Note = strings.Repeat("a", 1001) }),
		"note UTF-16 limit": hangzhou(func(v *tripmapv1.Visit) { v.Note = strings.Repeat("🌊", 501) }), // 1002 UTF-16 units
		"missing":           nil,
	} {
		_, err := upsert(e, token, v, 0)
		if codeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: code %v (%v)", name, codeOf(err), err)
		}
	}
	// Boundaries that must still be accepted.
	for name, v := range map[string]*tripmapv1.Visit{
		"1000 units": hangzhou(func(v *tripmapv1.Visit) { v.Note = strings.Repeat("🌊", 500) }),
		"leap day":   hangzhou(func(v *tripmapv1.Visit) { v.Date = "2028-02-29" }),
		"empty date": hangzhou(func(v *tripmapv1.Visit) { v.Date = "" }),
	} {
		if _, err := upsert(e, token, v, 0); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
	if got := snapshot(t, e, token); len(got.Visits) != 1 {
		t.Fatalf("expected a single stored visit, got %d", len(got.Visits))
	}
	ctx := context.Background()
	for _, m := range []*tripmapv1.ProvinceMark{{Province: "999999", Color: green}, {Province: "330000", Color: "nope"}, nil} {
		_, err := e.foot.UpsertProvinceMark(ctx, bearer(token, &tripmapv1.UpsertProvinceMarkRequest{Mark: m}))
		wantCode(t, err, connect.CodeInvalidArgument)
	}
	_, err := e.foot.DeleteVisit(ctx, bearer(token, &tripmapv1.DeleteVisitRequest{Code: "x"}))
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = e.foot.DeleteProvinceMark(ctx, bearer(token, &tripmapv1.DeleteProvinceMarkRequest{Province: "x"}))
	wantCode(t, err, connect.CodeInvalidArgument)
}

func TestCityVisitsAndProvinceMarksAreIndependent(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	token := e.register("alice")
	if _, err := e.foot.UpsertProvinceMark(ctx, bearer(token, &tripmapv1.UpsertProvinceMarkRequest{Mark: &tripmapv1.ProvinceMark{Province: "330000", Color: blue}})); err != nil {
		t.Fatal(err)
	}
	if _, err := upsert(e, token, hangzhou(), 0); err != nil {
		t.Fatal(err)
	}
	// Removing the mark keeps the province's city visits.
	del, err := e.foot.DeleteProvinceMark(ctx, bearer(token, &tripmapv1.DeleteProvinceMarkRequest{Province: "330000"}))
	if err != nil || !del.Msg.Deleted {
		t.Fatalf("delete mark: %v %v", del, err)
	}
	if got := snapshot(t, e, token); len(got.Visits) != 1 || len(got.ProvinceMarks) != 0 {
		t.Fatalf("after deleting mark: %v", got)
	}
	// Removing the last city keeps an independent mark.
	if _, err = e.foot.UpsertProvinceMark(ctx, bearer(token, &tripmapv1.UpsertProvinceMarkRequest{Mark: &tripmapv1.ProvinceMark{Province: "330000", Color: orange}})); err != nil {
		t.Fatal(err)
	}
	if _, err = e.foot.DeleteVisit(ctx, bearer(token, &tripmapv1.DeleteVisitRequest{Code: "330100"})); err != nil {
		t.Fatal(err)
	}
	got := snapshot(t, e, token)
	if len(got.Visits) != 0 || len(got.ProvinceMarks) != 1 || got.ProvinceMarks[0].Color != orange {
		t.Fatalf("after deleting city: %v", got)
	}
	// Updating the mark color changes it in place; repeating is a no-op.
	up, err := e.foot.UpsertProvinceMark(ctx, bearer(token, &tripmapv1.UpsertProvinceMarkRequest{Mark: &tripmapv1.ProvinceMark{Province: "330000", Color: orange}}))
	if err != nil || up.Msg.Changed {
		t.Fatalf("repeat mark upsert: %v %v", up, err)
	}
}

func TestConcurrentWritesAreSerialized(t *testing.T) {
	e := newEnv(t)
	token := e.register("alice")
	cities := []struct{ code, name string }{{"330100", "杭州市"}, {"330200", "宁波市"}, {"330300", "温州市"}, {"330400", "嘉兴市"}, {"330500", "湖州市"}, {"330600", "绍兴市"}}
	var wg sync.WaitGroup
	errs := make(chan error, len(cities))
	for _, c := range cities {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := upsert(e, token, &tripmapv1.Visit{Code: c.code, Name: c.name, Province: "330000", Color: green}, 0)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got := snapshot(t, e, token)
	if len(got.Visits) != len(cities) || got.Revision != int64(len(cities)) {
		t.Fatalf("lost writes: %d visits, revision %d", len(got.Visits), got.Revision)
	}
}
