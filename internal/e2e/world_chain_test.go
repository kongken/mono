package e2e_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	tripmapworldent "github.com/kongken/mono/internal/tripmap/ent/worlddocument"
	tripmapworldreceipt "github.com/kongken/mono/internal/tripmap/ent/worldreceipt"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"google.golang.org/protobuf/proto"
)

// The world footprint document over the real chain: browser (Cookie + CSRF + Origin) ->
// BFF -> tripmap -> PostgreSQL. docs/trip-map/world-spec.md section 9.1 and scenarios D, E, F
// and K; the validation rules themselves are exercised against the shared fixture in
// internal/tripmap/world.

func worldMark(code, color string) *bfftripmapv1.WorldCountry {
	return &bfftripmapv1.WorldCountry{Code: code, Color: color}
}

func worldPlace(id, name, country string, latE6, lngE6 int32, trips ...*bfftripmapv1.WorldTrip) *bfftripmapv1.WorldPlace {
	return &bfftripmapv1.WorldPlace{Id: id, Name: name, Country: country, LatE6: latE6, LngE6: lngE6, Trips: trips}
}

func worldTrip(id, date, note string) *bfftripmapv1.WorldTrip {
	return &bfftripmapv1.WorldTrip{Id: id, Date: date, Note: note}
}

func worldDoc(marks []*bfftripmapv1.WorldCountry, places ...*bfftripmapv1.WorldPlace) *bfftripmapv1.WorldDocument {
	return &bfftripmapv1.WorldDocument{Kind: "world", Version: 1, Countries: marks, Places: places}
}

// tokyoWorld is the acceptance sample: Japan marked, Tokyo with two trips.
func tokyoWorld() *bfftripmapv1.WorldDocument {
	return worldDoc(
		[]*bfftripmapv1.WorldCountry{worldMark("JP", "#719fc6")},
		worldPlace("p0mgx3k1y8c4f2a9z", "东京", "JP", 35_676_200, 139_650_300,
			worldTrip("t0mgx3k2a1b3c4d5e", "2026-04-01", "樱花季"), worldTrip("t0mgx3k2z9y8x7w6v", "", "")),
	)
}

// parisWorld has one city and a different mark, so it differs from tokyoWorld in every list.
func parisWorld() *bfftripmapv1.WorldDocument {
	return worldDoc(
		[]*bfftripmapv1.WorldCountry{worldMark("FR", "#4b937d")},
		worldPlace("p-paris", "巴黎", "FR", 48_856_600, 2_352_200, worldTrip("t-paris", "2026-06-01", "")),
	)
}

func replaceReq(base int64, id string, doc *bfftripmapv1.WorldDocument) *bfftripmapv1.ReplaceWorldRequest {
	return &bfftripmapv1.ReplaceWorldRequest{BaseRevision: base, RequestId: id, Document: doc}
}

func sameDoc(a, b *bfftripmapv1.WorldDocument) bool { return proto.Equal(a, b) }

func mustReplaceWorld(t *testing.T, b *Browser, base int64, id string, doc *bfftripmapv1.WorldDocument) *bfftripmapv1.ReplaceWorldResponse {
	t.Helper()
	res, err := b.ReplaceWorld(context.Background(), replaceReq(base, id, doc))
	if err != nil {
		t.Fatalf("ReplaceWorld(base %d, %s): %v", base, id, err)
	}
	return res
}

func registerWorldUser(t *testing.T, h *ChainHarness, name string) (*Browser, string) {
	t.Helper()
	b := h.FreshBrowser()
	id, err := b.Register(context.Background(), name, "password1", name)
	if err != nil {
		t.Fatal(err)
	}
	return b, id
}

func worldCounts(t *testing.T, h *ChainHarness, userID string) (docs, receipts int) {
	t.Helper()
	ctx := context.Background()
	return h.TripmapDB.WorldDocument.Query().Where(tripmapworldent.UserID(userID)).CountX(ctx),
		h.TripmapDB.WorldReceipt.Query().Where(tripmapworldreceipt.UserID(userID)).CountX(ctx)
}

func wantCodeOf(t *testing.T, what string, err error, want connect.Code) {
	t.Helper()
	if err == nil || connect.CodeOf(err) != want {
		t.Fatalf("%s: want %v, got %v", what, want, err)
	}
}

// A new user reads the empty document at revision 0; a Cookie + CSRF write is applied, stored
// in canonical form (sorted, names trimmed), read back, and the revision grows by one per change.
func TestChainWorldReadWriteAndCanonicalForm(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	alice, aliceID := registerWorldUser(t, h, "world_alice")

	got, err := alice.GetWorld(ctx)
	if err != nil || got.GetRevision() != 0 || !sameDoc(got.GetDocument(), worldDoc(nil)) {
		t.Fatalf("new user: %v %v", got, err)
	}
	if docs, receipts := worldCounts(t, h, aliceID); docs != 0 || receipts != 0 {
		t.Fatalf("a read created rows: %d %d", docs, receipts)
	}

	// Unsorted, with padded names: the answer and the store are the canonical form.
	sent := worldDoc(
		[]*bfftripmapv1.WorldCountry{worldMark("US", "#e2af5c"), worldMark("JP", "#719fc6")},
		worldPlace("pb", "  Berlin\u3000", "DE", 52_520_000, 13_405_000, worldTrip("tz", "", ""), worldTrip("ta", "2026-01-02", "n")),
		worldPlace("pa", "A", "US", 40_000_000, -74_000_000),
	)
	want := worldDoc(
		[]*bfftripmapv1.WorldCountry{worldMark("JP", "#719fc6"), worldMark("US", "#e2af5c")},
		worldPlace("pa", "A", "US", 40_000_000, -74_000_000),
		worldPlace("pb", "Berlin", "DE", 52_520_000, 13_405_000, worldTrip("ta", "2026-01-02", "n"), worldTrip("tz", "", "")),
	)
	res := mustReplaceWorld(t, alice, 0, "req-1", sent)
	if !res.GetApplied() || res.GetReplayed() || res.GetRevision() != 1 || res.GetCloudRevision() != 1 || !sameDoc(res.GetDocument(), want) {
		t.Fatalf("first write: %v", res)
	}
	got, err = alice.GetWorld(ctx)
	if err != nil || got.GetRevision() != 1 || !sameDoc(got.GetDocument(), want) {
		t.Fatalf("read back: %v %v", got, err)
	}

	// Revisions are monotonic: each changing write adds one.
	res = mustReplaceWorld(t, alice, 1, "req-2", tokyoWorld())
	if !res.GetApplied() || res.GetRevision() != 2 {
		t.Fatalf("second write: %v", res)
	}
	res = mustReplaceWorld(t, alice, 2, "req-3", worldDoc(nil))
	if !res.GetApplied() || res.GetRevision() != 3 || len(res.GetDocument().GetPlaces()) != 0 {
		t.Fatalf("clearing the world is a change too: %v", res)
	}
	if docs, receipts := worldCounts(t, h, aliceID); docs != 1 || receipts != 3 {
		t.Fatalf("rows: %d documents, %d receipts", docs, receipts)
	}
}

// Users never see each other's world; anonymous callers and writes without CSRF, with a
// foreign Origin or naming another account are refused by the BFF and change nothing.
func TestChainWorldIsolationAndWriteGuards(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	alice, aliceID := registerWorldUser(t, h, "world_iso_a")
	bob, bobID := registerWorldUser(t, h, "world_iso_b")

	mustReplaceWorld(t, alice, 0, "same-id", tokyoWorld())

	// B starts empty and may use the very same request_id: receipts belong to the user.
	got, err := bob.GetWorld(ctx)
	if err != nil || got.GetRevision() != 0 || len(got.GetDocument().GetPlaces()) != 0 {
		t.Fatalf("bob sees %v %v", got, err)
	}
	res := mustReplaceWorld(t, bob, 0, "same-id", parisWorld())
	if !res.GetApplied() || res.GetReplayed() || res.GetRevision() != 1 || !sameDoc(res.GetDocument(), parisWorld()) {
		t.Fatalf("bob's write with alice's request_id: %v", res)
	}
	if got, _ = alice.GetWorld(ctx); !sameDoc(got.GetDocument(), tokyoWorld()) || got.GetRevision() != 1 {
		t.Fatalf("alice's world changed: %v", got)
	}
	// B cannot write to A's world by guessing a revision: there is no way to name another user.
	conflict := mustReplaceWorld(t, bob, 0, "bob-2", tokyoWorld())
	if conflict.GetApplied() || !sameDoc(conflict.GetDocument(), parisWorld()) {
		t.Fatalf("a stale write by bob: %v", conflict)
	}

	// Guards. Rows are counted before and after; none may change.
	before := func() [4]int {
		ad, ar := worldCounts(t, h, aliceID)
		bd, br := worldCounts(t, h, bobID)
		return [4]int{ad, ar, bd, br}
	}
	snapshot := before()
	csrf := alice.refreshCSRF(ctx)
	send := func(mutate func(*connect.Request[bfftripmapv1.ReplaceWorldRequest])) error {
		req := connect.NewRequest(replaceReq(1, "guarded", parisWorld()))
		mutate(req)
		_, err := alice.World.ReplaceWorld(ctx, req)
		return err
	}
	wantCodeOf(t, "no CSRF token", send(func(r *connect.Request[bfftripmapv1.ReplaceWorldRequest]) { alice.writeHeaders(r, "", alice.Origin) }), connect.CodePermissionDenied)
	wantCodeOf(t, "a wrong CSRF token", send(func(r *connect.Request[bfftripmapv1.ReplaceWorldRequest]) {
		alice.writeHeaders(r, csrf+"x", alice.Origin)
	}), connect.CodePermissionDenied)
	wantCodeOf(t, "no Origin", send(func(r *connect.Request[bfftripmapv1.ReplaceWorldRequest]) { alice.writeHeaders(r, csrf, "") }), connect.CodePermissionDenied)
	wantCodeOf(t, "a foreign Origin", send(func(r *connect.Request[bfftripmapv1.ReplaceWorldRequest]) {
		alice.writeHeaders(r, csrf, "https://evil.example")
	}), connect.CodePermissionDenied)
	wantCodeOf(t, "another account's X-Kongken-User", send(func(r *connect.Request[bfftripmapv1.ReplaceWorldRequest]) {
		alice.writeHeaders(r, csrf, alice.Origin)
		r.Header().Set("X-Kongken-User", bobID)
	}), connect.CodeUnauthenticated)

	anon := h.FreshBrowser()
	_, err = anon.GetWorld(ctx)
	wantCodeOf(t, "anonymous GetWorld", err, connect.CodeUnauthenticated)
	anonReq := connect.NewRequest(replaceReq(0, "anon", tokyoWorld()))
	anon.writeHeaders(anonReq, anon.refreshCSRF(ctx), anon.Origin)
	if _, err = anon.World.ReplaceWorld(ctx, anonReq); err == nil || (connect.CodeOf(err) != connect.CodeUnauthenticated && connect.CodeOf(err) != connect.CodePermissionDenied) {
		t.Fatalf("anonymous ReplaceWorld: %v", err)
	}
	// An authenticated write without any headers of its own is refused as well.
	bare := connect.NewRequest(replaceReq(1, "bare", parisWorld()))
	_, err = alice.World.ReplaceWorld(ctx, bare)
	wantCodeOf(t, "a bare write", err, connect.CodePermissionDenied)

	if after := before(); after != snapshot {
		t.Fatalf("a refused request changed rows: %v -> %v", snapshot, after)
	}
	if got, _ = alice.GetWorld(ctx); got.GetRevision() != 1 || !sameDoc(got.GetDocument(), tokyoWorld()) {
		t.Fatalf("alice's world after refused writes: %v", got)
	}
}

// Documents the rules refuse are invalid arguments, leave no document and no receipt, and do not
// use up the request_id.
func TestChainWorldRejectsInvalidDocuments(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	alice, aliceID := registerWorldUser(t, h, "world_invalid")

	with := func(mutate func(*bfftripmapv1.WorldDocument)) *bfftripmapv1.WorldDocument {
		d := proto.Clone(tokyoWorld()).(*bfftripmapv1.WorldDocument)
		mutate(d)
		return d
	}
	bigTrips := func(n int) []*bfftripmapv1.WorldTrip {
		out := make([]*bfftripmapv1.WorldTrip, n)
		for i := range out {
			out[i] = worldTrip(fmt.Sprintf("t%06d", i), "", "")
		}
		return out
	}
	manyPlaces := func(n int) []*bfftripmapv1.WorldPlace {
		out := make([]*bfftripmapv1.WorldPlace, n)
		for i := range out {
			out[i] = worldPlace(fmt.Sprintf("p%06d", i), "c", "JP", 1, 1)
		}
		return out
	}
	oversized := worldDoc(nil, worldPlace("p1", "x", "JP", 1, 1, func() []*bfftripmapv1.WorldTrip {
		var out []*bfftripmapv1.WorldTrip
		for i := 0; i < 1000; i++ { // 1000 notes of 3000 bytes: 3 MB of text, under every transport limit
			out = append(out, worldTrip(fmt.Sprintf("t%04d", i), "", strings.Repeat("山", 1000)))
		}
		return out
	}()...))
	tooManyTrips := worldDoc(nil, manyPlaces(2)...)
	tooManyTrips.Places[0].Trips = bigTrips(10_001)

	cases := map[string]*bfftripmapv1.WorldDocument{
		"nil document":            nil,
		"a date off the calendar": with(func(d *bfftripmapv1.WorldDocument) { d.Places[0].Trips[0].Date = "2026-02-30" }),
		"latitude out of range":   with(func(d *bfftripmapv1.WorldDocument) { d.Places[0].LatE6 = 90_000_001 }),
		"longitude out of range":  with(func(d *bfftripmapv1.WorldDocument) { d.Places[0].LngE6 = -180_000_001 }),
		"a duplicate city id": with(func(d *bfftripmapv1.WorldDocument) {
			d.Places = append(d.Places, proto.Clone(d.Places[0]).(*bfftripmapv1.WorldPlace))
		}),
		"a duplicate trip id":          with(func(d *bfftripmapv1.WorldDocument) { d.Places[0].Trips[1].Id = d.Places[0].Trips[0].Id }),
		"an unknown country":           with(func(d *bfftripmapv1.WorldDocument) { d.Places[0].Country = "ZZ" }),
		"CN as a mark":                 with(func(d *bfftripmapv1.WorldDocument) { d.Countries[0].Code = "CN" }),
		"Hong Kong":                    with(func(d *bfftripmapv1.WorldDocument) { d.Places[0].Country = "HK" }),
		"a colour outside the palette": with(func(d *bfftripmapv1.WorldDocument) { d.Countries[0].Color = "#000000" }),
		"an unknown version":           with(func(d *bfftripmapv1.WorldDocument) { d.Version = 2 }),
		"no version":                   with(func(d *bfftripmapv1.WorldDocument) { d.Version = 0 }),
		"another kind":                 with(func(d *bfftripmapv1.WorldDocument) { d.Kind = "china" }),
		"an empty name":                with(func(d *bfftripmapv1.WorldDocument) { d.Places[0].Name = " \u3000 " }),
		"a control character":          with(func(d *bfftripmapv1.WorldDocument) { d.Places[0].Name = "a\tb" }),
		"a bad city id":                with(func(d *bfftripmapv1.WorldDocument) { d.Places[0].Id = "a b" }),
		"a note of 1001 units":         with(func(d *bfftripmapv1.WorldDocument) { d.Places[0].Trips[0].Note = strings.Repeat("n", 1001) }),
		"5001 cities":                  worldDoc(nil, manyPlaces(5001)...),
		"10001 trips":                  tooManyTrips,
		"over 2,500,000 text bytes":    oversized,
	}
	for name, doc := range cases {
		_, err := alice.ReplaceWorld(ctx, replaceReq(0, "bad", doc))
		wantCodeOf(t, name, err, connect.CodeInvalidArgument)
	}
	for name, req := range map[string]*bfftripmapv1.ReplaceWorldRequest{
		"an empty request_id":       replaceReq(0, "", tokyoWorld()),
		"a request_id with a dot":   replaceReq(0, "a.b", tokyoWorld()),
		"a 65 character request_id": replaceReq(0, strings.Repeat("r", 65), tokyoWorld()),
		"a negative base_revision":  replaceReq(-1, "neg", tokyoWorld()),
	} {
		_, err := alice.ReplaceWorld(ctx, req)
		wantCodeOf(t, name, err, connect.CodeInvalidArgument)
	}

	if docs, receipts := worldCounts(t, h, aliceID); docs != 0 || receipts != 0 {
		t.Fatalf("a rejected request stored something: %d documents, %d receipts", docs, receipts)
	}
	if got, err := alice.GetWorld(ctx); err != nil || got.GetRevision() != 0 || len(got.GetDocument().GetPlaces()) != 0 {
		t.Fatalf("the world after rejections: %v %v", got, err)
	}
	// "bad" was used by every rejected request above and is still free.
	if res := mustReplaceWorld(t, alice, 0, "bad", tokyoWorld()); !res.GetApplied() || res.GetReplayed() {
		t.Fatalf("a request_id used by rejected requests: %v", res)
	}
}

// The largest legal document (5000 cities, 10000 trips, 2,500,000 bytes of text) goes through the
// BFF and tripmap, is stored and read back.
func TestChainWorldCarriesTheLargestDocument(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	alice, _ := registerWorldUser(t, h, "world_big")

	places := make([]*bfftripmapv1.WorldPlace, 5000)
	total := len("JP") + len("#719fc6") // the one country mark
	for i := range places {
		id := fmt.Sprintf("p%05d", i)
		places[i] = worldPlace(id, "城市", "JP", int32(i)*1000, int32(i)*2000)
		total += len(id) + len("城市") + len("JP")
	}
	for i := 0; i < 10_000; i++ {
		id := fmt.Sprintf("t%05d", i)
		places[i%5000].Trips = append(places[i%5000].Trips, worldTrip(id, "2026-10-14", ""))
		total += len(id) + len("2026-10-14")
	}
	// Spend the rest of the 2,500,000 bytes on notes of up to 1000 characters (3 bytes each).
	budget := 2_500_000 - total
	for i := 0; budget > 0 && i < 10_000; i++ {
		trip := places[i%5000].Trips[i/5000]
		n := min(budget/3, 1000)
		trip.Note = strings.Repeat("山", n)
		budget -= n * 3
		if budget < 3 { // the last one or two bytes of the budget are ASCII
			trip.Note += strings.Repeat("x", budget)
			budget = 0
		}
	}
	doc := worldDoc([]*bfftripmapv1.WorldCountry{worldMark("JP", "#719fc6")}, places...)
	res := mustReplaceWorld(t, alice, 0, "big-1", doc)
	if !res.GetApplied() || res.GetRevision() != 1 {
		t.Fatalf("largest document: %v", res.GetRevision())
	}
	got, err := alice.GetWorld(ctx)
	if err != nil || got.GetRevision() != 1 || len(got.GetDocument().GetPlaces()) != 5000 || !sameDoc(got.GetDocument(), res.GetDocument()) {
		t.Fatalf("read back the largest document: %v", err)
	}
	// One byte more is refused.
	doc.Places[0].Name += "x"
	if _, err = alice.ReplaceWorld(ctx, replaceReq(1, "big-2", doc)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("one byte over: %v", err)
	}
}

// A stale base_revision is a conflict, not an error: the cloud's revision and document come back,
// nothing is written and no receipt is kept, so the same request_id is still free.
func TestChainWorldConflictLeavesCloudAndReceiptsAlone(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	alice, aliceID := registerWorldUser(t, h, "world_conflict")

	mustReplaceWorld(t, alice, 0, "c-1", tokyoWorld()) // revision 1
	mustReplaceWorld(t, alice, 1, "c-2", parisWorld()) // revision 2 (another device)
	docs, receipts := worldCounts(t, h, aliceID)

	res := mustReplaceWorld(t, alice, 1, "c-3", worldDoc(nil)) // based on revision 1: stale
	if res.GetApplied() || res.GetReplayed() || res.GetRevision() != 2 || res.GetCloudRevision() != 2 || !sameDoc(res.GetDocument(), parisWorld()) {
		t.Fatalf("conflict: %v", res)
	}
	if d, r := worldCounts(t, h, aliceID); d != docs || r != receipts {
		t.Fatalf("a conflict wrote: %d/%d -> %d/%d", docs, receipts, d, r)
	}
	if got, _ := alice.GetWorld(ctx); got.GetRevision() != 2 || !sameDoc(got.GetDocument(), parisWorld()) {
		t.Fatalf("the cloud moved: %v", got)
	}
	// A base ahead of the cloud is no different.
	if res = mustReplaceWorld(t, alice, 9, "c-4", worldDoc(nil)); res.GetApplied() || res.GetRevision() != 2 {
		t.Fatalf("a base from the future: %v", res)
	}
	// "c-3" left no receipt, so it is free to be sent again with the right base: it is a new
	// write, neither a replay nor ALREADY_EXISTS.
	res = mustReplaceWorld(t, alice, 2, "c-3", worldDoc(nil))
	if !res.GetApplied() || res.GetReplayed() || res.GetRevision() != 3 {
		t.Fatalf("the request_id of a conflict: %v", res)
	}
}

// Scenario K: a lost response is replayed with the same request_id and answers applied = true even
// though another device has written since; the same id for different content is ALREADY_EXISTS;
// an unchanged document at the right revision is a success with a receipt of its own.
func TestChainWorldReplayAndRequestIDReuse(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	alice, aliceID := registerWorldUser(t, h, "world_replay")

	first := mustReplaceWorld(t, alice, 0, "r-1", tokyoWorld())
	if !first.GetApplied() || first.GetRevision() != 1 {
		t.Fatalf("first: %v", first)
	}
	// The response was lost; meanwhile another device writes revision 2 and 3.
	mustReplaceWorld(t, alice, 1, "other-1", parisWorld())
	mustReplaceWorld(t, alice, 2, "other-2", worldDoc([]*bfftripmapv1.WorldCountry{worldMark("US", "#e2af5c")}))
	docs, receipts := worldCounts(t, h, aliceID)

	replay := mustReplaceWorld(t, alice, 0, "r-1", tokyoWorld())
	if !replay.GetApplied() || !replay.GetReplayed() || replay.GetRevision() != 1 || replay.GetCloudRevision() != 3 || !sameDoc(replay.GetDocument(), tokyoWorld()) {
		t.Fatalf("replay: %v", replay)
	}
	if d, r := worldCounts(t, h, aliceID); d != docs || r != receipts {
		t.Fatalf("a replay wrote: %d/%d -> %d/%d", docs, receipts, d, r)
	}
	if got, _ := alice.GetWorld(ctx); got.GetRevision() != 3 || len(got.GetDocument().GetPlaces()) != 0 {
		t.Fatalf("a replay moved the cloud: %v", got)
	}
	// Replaying twice is as good as once.
	if again := mustReplaceWorld(t, alice, 0, "r-1", tokyoWorld()); !again.GetReplayed() || again.GetRevision() != 1 {
		t.Fatalf("second replay: %v", again)
	}

	// The same request_id never means another request. Nothing changes, whatever the cloud holds.
	_, err := alice.ReplaceWorld(ctx, replaceReq(0, "r-1", parisWorld()))
	wantCodeOf(t, "same id, other document", err, connect.CodeAlreadyExists)
	_, err = alice.ReplaceWorld(ctx, replaceReq(1, "r-1", tokyoWorld()))
	wantCodeOf(t, "same id and document, other base_revision", err, connect.CodeAlreadyExists)
	_, err = alice.ReplaceWorld(ctx, replaceReq(3, "r-1", tokyoWorld()))
	wantCodeOf(t, "same id and document, the current base_revision", err, connect.CodeAlreadyExists)
	if d, r := worldCounts(t, h, aliceID); d != docs || r != receipts {
		t.Fatalf("a refused reuse wrote: %d/%d -> %d/%d", docs, receipts, d, r)
	}

	// The digest is over the canonical form: the same document in another order and with padded
	// names is the same request.
	reshuffled := tokyoWorld()
	reshuffled.Places[0].Name = "  东京 "
	reshuffled.Places[0].Trips[0], reshuffled.Places[0].Trips[1] = reshuffled.Places[0].Trips[1], reshuffled.Places[0].Trips[0]
	if res := mustReplaceWorld(t, alice, 0, "r-1", reshuffled); !res.GetReplayed() || res.GetRevision() != 1 || !sameDoc(res.GetDocument(), tokyoWorld()) {
		t.Fatalf("replay of the same canonical document: %v", res)
	}

	// An unchanged document at the current revision succeeds without a new revision, and its
	// receipt replays like any other.
	current, _ := alice.GetWorld(ctx)
	noop := mustReplaceWorld(t, alice, current.GetRevision(), "noop-1", current.GetDocument())
	if !noop.GetApplied() || noop.GetReplayed() || noop.GetRevision() != 3 || noop.GetCloudRevision() != 3 {
		t.Fatalf("no-op write: %v", noop)
	}
	if _, r := worldCounts(t, h, aliceID); r != receipts+1 {
		t.Fatalf("a no-op leaves a receipt: %d -> %d", receipts, r)
	}
	mustReplaceWorld(t, alice, 3, "next-1", tokyoWorld()) // revision 4
	if again := mustReplaceWorld(t, alice, 3, "noop-1", current.GetDocument()); !again.GetReplayed() || again.GetRevision() != 3 || again.GetCloudRevision() != 4 {
		t.Fatalf("no-op replay: %v", again)
	}
}

// Scenario K: only the last 32 receipts are kept. A replay after eviction is an ordinary stale
// request: a conflict that carries the cloud's document, and never a second write.
func TestChainWorldReceiptsAreCappedAt32(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	alice, aliceID := registerWorldUser(t, h, "world_evict")

	// Request n writes tokyo for odd n and paris for even n: 33 writes, revisions 1..33.
	docFor := func(n int) *bfftripmapv1.WorldDocument {
		if n%2 == 1 {
			return tokyoWorld()
		}
		return parisWorld()
	}
	for n := 1; n <= 33; n++ {
		if res := mustReplaceWorld(t, alice, int64(n-1), fmt.Sprintf("e-%02d", n), docFor(n)); !res.GetApplied() || res.GetRevision() != int64(n) {
			t.Fatalf("write %d: %v", n, res)
		}
	}
	if _, receipts := worldCounts(t, h, aliceID); receipts != 32 {
		t.Fatalf("%d receipts kept, want 32", receipts)
	}

	// e-02 is the oldest receipt left: it still replays.
	if res := mustReplaceWorld(t, alice, 1, "e-02", parisWorld()); !res.GetApplied() || !res.GetReplayed() || res.GetRevision() != 2 || res.GetCloudRevision() != 33 {
		t.Fatalf("the oldest kept receipt: %v", res)
	}
	// e-01 is gone. Its request was at base 0 and wrote tokyo, which is what the cloud holds again
	// (revision 33), so a client may treat the answer as a silent success - but the server only says
	// "conflict" and writes nothing.
	res := mustReplaceWorld(t, alice, 0, "e-01", tokyoWorld())
	if res.GetApplied() || res.GetReplayed() || res.GetRevision() != 33 || res.GetCloudRevision() != 33 || !sameDoc(res.GetDocument(), tokyoWorld()) {
		t.Fatalf("an evicted replay: %v", res)
	}
	if got, _ := alice.GetWorld(ctx); got.GetRevision() != 33 {
		t.Fatalf("an evicted replay wrote: revision %d", got.GetRevision())
	}
	if _, receipts := worldCounts(t, h, aliceID); receipts != 32 {
		t.Fatalf("the conflict kept a receipt: %d", receipts)
	}
	// An evicted id with different content is not ALREADY_EXISTS any more either: it is just stale.
	if res = mustReplaceWorld(t, alice, 0, "e-01", parisWorld()); res.GetApplied() {
		t.Fatalf("an evicted id with other content: %v", res)
	}
}

// Scenario K: concurrent requests on the same base_revision - one wins, the rest are conflicts.
// Both for the first creation (revision 0, where the per-user unique row is what is raced) and
// for an update; none may fail with an error.
func TestChainWorldConcurrentWritersSettleOnOne(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()

	race := func(name string, base int64, who *Browser, prefix string) *bfftripmapv1.ReplaceWorldResponse {
		t.Helper()
		const writers = 8
		csrf := who.refreshCSRF(ctx)
		results := make([]*bfftripmapv1.ReplaceWorldResponse, writers)
		errs := make([]error, writers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				doc := worldDoc(nil, worldPlace(fmt.Sprintf("p-%s-%d", prefix, i), fmt.Sprintf("writer %d", i), "JP", 1, int32(i)))
				req := connect.NewRequest(replaceReq(base, fmt.Sprintf("%s-%d", prefix, i), doc))
				who.writeHeaders(req, csrf, who.Origin)
				<-start
				res, err := who.World.ReplaceWorld(ctx, req)
				if err == nil {
					results[i] = res.Msg
				}
				errs[i] = err
			}()
		}
		close(start)
		wg.Wait()
		var winner *bfftripmapv1.ReplaceWorldResponse
		for i := range results {
			if errs[i] != nil {
				t.Fatalf("%s: writer %d failed: %v", name, i, errs[i])
			}
			if results[i].GetApplied() {
				if winner != nil {
					t.Fatalf("%s: two writers succeeded: %v and %v", name, winner, results[i])
				}
				winner = results[i]
				continue
			}
			if results[i].GetRevision() != base+1 {
				t.Fatalf("%s: a conflict reports revision %d, want %d", name, results[i].GetRevision(), base+1)
			}
		}
		if winner == nil || winner.GetRevision() != base+1 {
			t.Fatalf("%s: no single winner: %v", name, winner)
		}
		for i := range results {
			if !results[i].GetApplied() && !sameDoc(results[i].GetDocument(), winner.GetDocument()) {
				t.Fatalf("%s: a loser was told another document than the winner's", name)
			}
		}
		return winner
	}

	for round := 0; round < 3; round++ {
		alice, aliceID := registerWorldUser(t, h, fmt.Sprintf("world_race_%d", round))
		first := race("first creation", 0, alice, "a")
		second := race("update", 1, alice, "b")
		if second.GetRevision() != 2 {
			t.Fatalf("update winner: %v", second)
		}
		if docs, receipts := worldCounts(t, h, aliceID); docs != 1 || receipts != 2 {
			t.Fatalf("rows after the races: %d documents, %d receipts (first %v)", docs, receipts, first.GetRevision())
		}
		got, err := alice.GetWorld(ctx)
		if err != nil || got.GetRevision() != 2 || !sameDoc(got.GetDocument(), second.GetDocument()) {
			t.Fatalf("the cloud after the races: %v %v", got, err)
		}
	}

	// The same request sent twice at once is one write and one replay.
	alice, _ := registerWorldUser(t, h, "world_race_same")
	csrf := alice.refreshCSRF(ctx)
	var wg sync.WaitGroup
	out := make([]*bfftripmapv1.ReplaceWorldResponse, 4)
	errs := make([]error, 4)
	start := make(chan struct{})
	for i := range out {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := connect.NewRequest(replaceReq(0, "twin", tokyoWorld()))
			alice.writeHeaders(req, csrf, alice.Origin)
			<-start
			res, err := alice.World.ReplaceWorld(ctx, req)
			if err == nil {
				out[i] = res.Msg
			}
			errs[i] = err
		}()
	}
	close(start)
	wg.Wait()
	fresh, replays := 0, 0
	for i := range out {
		if errs[i] != nil || !out[i].GetApplied() || out[i].GetRevision() != 1 {
			t.Fatalf("twin %d: %v %v", i, out[i], errs[i])
		}
		if out[i].GetReplayed() {
			replays++
		} else {
			fresh++
		}
	}
	if fresh != 1 || replays != 3 {
		t.Fatalf("%d writes and %d replays, want 1 and 3", fresh, replays)
	}
}

// Account deletion removes the user's world document and receipts and nobody else's; a new account on
// the same address starts empty.
func TestChainWorldAccountDeletion(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	alice, aliceID := registerWorldUser(t, h, "world_del_a")
	bob, bobID := registerWorldUser(t, h, "world_del_b")

	mustReplaceWorld(t, alice, 0, "d-1", tokyoWorld())
	mustReplaceWorld(t, alice, 1, "d-2", parisWorld())
	mustReplaceWorld(t, bob, 0, "d-1", tokyoWorld())
	if d, r := worldCounts(t, h, aliceID); d != 1 || r != 2 {
		t.Fatalf("alice before: %d/%d", d, r)
	}

	if err := alice.DeleteAccount(ctx, &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "world-delete"}, aliceID); err != nil {
		t.Fatal(err)
	}
	if d, r := worldCounts(t, h, aliceID); d != 0 || r != 0 {
		t.Fatalf("alice's world survived her account: %d documents, %d receipts", d, r)
	}
	if d, r := worldCounts(t, h, bobID); d != 1 || r != 1 {
		t.Fatalf("bob's world was touched: %d/%d", d, r)
	}
	if got, err := bob.GetWorld(ctx); err != nil || got.GetRevision() != 1 || !sameDoc(got.GetDocument(), tokyoWorld()) {
		t.Fatalf("bob after alice's deletion: %v %v", got, err)
	}
	if _, err := alice.GetWorld(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a deleted account reads a world: %v", err)
	}

	again := h.FreshBrowser()
	newID, err := again.Register(ctx, "world_del_a", "password1", "again")
	if err != nil || newID == aliceID {
		t.Fatalf("re-registration: %q %v", newID, err)
	}
	got, err := again.GetWorld(ctx)
	if err != nil || got.GetRevision() != 0 || len(got.GetDocument().GetPlaces()) != 0 {
		t.Fatalf("the new account's world: %v %v", got, err)
	}
	// The old receipts went with the account: the first request_id is free again.
	if res := mustReplaceWorld(t, again, 0, "d-1", parisWorld()); !res.GetApplied() || res.GetReplayed() || res.GetRevision() != 1 {
		t.Fatalf("a request_id of the deleted account: %v", res)
	}
}
