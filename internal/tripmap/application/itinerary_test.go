package application_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"go.orx.me/apps/trip-map/internal/tripmap/ent/itinerary"
	"go.orx.me/apps/trip-map/internal/tripmap/ent/schema"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
)

func TestItineraryServiceCRUDReorderingCacheAndIsolation(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	_, err := e.itinerary.ListItineraries(ctx, connect.NewRequest(&tripmapv1.ListItinerariesRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)

	alice := e.register("alice_f11")
	bob := e.register("bob_f11")

	// Reject < 2 stops.
	_, err = e.itinerary.SaveItinerary(ctx, bearer(alice, &tripmapv1.SaveItineraryRequest{
		Id:    "it-too-short",
		Title: "单站行程",
		Color: "#4b937d",
		Stops: []*tripmapv1.ItineraryStopInput{
			{Id: "s1", RegionCode: "310101", Transport: "train"},
		},
	}))
	wantCode(t, err, connect.CodeInvalidArgument)

	// Create itinerary with 3 stops.
	createReq := &tripmapv1.SaveItineraryRequest{
		Id:         "it-jiangnan",
		Title:      "江南与海西春行",
		Note:       "上海-杭州-厦门连线",
		Color:      "#4b937d",
		MutationId: "mut-it-1",
		Stops: []*tripmapv1.ItineraryStopInput{
			{Id: "s1", RegionCode: "310101", Date: "2026-04-01", Note: "外滩出发", Transport: "train"},
			{Id: "s2", RegionCode: "330100", Date: "2026-04-02", Note: "西湖漫步", Transport: "train"},
			{Id: "s3", RegionCode: "350200", Date: "2026-04-04", Note: "鼓浪屿环岛", Transport: "car"},
		},
	}
	saved, err := e.itinerary.SaveItinerary(ctx, bearer(alice, createReq))
	if err != nil {
		t.Fatalf("SaveItinerary create: %v", err)
	}
	if saved.Msg.Replayed {
		t.Fatalf("expected initial save not to be replayed")
	}
	info := saved.Msg.GetItinerary()
	if info.GetId() != "it-jiangnan" || info.GetVersion() != 1 || len(info.GetStops()) != 3 || len(info.GetLegs()) != 2 {
		t.Fatalf("unexpected created itinerary: %+v", info)
	}
	if saved.Msg.GetPolicy().GetProvider() != "offline_great_circle" || saved.Msg.GetPolicy().GetExternalApiEnabled() {
		t.Fatalf("unexpected policy: %+v", saved.Msg.GetPolicy())
	}

	// Idempotent replay with same mutation_id returns replayed=true and same version.
	replayed, err := e.itinerary.SaveItinerary(ctx, bearer(alice, createReq))
	if err != nil {
		t.Fatalf("SaveItinerary replay: %v", err)
	}
	if !replayed.Msg.GetReplayed() || replayed.Msg.GetItinerary().GetVersion() != 1 {
		t.Fatalf("expected replayed version 1, got %+v", replayed.Msg)
	}

	// Reusing mutation_id with different title is rejected.
	conflictMut := &tripmapv1.SaveItineraryRequest{
		Id:         "it-jiangnan",
		Title:      "篡改标题",
		Color:      "#4b937d",
		MutationId: "mut-it-1",
		Stops:      createReq.Stops,
	}
	_, err = e.itinerary.SaveItinerary(ctx, bearer(alice, conflictMut))
	wantCode(t, err, connect.CodeAlreadyExists)

	// Subsequent GetItinerary hits Redis leg cache (FromCache = true).
	fetched, err := e.itinerary.GetItinerary(ctx, bearer(alice, &tripmapv1.GetItineraryRequest{Id: "it-jiangnan"}))
	if err != nil {
		t.Fatalf("GetItinerary: %v", err)
	}
	if !fetched.Msg.GetItinerary().GetFromCache() {
		t.Fatalf("expected legs to be served from Redis leg cache on second read")
	}

	// Reorder stops (move Xiamen before Hangzhou) with expected_version=1.
	reordered, err := e.itinerary.SaveItinerary(ctx, bearer(alice, &tripmapv1.SaveItineraryRequest{
		Id:              "it-jiangnan",
		Title:           "江南与海西春行（调整顺序）",
		Note:            "先去厦门再去杭州",
		Color:           "#719fc6",
		ExpectedVersion: 1,
		MutationId:      "mut-it-2",
		Stops: []*tripmapv1.ItineraryStopInput{
			{Id: "s1", RegionCode: "310101", Date: "2026-04-01", Note: "外滩出发", Transport: "train"},
			{Id: "s3", RegionCode: "350200", Date: "2026-04-02", Note: "直飞厦门", Transport: "flight"},
			{Id: "s2", RegionCode: "330100", Date: "2026-04-04", Note: "北上杭州", Transport: "train"},
		},
	}))
	if err != nil {
		t.Fatalf("SaveItinerary reorder: %v", err)
	}
	if reordered.Msg.GetItinerary().GetVersion() != 2 {
		t.Fatalf("expected version 2, got %d", reordered.Msg.GetItinerary().GetVersion())
	}
	if reordered.Msg.GetItinerary().GetLegs()[0].GetToRegionCode() != "350200" ||
		reordered.Msg.GetItinerary().GetLegs()[1].GetToRegionCode() != "330100" {
		t.Fatalf("unexpected reordered legs: %+v", reordered.Msg.GetItinerary().GetLegs())
	}

	// Stale expected_version=1 is rejected with CodeAborted.
	_, err = e.itinerary.SaveItinerary(ctx, bearer(alice, &tripmapv1.SaveItineraryRequest{
		Id:              "it-jiangnan",
		Title:           "过期版本写入",
		Color:           "#4b937d",
		ExpectedVersion: 1,
		Stops:           createReq.Stops,
	}))
	wantCode(t, err, connect.CodeAborted)

	// User isolation: Bob cannot see, get, or delete Alice's itinerary.
	bobList, err := e.itinerary.ListItineraries(ctx, bearer(bob, &tripmapv1.ListItinerariesRequest{}))
	if err != nil {
		t.Fatalf("bob ListItineraries: %v", err)
	}
	if len(bobList.Msg.GetItineraries()) != 0 {
		t.Fatalf("expected bob list to be empty, got %d", len(bobList.Msg.GetItineraries()))
	}
	_, err = e.itinerary.GetItinerary(ctx, bearer(bob, &tripmapv1.GetItineraryRequest{Id: "it-jiangnan"}))
	wantCode(t, err, connect.CodeNotFound)

	delBob, err := e.itinerary.DeleteItinerary(ctx, bearer(bob, &tripmapv1.DeleteItineraryRequest{Id: "it-jiangnan"}))
	if err != nil {
		t.Fatalf("bob DeleteItinerary: %v", err)
	}
	if delBob.Msg.GetDeleted() {
		t.Fatalf("bob should not be able to delete alice's itinerary")
	}

	// Alice deletes her itinerary.
	delAlice, err := e.itinerary.DeleteItinerary(ctx, bearer(alice, &tripmapv1.DeleteItineraryRequest{
		Id:              "it-jiangnan",
		ExpectedVersion: 2,
	}))
	if err != nil || !delAlice.Msg.GetDeleted() {
		t.Fatalf("alice DeleteItinerary failed: err=%v", err)
	}
	aliceList, err := e.itinerary.ListItineraries(ctx, bearer(alice, &tripmapv1.ListItinerariesRequest{}))
	if err != nil || len(aliceList.Msg.GetItineraries()) != 0 {
		t.Fatalf("expected empty list after delete, got %+v (err=%v)", aliceList, err)
	}
}

func TestItineraryHistoricalRegionUpgradeAndAccountDeletion(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	idx := retiredIndex(t)
	regionClient, _, _ := serveRegionIndex(t, e, idx)

	token, u := regionUser(t, e, "alice_f11_upgrade")

	// Insert an itinerary containing a retired historical region code ("old-hangzhou" -> "330100").
	_, err := e.store.DB.Itinerary.Create().
		SetUserID(u.ID).
		SetItineraryID("it-hangzhou-retired").
		SetTitle("浙北历史区划行程").
		SetNote("杭州历史编码升级验证").
		SetColor("#4b937d").
		SetVersion(1).
		SetStops([]schema.ItineraryStopRecord{
			{ID: "s1", RegionCode: "310101", Date: "2026-03-01", Note: "上海", Transport: "train"},
			{ID: "s2", RegionCode: "old-hangzhou", Date: "2026-03-02", Note: "旧杭州编码", Transport: "train"},
		}).
		Save(ctx)
	if err != nil {
		t.Fatalf("seed historical itinerary: %v", err)
	}

	preview, err := regionClient.PreviewRegionUpgrade(ctx, regionReq(token, &tripmapv1.PreviewRegionUpgradeRequest{}))
	if err != nil {
		t.Fatalf("PreviewRegionUpgrade: %v", err)
	}
	if len(preview.Msg.GetChanges()) < 1 {
		t.Fatalf("expected at least 1 change in preview, got %+v", preview.Msg)
	}

	applied, err := regionClient.ApplyRegionUpgrade(ctx, regionReq(token, &tripmapv1.ApplyRegionUpgradeRequest{
		Version:      preview.Msg.GetVersion(),
		BaseRevision: preview.Msg.GetRevision(),
		MutationId:   "upgrade-itinerary-f11",
	}))
	if err != nil || applied.Msg.GetChangedCount() < 1 {
		t.Fatalf("ApplyRegionUpgrade: err=%v, msg=%+v", err, applied)
	}

	row, err := e.store.DB.Itinerary.Query().
		Where(itinerary.UserID(u.ID), itinerary.ItineraryID("it-hangzhou-retired")).
		Only(ctx)
	if err != nil {
		t.Fatalf("query upgraded itinerary: %v", err)
	}
	if len(row.Stops) != 2 || row.Stops[1].RegionCode != "330100" {
		t.Fatalf("expected stop[1] upgraded to 330100, got %+v", row.Stops)
	}

	// Deleting the account removes all itineraries for this user.
	_, err = e.auth.DeleteAccount(ctx, bearer(token, &tripmapv1.DeleteAccountRequest{
		Confirmation: "DELETE",
		Password:     "correct horse battery",
	}))
	if err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	remaining, err := e.store.DB.Itinerary.Query().Where(itinerary.UserID(u.ID)).Count(ctx)
	if err != nil || remaining != 0 {
		t.Fatalf("expected 0 itineraries after account deletion, got %d (err=%v)", remaining, err)
	}
}
