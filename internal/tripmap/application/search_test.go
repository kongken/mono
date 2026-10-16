package application_test

import (
	"strings"
	"testing"

	"connectrpc.com/connect"
	"go.orx.me/apps/trip-map/internal/tripmap/auth"
	"go.orx.me/apps/trip-map/internal/tripmap/ent/session"
	"go.orx.me/apps/trip-map/internal/tripmap/ent/user"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
)

func TestNoteSearchIsolationTermsPaginationAndChanges(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	_, err := e.search.SearchNotes(ctx, connect.NewRequest(&tripmapv1.SearchNotesRequest{Query: "西湖"}))
	wantCode(t, err, connect.CodeUnauthenticated)
	a, b := e.register("search_alice"), e.register("search_bob")
	rev := e.mustSync2(a, "search-seed", 0,
		upCity("330100", "杭州市", "330000", "#4b937d"),
		upTrip("330100", "t-a", "2026-01-01", "西湖徒步 100%_ SQL' <script> Café"),
		upTrip("330100", "t-b", "2026-03-01", "西湖晚霞"),
		upTrip("330100", "t-c", "", "未填日期的西湖"),
	)
	e.mustSync2(b, "bob-search", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "t-private", "2026-04-01", "西湖 他人私密"))
	for _, q := range []string{"西湖 徒步", "100%_", "SQL'", "CAFÉ", "<script>"} {
		r, err := e.search.SearchNotes(ctx, bearer(a, &tripmapv1.SearchNotesRequest{Query: q}))
		if err != nil || len(r.Msg.Matches) != 1 || r.Msg.Matches[0].TripId != "t-a" {
			t.Fatalf("literal query %q: %v / %v", q, r, err)
		}
		if r.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("private response is cacheable")
		}
	}
	r, err := e.search.SearchNotes(ctx, bearer(a, &tripmapv1.SearchNotesRequest{Query: "西湖", Limit: 1}))
	if err != nil || len(r.Msg.Matches) != 1 || r.Msg.Matches[0].TripId != "t-b" || !r.Msg.HasMore || r.Msg.NextOffset != 1 {
		t.Fatalf("first page: %v / %v", r, err)
	}
	page, err := e.search.SearchNotes(ctx, bearer(a, &tripmapv1.SearchNotesRequest{Query: "西湖", Limit: 1, Offset: 1, ExpectedRevision: new(rev)}))
	if err != nil || page.Msg.Matches[0].TripId != "t-a" {
		t.Fatalf("second page: %v / %v", page, err)
	}
	page, err = e.search.SearchNotes(ctx, bearer(a, &tripmapv1.SearchNotesRequest{Query: "西湖", Limit: 1, Offset: 2, ExpectedRevision: new(rev)}))
	if err != nil || page.Msg.Matches[0].TripId != "t-c" || page.Msg.HasMore {
		t.Fatalf("undated final page: %v / %v", page, err)
	}
	for _, q := range []string{"他人私密", "100%_miss", "' OR 1=1 --", "西湖", ""} {
		province := ""
		if q == "西湖" {
			province = "350000"
		}
		r, err := e.search.SearchNotes(ctx, bearer(a, &tripmapv1.SearchNotesRequest{Query: q, ProvinceCode: province}))
		if err != nil || len(r.Msg.Matches) != 0 {
			t.Fatalf("must not match %q: %v / %v", q, r, err)
		}
	}
	rev = e.mustSync2(a, "search-modify", rev, upTrip("330100", "t-a", "2026-01-01", "龙井"), delTrip("330100", "t-b"))
	_, err = e.search.SearchNotes(ctx, bearer(a, &tripmapv1.SearchNotesRequest{Query: "西湖", Offset: 1, ExpectedRevision: new(rev - 1)}))
	wantCode(t, err, connect.CodeAborted)
	r, err = e.search.SearchNotes(ctx, bearer(a, &tripmapv1.SearchNotesRequest{Query: "西湖"}))
	if err != nil || len(r.Msg.Matches) != 1 || r.Msg.Matches[0].TripId != "t-c" {
		t.Fatalf("latest notes only: %v / %v", r, err)
	}
}

func TestNoteSearchLegacyCompatibilityAndCanonicalProvince(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	a := e.register("search_legacy")
	res, err := e.foot.UpsertVisit(ctx, bearer(a, &tripmapv1.UpsertVisitRequest{Visit: &tripmapv1.Visit{Code: "tw-09020", Name: "金门县", Province: "710000", Color: "#4b937d", Date: "2026-01-01", Note: "海边徒步"}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, province := range []string{"350000", "710000"} {
		r, err := e.search.SearchNotes(ctx, bearer(a, &tripmapv1.SearchNotesRequest{Query: "徒步", ProvinceCode: province}))
		if err != nil {
			t.Fatal(err)
		}
		if province == "350000" && (len(r.Msg.Matches) != 1 || r.Msg.Matches[0].ProvinceCode != province || r.Msg.Matches[0].TripId != "legacy-tw-09020") {
			t.Fatalf("legacy province: %v", r)
		}
		if province == "710000" && len(r.Msg.Matches) != 0 {
			t.Fatal("legacy islands leak into Taiwan search")
		}
	}
	e.mustSync2(a, "legacy-search-upgrade", res.Msg.Revision, upTrip("tw-09020", "legacy-tw-09020", "2026-01-01", "新记忆"))
	r, err := e.search.SearchNotes(ctx, bearer(a, &tripmapv1.SearchNotesRequest{Query: "徒步"}))
	if err != nil || len(r.Msg.Matches) != 0 {
		t.Fatalf("frozen legacy note must not match after upgrade: %v / %v", r, err)
	}
}

func TestNoteSearchValidationRevocationAndRateLimit(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	a := e.register("search_limits")
	for _, req := range []*tripmapv1.SearchNotesRequest{
		{Query: strings.Repeat("😀", 51)}, {Query: "a b c d e f g h i"}, {ProvinceCode: "invalid"}, {Limit: 51}, {Offset: 1}, {Offset: 100001},
	} {
		_, err := e.search.SearchNotes(ctx, bearer(a, req))
		wantCode(t, err, connect.CodeInvalidArgument)
	}
	for range 60 {
		_, err := e.search.SearchNotes(ctx, bearer(a, &tripmapv1.SearchNotesRequest{Query: "不存在"}))
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.search.SearchNotes(ctx, bearer(a, &tripmapv1.SearchNotesRequest{Query: "不存在"}))
	wantCode(t, err, connect.CodeResourceExhausted)
	sess, err := e.store.DB.Session.Query().Where(session.TokenHash(auth.HashToken(a))).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.DB.User.Update().Where(user.ID(sess.UserID)).SetDisabled(true).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = e.search.SearchNotes(ctx, bearer(a, &tripmapv1.SearchNotesRequest{Query: "西湖"}))
	wantCode(t, err, connect.CodeUnauthenticated)
}
