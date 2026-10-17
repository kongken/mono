package application_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

func uploadTripPhoto(t *testing.T, e *env, token, regionCode, tripID, filename string) string {
	t.Helper()
	ctx := context.Background()
	data := photo(t)
	res, err := e.media.PrepareUpload(ctx, bearer(token, &tripmapv1.PrepareUploadRequest{
		RegionCode:  regionCode,
		TripId:      tripID,
		Filename:    filename,
		ContentType: "image/png",
		SizeBytes:   int64(len(data)),
		UploadId:    uuid.NewString(),
	}))
	if err != nil {
		t.Fatalf("uploadTripPhoto(%s, %s): %v", regionCode, tripID, err)
	}
	id := res.Msg.Media.Id
	m := e.store.DB.MediaObject.GetX(ctx, id)
	if err = e.store.Blobs.Put(ctx, m.UploadKey, bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.media.CompleteUpload(ctx, bearer(token, &tripmapv1.CompleteUploadRequest{Id: id})); err != nil {
		t.Fatalf("CompleteUpload(%s): %v", id, err)
	}
	return id
}

func TestFriendshipLifecycleAndPermissions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// Unauthenticated requests are rejected.
	_, err := e.collab.GetCollaborationOverview(ctx, connect.NewRequest(&tripmapv1.GetCollaborationOverviewRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)

	aID := e.register("Alice_F7")
	bID := e.register("Bob_F7")

	ovA, err := e.collab.GetCollaborationOverview(ctx, bearer(aID, &tripmapv1.GetCollaborationOverviewRequest{}))
	if err != nil {
		t.Fatalf("overview A: %v", err)
	}
	ovB, err := e.collab.GetCollaborationOverview(ctx, bearer(bID, &tripmapv1.GetCollaborationOverviewRequest{}))
	if err != nil {
		t.Fatalf("overview B: %v", err)
	}
	if ovA.Msg.MyFriendCode == "" || ovB.Msg.MyFriendCode == "" || ovA.Msg.MyFriendCode == ovB.Msg.MyFriendCode {
		t.Fatalf("unexpected friend codes: %q vs %q", ovA.Msg.MyFriendCode, ovB.Msg.MyFriendCode)
	}
	if !ovA.Msg.FriendSettings.ShowCities || ovA.Msg.FriendSettings.ShowDates || ovA.Msg.FriendSettings.ShowNotes {
		t.Fatalf("unexpected default friend settings: %+v", ovA.Msg.FriendSettings)
	}

	// Self-request and missing target are rejected.
	_, err = e.collab.SendFriendRequest(ctx, bearer(aID, &tripmapv1.SendFriendRequestRequest{Target: "alice_f7"}))
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = e.collab.SendFriendRequest(ctx, bearer(aID, &tripmapv1.SendFriendRequestRequest{Target: ovA.Msg.MyFriendCode}))
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = e.collab.SendFriendRequest(ctx, bearer(aID, &tripmapv1.SendFriendRequestRequest{Target: "nobody_such_user"}))
	wantCode(t, err, connect.CodeNotFound)

	// Send friend request by case-insensitive username.
	sendRes, err := e.collab.SendFriendRequest(ctx, bearer(aID, &tripmapv1.SendFriendRequestRequest{Target: "bob_f7"}))
	if err != nil {
		t.Fatalf("send friend request: %v", err)
	}
	frID := sendRes.Msg.Friendship.Id
	if sendRes.Msg.Friendship.Status != "pending" || sendRes.Msg.Friendship.Direction != "outgoing" || sendRes.Msg.Friendship.Friend.Id != bID {
		t.Fatalf("unexpected pending friendship on sender: %+v", sendRes.Msg.Friendship)
	}

	// While pending, B sees incoming, and neither side can read the other's footprints or invite to shared trips.
	ovB, err = e.collab.GetCollaborationOverview(ctx, bearer(bID, &tripmapv1.GetCollaborationOverviewRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(ovB.Msg.Friends) != 1 || ovB.Msg.Friends[0].Direction != "incoming" || ovB.Msg.Friends[0].Status != "pending" {
		t.Fatalf("unexpected B friends while pending: %+v", ovB.Msg.Friends)
	}
	_, err = e.collab.GetFriendFootprints(ctx, bearer(aID, &tripmapv1.GetFriendFootprintsRequest{FriendUserId: bID}))
	wantCode(t, err, connect.CodePermissionDenied)
	_, err = e.collab.GetFriendFootprints(ctx, bearer(bID, &tripmapv1.GetFriendFootprintsRequest{FriendUserId: aID}))
	wantCode(t, err, connect.CodePermissionDenied)

	// Requester A cannot accept their own outgoing request.
	_, err = e.collab.RespondFriendRequest(ctx, bearer(aID, &tripmapv1.RespondFriendRequestRequest{FriendshipId: frID, Accept: true}))
	wantCode(t, err, connect.CodePermissionDenied)

	// Addressee B declines first; request disappears.
	if _, err = e.collab.RespondFriendRequest(ctx, bearer(bID, &tripmapv1.RespondFriendRequestRequest{FriendshipId: frID, Accept: false})); err != nil {
		t.Fatalf("decline friend request: %v", err)
	}
	ovA, _ = e.collab.GetCollaborationOverview(ctx, bearer(aID, &tripmapv1.GetCollaborationOverviewRequest{}))
	if len(ovA.Msg.Friends) != 0 {
		t.Fatalf("expected 0 friends after decline, got %+v", ovA.Msg.Friends)
	}

	// Now A sends by B's friend invite code, and B accepts.
	sendRes, err = e.collab.SendFriendRequest(ctx, bearer(aID, &tripmapv1.SendFriendRequestRequest{Target: ovB.Msg.MyFriendCode}))
	if err != nil {
		t.Fatalf("send by friend code: %v", err)
	}
	frID = sendRes.Msg.Friendship.Id
	accRes, err := e.collab.RespondFriendRequest(ctx, bearer(bID, &tripmapv1.RespondFriendRequestRequest{FriendshipId: frID, Accept: true}))
	if err != nil {
		t.Fatalf("accept friend request: %v", err)
	}
	if accRes.Msg.Friendship.Status != "accepted" || accRes.Msg.Friendship.Direction != "accepted" {
		t.Fatalf("unexpected accepted friendship: %+v", accRes.Msg.Friendship)
	}

	// Both sides can now read each other's friend footprints.
	if _, err = e.collab.GetFriendFootprints(ctx, bearer(aID, &tripmapv1.GetFriendFootprintsRequest{FriendUserId: bID})); err != nil {
		t.Fatalf("GetFriendFootprints after accept: %v", err)
	}

	// Removing friend revokes access immediately for both sides.
	if _, err = e.collab.RemoveFriend(ctx, bearer(aID, &tripmapv1.RemoveFriendRequest{FriendshipId: frID})); err != nil {
		t.Fatalf("remove friend: %v", err)
	}
	_, err = e.collab.GetFriendFootprints(ctx, bearer(aID, &tripmapv1.GetFriendFootprintsRequest{FriendUserId: bID}))
	wantCode(t, err, connect.CodePermissionDenied)
	_, err = e.collab.GetFriendFootprints(ctx, bearer(bID, &tripmapv1.GetFriendFootprintsRequest{FriendUserId: aID}))
	wantCode(t, err, connect.CodePermissionDenied)
}

func TestFriendFootprintComparisonRespectsBothUsersPrivacySettings(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	aID := e.register("cmp_a")
	bID := e.register("cmp_b")

	// Seed A: Hangzhou (330100, Zhejiang 330000), Suzhou (320500, Jiangsu 320000), mark Yunnan (530000).
	e.mustSync2(aID, "seed-a", 0,
		upCity("330100", "杭州市", "330000", "#4b937d"),
		upTrip("330100", "ta1", "2026-05-01", "A的西湖笔记"),
		upCity("320500", "苏州市", "320000", "#719fc6"),
		upTrip("320500", "ta2", "2026-05-03", "A的苏州笔记"),
		&tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertMark{UpsertMark: &tripmapv1.ProvinceMark{Province: "530000", Color: "#e2af5c"}}},
	)
	// Seed B: Hangzhou (330100, Zhejiang 330000), Ningbo (330200, Zhejiang 330000), mark Sichuan (510000).
	e.mustSync2(bID, "seed-b", 0,
		upCity("330100", "杭州市", "330000", "#d88370"),
		upTrip("330100", "tb1", "2026-06-01", "B的西湖秘密"),
		upCity("330200", "宁波市", "330000", "#a08cbd"),
		upTrip("330200", "tb2", "2026-06-02", "B的宁波秘密"),
		&tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertMark{UpsertMark: &tripmapv1.ProvinceMark{Province: "510000", Color: "#e2af5c"}}},
	)

	sendRes, err := e.collab.SendFriendRequest(ctx, bearer(aID, &tripmapv1.SendFriendRequestRequest{Target: "cmp_b"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.collab.RespondFriendRequest(ctx, bearer(bID, &tripmapv1.RespondFriendRequestRequest{FriendshipId: sendRes.Msg.Friendship.Id, Accept: true})); err != nil {
		t.Fatal(err)
	}

	// Default: both have show_cities=true, show_dates=false, show_notes=false.
	res, err := e.collab.GetFriendFootprints(ctx, bearer(aID, &tripmapv1.GetFriendFootprintsRequest{FriendUserId: bID}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Msg.ContentJson, "杭州市") || strings.Contains(res.Msg.ContentJson, "2026-06-01") || strings.Contains(res.Msg.ContentJson, "B的西湖秘密") {
		t.Fatalf("default friend content leaked date/note or missed city: %s", res.Msg.ContentJson)
	}
	cmp := res.Msg.Comparison
	if !cmp.CityComparisonAvailable || len(cmp.CommonCities) != 1 || cmp.CommonCities[0].Code != "330100" || cmp.MyOnlyCityCount != 1 || cmp.FriendOnlyCityCount != 1 {
		t.Fatalf("unexpected default city comparison: %+v", cmp)
	}
	if strings.Join(cmp.CommonProvinces, ",") != "330000" || strings.Join(cmp.OnlyMeProvinces, ",") != "320000,530000" || strings.Join(cmp.OnlyFriendProvinces, ",") != "510000" {
		t.Fatalf("unexpected province comparison: %+v", cmp)
	}

	// B hides cities: neither B's cities nor city-level comparison may be exposed.
	if _, err = e.collab.UpdateFriendSettings(ctx, bearer(bID, &tripmapv1.UpdateFriendSettingsRequest{
		Settings: &tripmapv1.FriendShareSettings{ShowCities: false, ShowDates: true, ShowNotes: true},
	})); err != nil {
		t.Fatal(err)
	}
	res, err = e.collab.GetFriendFootprints(ctx, bearer(aID, &tripmapv1.GetFriendFootprintsRequest{FriendUserId: bID}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Msg.ContentJson, "杭州市") || strings.Contains(res.Msg.ContentJson, "330100") || strings.Contains(res.Msg.ContentJson, "B的西湖秘密") {
		t.Fatalf("cities leaked when show_cities=false: %s", res.Msg.ContentJson)
	}
	if res.Msg.Comparison.CityComparisonAvailable || len(res.Msg.Comparison.CommonCities) != 0 || res.Msg.Comparison.FriendOnlyCityCount != 0 {
		t.Fatalf("city comparison leaked when friend disabled show_cities: %+v", res.Msg.Comparison)
	}

	// B enables cities + dates (not notes), while A disables show_cities:
	// A can see B's cities and dates, NOT B's notes, and city comparison is disabled because A chose not to share cities.
	if _, err = e.collab.UpdateFriendSettings(ctx, bearer(bID, &tripmapv1.UpdateFriendSettingsRequest{
		Settings: &tripmapv1.FriendShareSettings{ShowCities: true, ShowDates: true, ShowNotes: false},
	})); err != nil {
		t.Fatal(err)
	}
	if _, err = e.collab.UpdateFriendSettings(ctx, bearer(aID, &tripmapv1.UpdateFriendSettingsRequest{
		Settings: &tripmapv1.FriendShareSettings{ShowCities: false},
	})); err != nil {
		t.Fatal(err)
	}
	res, err = e.collab.GetFriendFootprints(ctx, bearer(aID, &tripmapv1.GetFriendFootprintsRequest{FriendUserId: bID}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Msg.ContentJson, "2026-06-01") || strings.Contains(res.Msg.ContentJson, "B的西湖秘密") {
		t.Fatalf("expected date without note in friend content: %s", res.Msg.ContentJson)
	}
	if res.Msg.Comparison.CityComparisonAvailable || len(res.Msg.Comparison.CommonCities) != 0 {
		t.Fatalf("city comparison should require both users to enable show_cities: %+v", res.Msg.Comparison)
	}
}

func TestSharedTripInviteAcceptSyncAndLeavePreservesCityAndOtherRecords(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	aID := e.register("trip_a")
	bID := e.register("trip_b")
	cID := e.register("trip_c")

	// Make A friends with B and C.
	frAB, _ := e.collab.SendFriendRequest(ctx, bearer(aID, &tripmapv1.SendFriendRequestRequest{Target: "trip_b"}))
	_, _ = e.collab.RespondFriendRequest(ctx, bearer(bID, &tripmapv1.RespondFriendRequestRequest{FriendshipId: frAB.Msg.Friendship.Id, Accept: true}))
	frAC, _ := e.collab.SendFriendRequest(ctx, bearer(aID, &tripmapv1.SendFriendRequestRequest{Target: "trip_c"}))
	_, _ = e.collab.RespondFriendRequest(ctx, bearer(cID, &tripmapv1.RespondFriendRequestRequest{FriendshipId: frAC.Msg.Friendship.Id, Accept: true}))

	// A has Hangzhou with trip "t-westlake".
	e.mustSync2(aID, "a-init", 0,
		upCity("330100", "杭州市", "330000", "#4b937d"),
		upTrip("330100", "t-westlake", "2026-05-01", "初游西湖"),
	)
	// B already has Hangzhou with B's own color (#d88370) and B's own solo trip "t-b-solo".
	e.mustSync2(bID, "b-init", 0,
		upCity("330100", "杭州市", "330000", "#d88370"),
		upTrip("330100", "t-b-solo", "2025-01-01", "B独自出差"),
	)
	// C is still on schema 1 with a legacy city 330200 (Ningbo).
	e.sync(cID, "c-legacy", 0, &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertVisit{UpsertVisit: &tripmapv1.Visit{
		Code: "330200", Name: "宁波市", Province: "330000", Color: "#719fc6", Note: "老外滩",
	}}})

	// A invites B and C as editors.
	invB, err := e.collab.InviteToSharedTrip(ctx, bearer(aID, &tripmapv1.InviteToSharedTripRequest{
		RegionCode:   "330100",
		TripId:       "t-westlake",
		FriendUserId: bID,
		Role:         "editor",
	}))
	if err != nil {
		t.Fatalf("invite B: %v", err)
	}
	stID := invB.Msg.SharedTrip.Id
	if _, err = e.collab.InviteToSharedTrip(ctx, bearer(aID, &tripmapv1.InviteToSharedTripRequest{
		RegionCode:   "330100",
		TripId:       "t-westlake",
		FriendUserId: cID,
		Role:         "editor",
	})); err != nil {
		t.Fatalf("invite C: %v", err)
	}

	// Before accepting, B does not have t-westlake.
	gotB, _ := e.state2(bID)
	wantLines(t, gotB, "city:330100:#d88370", "trip:t-b-solo:330100:2025-01-01:B独自出差")

	// B accepts -> B sees t-westlake alongside t-b-solo, keeping B's city color.
	if _, err = e.collab.RespondSharedTripInvite(ctx, bearer(bID, &tripmapv1.RespondSharedTripInviteRequest{
		SharedTripId: stID,
		Accept:       true,
	})); err != nil {
		t.Fatalf("B accept: %v", err)
	}
	gotB, _ = e.state2(bID)
	wantLines(t, gotB,
		"city:330100:#d88370",
		"trip:t-b-solo:330100:2025-01-01:B独自出差",
		"trip:t-westlake:330100:2026-05-01:初游西湖",
	)

	// C accepts -> C is upgraded in place from schema 1, keeps legacy-320100, and gets city 330100 + trip t-westlake.
	if _, err = e.collab.RespondSharedTripInvite(ctx, bearer(cID, &tripmapv1.RespondSharedTripInviteRequest{
		SharedTripId: stID,
		Accept:       true,
	})); err != nil {
		t.Fatalf("C accept: %v", err)
	}
	gotC, _ := e.state2(cID)
	wantLines(t, gotC,
		"city:330100:#4b937d",
		"city:330200:#719fc6",
		"trip:legacy-330200:330200::老外滩",
		"trip:t-westlake:330100:2026-05-01:初游西湖",
	)

	// B (editor) updates the shared trip -> A, B, and C all see the updated date and note.
	if _, err = e.collab.UpdateSharedTrip(ctx, bearer(bID, &tripmapv1.UpdateSharedTripRequest{
		SharedTripId: stID,
		Date:         "2026-05-02",
		Note:         "三人共游西湖",
		MutationId:   "edit-1",
	})); err != nil {
		t.Fatalf("B update shared trip: %v", err)
	}
	gotA, _ := e.state2(aID)
	wantLines(t, gotA, "city:330100:#4b937d", "trip:t-westlake:330100:2026-05-02:三人共游西湖")
	gotB, _ = e.state2(bID)
	wantLines(t, gotB,
		"city:330100:#d88370",
		"trip:t-b-solo:330100:2025-01-01:B独自出差",
		"trip:t-westlake:330100:2026-05-02:三人共游西湖",
	)
	gotC, _ = e.state2(cID)
	wantLines(t, gotC,
		"city:330100:#4b937d",
		"city:330200:#719fc6",
		"trip:legacy-330200:330200::老外滩",
		"trip:t-westlake:330100:2026-05-02:三人共游西湖",
	)

	// B leaves the shared trip -> A and C keep t-westlake intact; B keeps city 330100 and t-b-solo;
	// C leaves too -> C still keeps city 330100 check-in even though t-westlake was C's only trip in 330100!
	if _, err = e.collab.LeaveSharedTrip(ctx, bearer(bID, &tripmapv1.LeaveSharedTripRequest{SharedTripId: stID})); err != nil {
		t.Fatalf("B leave: %v", err)
	}
	gotA, _ = e.state2(aID)
	wantLines(t, gotA, "city:330100:#4b937d", "trip:t-westlake:330100:2026-05-02:三人共游西湖")
	gotB, _ = e.state2(bID)
	wantLines(t, gotB, "city:330100:#d88370", "trip:t-b-solo:330100:2025-01-01:B独自出差")

	if _, err = e.collab.LeaveSharedTrip(ctx, bearer(cID, &tripmapv1.LeaveSharedTripRequest{SharedTripId: stID})); err != nil {
		t.Fatalf("C leave: %v", err)
	}
	gotC, _ = e.state2(cID)
	wantLines(t, gotC,
		"city:330100:#4b937d",
		"city:330200:#719fc6",
		"trip:legacy-330200:330200::老外滩",
	)

	// After leaving, B cannot edit the shared trip anymore.
	_, err = e.collab.UpdateSharedTrip(ctx, bearer(bID, &tripmapv1.UpdateSharedTripRequest{
		SharedTripId: stID,
		Date:         "2026-05-09",
		Note:         "越权修改",
	}))
	wantCode(t, err, connect.CodePermissionDenied)
}

func TestSharedTripMemberRolesRemoveAndOwnerDissolve(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.store.Blobs = &mediaMemory{files: map[string][]byte{}}
	aID := e.register("role_a")
	bID := e.register("role_b")
	cID := e.register("role_c")

	frAB, _ := e.collab.SendFriendRequest(ctx, bearer(aID, &tripmapv1.SendFriendRequestRequest{Target: "role_b"}))
	_, _ = e.collab.RespondFriendRequest(ctx, bearer(bID, &tripmapv1.RespondFriendRequestRequest{FriendshipId: frAB.Msg.Friendship.Id, Accept: true}))
	frAC, _ := e.collab.SendFriendRequest(ctx, bearer(aID, &tripmapv1.SendFriendRequestRequest{Target: "role_c"}))
	_, _ = e.collab.RespondFriendRequest(ctx, bearer(cID, &tripmapv1.RespondFriendRequestRequest{FriendshipId: frAC.Msg.Friendship.Id, Accept: true}))

	e.mustSync2(aID, "a-seed", 0,
		upCity("330100", "杭州市", "330000", "#4b937d"),
		upTrip("330100", "t-role", "2026-07-01", "初始备注"),
	)

	// Invite B as viewer and C as editor; both accept.
	invRes, err := e.collab.InviteToSharedTrip(ctx, bearer(aID, &tripmapv1.InviteToSharedTripRequest{
		RegionCode:   "330100",
		TripId:       "t-role",
		FriendUserId: bID,
		Role:         "viewer",
	}))
	if err != nil {
		t.Fatal(err)
	}
	stID := invRes.Msg.SharedTrip.Id
	if _, err = e.collab.InviteToSharedTrip(ctx, bearer(aID, &tripmapv1.InviteToSharedTripRequest{
		RegionCode:   "330100",
		TripId:       "t-role",
		FriendUserId: cID,
		Role:         "editor",
	})); err != nil {
		t.Fatal(err)
	}
	if _, err = e.collab.RespondSharedTripInvite(ctx, bearer(bID, &tripmapv1.RespondSharedTripInviteRequest{SharedTripId: stID, Accept: true})); err != nil {
		t.Fatal(err)
	}
	if _, err = e.collab.RespondSharedTripInvite(ctx, bearer(cID, &tripmapv1.RespondSharedTripInviteRequest{SharedTripId: stID, Accept: true})); err != nil {
		t.Fatal(err)
	}

	// Viewer B cannot edit via UpdateSharedTrip, SyncChanges, or PrepareUpload.
	_, err = e.collab.UpdateSharedTrip(ctx, bearer(bID, &tripmapv1.UpdateSharedTripRequest{
		SharedTripId: stID,
		Date:         "2026-07-02",
		Note:         "只读尝试修改",
	}))
	wantCode(t, err, connect.CodePermissionDenied)

	_, bRev := e.state2(bID)
	_, err = e.sync2(bID, "b-illegal-sync", bRev, upTrip("330100", "t-role", "2026-07-02", "绕过接口修改"))
	wantCode(t, err, connect.CodePermissionDenied)

	data := photo(t)
	_, err = e.media.PrepareUpload(ctx, bearer(bID, &tripmapv1.PrepareUploadRequest{
		RegionCode:  "330100",
		TripId:      "t-role",
		Filename:    "viewer.png",
		ContentType: "image/png",
		SizeBytes:   int64(len(data)),
		UploadId:    uuid.NewString(),
	}))
	wantCode(t, err, connect.CodePermissionDenied)

	// Owner A promotes B to editor -> B can now edit via SyncChanges and it propagates to A and C!
	if _, err = e.collab.SetSharedTripMemberRole(ctx, bearer(aID, &tripmapv1.SetSharedTripMemberRoleRequest{
		SharedTripId: stID,
		MemberUserId: bID,
		Role:         "editor",
	})); err != nil {
		t.Fatalf("promote B: %v", err)
	}
	_, bRev = e.state2(bID)
	e.mustSync2(bID, "b-legal-sync", bRev, upTrip("330100", "t-role", "2026-07-03", "晋升后修改"))
	gotA, _ := e.state2(aID)
	wantLines(t, gotA, "city:330100:#4b937d", "trip:t-role:330100:2026-07-03:晋升后修改")

	// Owner A removes B -> B loses access and trip sub-record, keeps city check-in; A and C keep t-role.
	if _, err = e.collab.RemoveSharedTripMember(ctx, bearer(aID, &tripmapv1.RemoveSharedTripMemberRequest{
		SharedTripId: stID,
		MemberUserId: bID,
	})); err != nil {
		t.Fatalf("remove B: %v", err)
	}
	gotB, _ := e.state2(bID)
	wantLines(t, gotB, "city:330100:#4b937d")
	_, err = e.collab.UpdateSharedTrip(ctx, bearer(bID, &tripmapv1.UpdateSharedTripRequest{
		SharedTripId: stID,
		Date:         "2026-07-04",
		Note:         "移除后修改",
	}))
	wantCode(t, err, connect.CodePermissionDenied)

	// Owner A dissolves the shared trip -> C keeps C's synchronized trip and city check-in!
	if _, err = e.collab.DeleteSharedTrip(ctx, bearer(aID, &tripmapv1.DeleteSharedTripRequest{SharedTripId: stID})); err != nil {
		t.Fatalf("dissolve shared trip: %v", err)
	}
	gotC, _ := e.state2(cID)
	wantLines(t, gotC, "city:330100:#4b937d", "trip:t-role:330100:2026-07-03:晋升后修改")
}

func TestSharedTripMediaOwnershipAndRevocation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.store.Blobs = &mediaMemory{files: map[string][]byte{}}
	aID := e.register("med_a")
	bID := e.register("med_b")
	cID := e.register("med_c")

	frAB, _ := e.collab.SendFriendRequest(ctx, bearer(aID, &tripmapv1.SendFriendRequestRequest{Target: "med_b"}))
	_, _ = e.collab.RespondFriendRequest(ctx, bearer(bID, &tripmapv1.RespondFriendRequestRequest{FriendshipId: frAB.Msg.Friendship.Id, Accept: true}))
	frAC, _ := e.collab.SendFriendRequest(ctx, bearer(aID, &tripmapv1.SendFriendRequestRequest{Target: "med_c"}))
	_, _ = e.collab.RespondFriendRequest(ctx, bearer(cID, &tripmapv1.RespondFriendRequestRequest{FriendshipId: frAC.Msg.Friendship.Id, Accept: true}))

	e.mustSync2(aID, "a-med", 0,
		upCity("330100", "杭州市", "330000", "#4b937d"),
		upTrip("330100", "t-photo", "2026-08-01", "拍照之旅"),
	)
	invRes, _ := e.collab.InviteToSharedTrip(ctx, bearer(aID, &tripmapv1.InviteToSharedTripRequest{
		RegionCode: "330100", TripId: "t-photo", FriendUserId: bID, Role: "editor",
	}))
	stID := invRes.Msg.SharedTrip.Id
	_, _ = e.collab.InviteToSharedTrip(ctx, bearer(aID, &tripmapv1.InviteToSharedTripRequest{
		RegionCode: "330100", TripId: "t-photo", FriendUserId: cID, Role: "viewer",
	}))
	_, _ = e.collab.RespondSharedTripInvite(ctx, bearer(bID, &tripmapv1.RespondSharedTripInviteRequest{SharedTripId: stID, Accept: true}))
	_, _ = e.collab.RespondSharedTripInvite(ctx, bearer(cID, &tripmapv1.RespondSharedTripInviteRequest{SharedTripId: stID, Accept: true}))

	mA := uploadTripPhoto(t, e, aID, "330100", "t-photo", "a.png")
	mB := uploadTripPhoto(t, e, bID, "330100", "t-photo", "b.png")

	// Viewer C can list and read both mA and mB via CollaborationService.
	listRes, err := e.collab.ListSharedTripMedia(ctx, bearer(cID, &tripmapv1.ListSharedTripMediaRequest{SharedTripId: stID}))
	if err != nil {
		t.Fatalf("C list shared media: %v", err)
	}
	if len(listRes.Msg.Media) != 2 {
		t.Fatalf("expected 2 shared media items, got %d", len(listRes.Msg.Media))
	}
	readRes, err := e.collab.ReadSharedTripMedia(ctx, bearer(cID, &tripmapv1.ReadSharedTripMediaRequest{
		SharedTripId: stID, MediaId: mA, Thumbnail: true,
	}))
	if err != nil || len(readRes.Msg.Data) == 0 {
		t.Fatalf("C read shared media mA: %v", err)
	}

	// B cannot delete A's media via MediaService.DeleteMedia (mA remains intact for A).
	_, _ = e.media.DeleteMedia(ctx, bearer(bID, &tripmapv1.DeleteMediaRequest{Id: mA}))
	if _, err = e.media.ReadMedia(ctx, bearer(aID, &tripmapv1.ReadMediaRequest{Id: mA})); err != nil {
		t.Fatalf("B should not be able to delete A's media mA: %v", err)
	}

	// B leaves the shared trip -> B cannot list or read mA anymore; A and C no longer see mB in shared media;
	// B still owns mB in B's personal MediaService.ListMedia.
	if _, err = e.collab.LeaveSharedTrip(ctx, bearer(bID, &tripmapv1.LeaveSharedTripRequest{SharedTripId: stID})); err != nil {
		t.Fatal(err)
	}
	_, err = e.collab.ReadSharedTripMedia(ctx, bearer(bID, &tripmapv1.ReadSharedTripMediaRequest{SharedTripId: stID, MediaId: mA}))
	wantCode(t, err, connect.CodePermissionDenied)

	listRes, err = e.collab.ListSharedTripMedia(ctx, bearer(aID, &tripmapv1.ListSharedTripMediaRequest{SharedTripId: stID}))
	if err != nil {
		t.Fatal(err)
	}
	if len(listRes.Msg.Media) != 1 || listRes.Msg.Media[0].Media.Id != mA {
		t.Fatalf("expected only mA after B left, got %+v", listRes.Msg.Media)
	}
	_, err = e.collab.ReadSharedTripMedia(ctx, bearer(aID, &tripmapv1.ReadSharedTripMediaRequest{SharedTripId: stID, MediaId: mB}))
	wantCode(t, err, connect.CodeNotFound)

	bPersonalMedia, err := e.media.ListMedia(ctx, bearer(bID, &tripmapv1.ListMediaRequest{RegionCode: "330100"}))
	if err != nil || len(bPersonalMedia.Msg.Media) != 1 || bPersonalMedia.Msg.Media[0].Id != mB {
		t.Fatalf("B should still own mB in personal media: %+v %v", bPersonalMedia, err)
	}
}

func TestRemoveFriendDetachesSharedTripsAndPreservesRecords(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	aID := e.register("unfriend_a")
	bID := e.register("unfriend_b")

	frAB, _ := e.collab.SendFriendRequest(ctx, bearer(aID, &tripmapv1.SendFriendRequestRequest{Target: "unfriend_b"}))
	frID := frAB.Msg.Friendship.Id
	_, _ = e.collab.RespondFriendRequest(ctx, bearer(bID, &tripmapv1.RespondFriendRequestRequest{FriendshipId: frID, Accept: true}))

	e.mustSync2(aID, "a-uf", 0,
		upCity("330100", "杭州市", "330000", "#4b937d"),
		upTrip("330100", "t-uf", "2026-09-01", "好友旅行"),
	)
	invRes, _ := e.collab.InviteToSharedTrip(ctx, bearer(aID, &tripmapv1.InviteToSharedTripRequest{
		RegionCode: "330100", TripId: "t-uf", FriendUserId: bID, Role: "editor",
	}))
	stID := invRes.Msg.SharedTrip.Id
	_, _ = e.collab.RespondSharedTripInvite(ctx, bearer(bID, &tripmapv1.RespondSharedTripInviteRequest{SharedTripId: stID, Accept: true}))

	// Unfriending detaches B from A's shared trip while keeping both users' existing footprints intact.
	if _, err := e.collab.RemoveFriend(ctx, bearer(aID, &tripmapv1.RemoveFriendRequest{FriendshipId: frID})); err != nil {
		t.Fatal(err)
	}
	_, err := e.collab.UpdateSharedTrip(ctx, bearer(bID, &tripmapv1.UpdateSharedTripRequest{
		SharedTripId: stID, Date: "2026-09-09", Note: "解绑后尝试修改",
	}))
	wantCode(t, err, connect.CodePermissionDenied)

	gotA, _ := e.state2(aID)
	wantLines(t, gotA, "city:330100:#4b937d", "trip:t-uf:330100:2026-09-01:好友旅行")
	gotB, _ := e.state2(bID)
	wantLines(t, gotB, "city:330100:#4b937d", "trip:t-uf:330100:2026-09-01:好友旅行")
}

