package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/kongken/mono/internal/metro/application"
	"github.com/kongken/mono/internal/metro/ent/riderline"
	"github.com/kongken/mono/internal/metro/testutil"
	"github.com/kongken/mono/internal/platform/rpcauth"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func as(user string) context.Context {
	return rpcauth.WithCall(context.Background(), rpcauth.Call{UserID: user})
}

func year(y int32) *int32 { return &y }

func TestMarkLineIsIdempotentAndUpdatesTheYear(t *testing.T) {
	db := testutil.DB(t)
	s := seed{db}
	sh := s.city(t, "shanghai", "上海", 0)
	s.line(t, sh, "sh-1", lineOpts{number: 1, km: 36.9})
	s.line(t, sh, "sh-2", lineOpts{number: 2, km: 64.2})
	svc := application.NewRiderService(db)

	if _, err := svc.MarkLine(as("a"), &metrov1.MarkLineRequest{LineCode: "sh-1", RiddenYear: year(2019)}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.MarkLine(as("a"), &metrov1.MarkLineRequest{LineCode: "sh-1", RiddenYear: year(2021)})
	if err != nil || res.Line.RiddenYear != 2021 || res.Line.LineName != "sh-1" {
		t.Fatalf("second mark: %+v %v", res, err)
	}
	if n, _ := db.RiderLine.Query().Where(riderline.UserID("a")).Count(context.Background()); n != 1 {
		t.Fatalf("%d records after marking twice, want 1", n)
	}
	// Marking without a year forgets the year.
	if res, err = svc.MarkLine(as("a"), &metrov1.MarkLineRequest{LineCode: "sh-1"}); err != nil || res.Line.RiddenYear != 0 {
		t.Fatalf("no year: %+v %v", res, err)
	}
	if _, err = svc.MarkLine(as("a"), &metrov1.MarkLineRequest{LineCode: "sh-2", RiddenYear: year(2022)}); err != nil {
		t.Fatal(err)
	}

	list, err := svc.ListMyLines(as("a"), &metrov1.ListMyLinesRequest{})
	if err != nil || len(list.Cities) != 1 || len(list.Cities[0].Lines) != 2 || list.Cities[0].Lines[0].LineCode != "sh-1" ||
		list.Stats.CityCount != 1 || list.Stats.LineCount != 2 || list.Stats.TotalLengthKm != 101.1 {
		t.Fatalf("list: %+v %v", list, err)
	}

	// Unmarking is idempotent too, including for a line that was never marked.
	if _, err = svc.UnmarkLine(as("a"), &metrov1.UnmarkLineRequest{LineCode: "sh-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.UnmarkLine(as("a"), &metrov1.UnmarkLineRequest{LineCode: "sh-1"}); err != nil {
		t.Fatalf("unmark twice: %v", err)
	}
	if _, err = svc.UnmarkLine(as("b"), &metrov1.UnmarkLineRequest{LineCode: "sh-2"}); err != nil {
		t.Fatalf("unmark a line b never marked: %v", err)
	}
	// ...and did not touch a's other record.
	if list, _ = svc.ListMyLines(as("a"), &metrov1.ListMyLinesRequest{}); list.Stats.LineCount != 1 {
		t.Fatalf("a's records after b's unmark: %+v", list)
	}
	for _, code := range []string{"nope", ""} {
		if _, err = svc.MarkLine(as("a"), &metrov1.MarkLineRequest{LineCode: code}); status.Code(err) == codes.OK {
			t.Fatalf("mark %q succeeded", code)
		}
	}
	if _, err = svc.UnmarkLine(as("a"), &metrov1.UnmarkLineRequest{LineCode: "nope"}); status.Code(err) != codes.NotFound {
		t.Fatalf("unmark unknown line: %v", err)
	}
	next := int32(time.Now().Year() + 1)
	for _, y := range []int32{1899, next} {
		if _, err = svc.MarkLine(as("a"), &metrov1.MarkLineRequest{LineCode: "sh-1", RiddenYear: &y}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("year %d: %v", y, err)
		}
	}
	if _, err = svc.MarkLine(context.Background(), &metrov1.MarkLineRequest{LineCode: "sh-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("no user: %v", err)
	}
}

func TestRetiredAndPlannedLinesCannotBeNewlyMarked(t *testing.T) {
	db := testutil.DB(t)
	s := seed{db}
	c := s.city(t, "shanghai", "上海", 0)
	s.line(t, c, "sh-old", lineOpts{number: 1, status: "operating", km: 10})
	s.line(t, c, "sh-plan", lineOpts{number: 2, status: "planned"})
	svc := application.NewRiderService(db)
	if _, err := svc.MarkLine(as("a"), &metrov1.MarkLineRequest{LineCode: "sh-old"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.MarkLine(as("a"), &metrov1.MarkLineRequest{LineCode: "sh-plan"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("planned: %v", err)
	}

	// The line is retired after a rode it: the record stays, can be re-dated and
	// removed, and a new rider cannot add it.
	db.Line.Update().SetStatus(statusOf("retired")).ExecX(context.Background())
	if _, err := svc.MarkLine(as("b"), &metrov1.MarkLineRequest{LineCode: "sh-old"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("new mark of a retired line: %v", err)
	}
	if _, err := svc.MarkLine(as("a"), &metrov1.MarkLineRequest{LineCode: "sh-old", RiddenYear: year(2015)}); err != nil {
		t.Fatalf("re-dating an existing record: %v", err)
	}
	list, _ := svc.ListMyLines(as("a"), &metrov1.ListMyLinesRequest{})
	if len(list.Cities) != 1 || list.Cities[0].Lines[0].Status != metrov1.LineStatus_LINE_STATUS_RETIRED || list.Cities[0].Lines[0].RiddenYear != 2015 {
		t.Fatalf("history after retirement: %+v", list)
	}
	if _, err := svc.UnmarkLine(as("a"), &metrov1.UnmarkLineRequest{LineCode: "sh-old"}); err != nil {
		t.Fatalf("unmark a retired line: %v", err)
	}
}

func TestLinesWithRiderRecordsCannotBeDeletedButCanBeRetired(t *testing.T) {
	db := testutil.DB(t)
	admin := application.NewAdminService(db, application.RiderLineGuard)
	riders := application.NewRiderService(db)
	ctx := adminCtx()
	if _, err := admin.CreateCity(ctx, &metrov1.CreateCityRequest{City: &metrov1.CityInput{Slug: "shanghai", Name: "上海"}}); err != nil {
		t.Fatal(err)
	}
	created, err := admin.CreateLine(ctx, &metrov1.CreateLineRequest{Line: goodLine()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = riders.MarkLine(as("a"), &metrov1.MarkLineRequest{LineCode: "sh-1", RiddenYear: year(2020)}); err != nil {
		t.Fatal(err)
	}
	_, err = admin.DeleteLine(ctx, &metrov1.DeleteLineRequest{Id: created.Line.Id, ExpectedRevision: 1})
	refs := detail(t, err, &metrov1.ReferencedDetail{}).References
	if status.Code(err) != codes.FailedPrecondition || len(refs) != 1 || refs[0].EntityType != "rider_line" {
		t.Fatalf("delete: %v", err)
	}
	retire := goodLine()
	retire.Status = metrov1.LineStatus_LINE_STATUS_RETIRED
	if _, err = admin.UpdateLine(ctx, &metrov1.UpdateLineRequest{Id: created.Line.Id, ExpectedRevision: 1, Line: retire}); err != nil {
		t.Fatal(err)
	}
	list, err := riders.ListMyLines(as("a"), &metrov1.ListMyLinesRequest{})
	if err != nil || list.Stats.LineCount != 1 || list.Cities[0].Lines[0].RiddenYear != 2020 {
		t.Fatalf("history: %+v %v", list, err)
	}
}

func TestProfilesArePrivateUntilPublishedAndHandlesAreUnique(t *testing.T) {
	db := testutil.DB(t)
	s := seed{db}
	c := s.city(t, "shanghai", "上海", 0)
	s.line(t, c, "sh-1", lineOpts{number: 1, km: 36.9})
	svc := application.NewRiderService(db)

	// Reading before writing creates nothing.
	mine, err := svc.GetMyProfile(as("a"), &metrov1.GetMyProfileRequest{})
	if err != nil || mine.Exists {
		t.Fatalf("empty profile: %+v %v", mine, err)
	}
	if n, _ := db.RiderProfile.Query().Count(context.Background()); n != 0 {
		t.Fatal("reading a profile created a row")
	}
	profile := &metrov1.ProfileInput{Handle: " Alice_01 ", DisplayName: "Alice", Bio: "line 1\nline 2",
		Twitter: "alice", Github: "https://github.com/alice", Location: "Shanghai"}
	saved, err := svc.UpdateMyProfile(as("a"), &metrov1.UpdateMyProfileRequest{Profile: profile})
	if err != nil || saved.Profile.Handle != "alice_01" || saved.Profile.IsPublic {
		t.Fatalf("save: %+v %v", saved, err)
	}
	if _, err = svc.MarkLine(as("a"), &metrov1.MarkLineRequest{LineCode: "sh-1", RiddenYear: year(2020)}); err != nil {
		t.Fatal(err)
	}

	// Private: indistinguishable from a handle nobody has.
	_, privateErr := svc.GetPublicProfile(context.Background(), &metrov1.GetPublicProfileRequest{Handle: "alice_01"})
	_, missingErr := svc.GetPublicProfile(context.Background(), &metrov1.GetPublicProfileRequest{Handle: "nobody_here"})
	_, badErr := svc.GetPublicProfile(context.Background(), &metrov1.GetPublicProfileRequest{Handle: "!!"})
	for _, err := range []error{privateErr, missingErr, badErr} {
		if status.Code(err) != codes.NotFound || status.Convert(err).Message() != "profile not found" {
			t.Fatalf("private/missing profile: %v", err)
		}
	}

	profile.IsPublic = true
	if _, err = svc.UpdateMyProfile(as("a"), &metrov1.UpdateMyProfileRequest{Profile: profile}); err != nil {
		t.Fatal(err)
	}
	pub, err := svc.GetPublicProfile(context.Background(), &metrov1.GetPublicProfileRequest{Handle: "ALICE_01"})
	if err != nil || pub.Profile.DisplayName != "Alice" || pub.Profile.Handle != "alice_01" || pub.Stats.LineCount != 1 ||
		len(pub.Cities) != 1 || pub.Cities[0].Lines[0].Color != "#E4002B" || pub.Cities[0].Lines[0].RiddenYear != 2020 {
		t.Fatalf("public profile: %+v %v", pub, err)
	}

	// Someone else cannot take the handle; their own profile is separate.
	taken := &metrov1.ProfileInput{Handle: "alice_01"}
	_, err = svc.UpdateMyProfile(as("b"), &metrov1.UpdateMyProfileRequest{Profile: taken})
	if status.Code(err) != codes.AlreadyExists || fieldErrorsOf(t, err)["handle"] == "" {
		t.Fatalf("handle clash: %v", err)
	}
	if _, err = svc.UpdateMyProfile(as("b"), &metrov1.UpdateMyProfileRequest{Profile: &metrov1.ProfileInput{Handle: "bobby"}}); err != nil {
		t.Fatal(err)
	}
	// b never sees a's records, a never sees b's.
	if list, _ := svc.ListMyLines(as("b"), &metrov1.ListMyLinesRequest{}); list.Stats.LineCount != 0 {
		t.Fatalf("b sees a's records: %+v", list)
	}

	// Replacing the profile can clear the handle, which must then be private again.
	profile.Handle = ""
	_, err = svc.UpdateMyProfile(as("a"), &metrov1.UpdateMyProfileRequest{Profile: profile})
	if got := fieldErrorsOf(t, err); got["is_public"] == "" {
		t.Fatalf("public without a handle: %v", got)
	}
	profile.IsPublic = false
	if saved, err = svc.UpdateMyProfile(as("a"), &metrov1.UpdateMyProfileRequest{Profile: profile}); err != nil || saved.Profile.Handle != "" {
		t.Fatalf("clear handle: %+v %v", saved, err)
	}
}

func fieldErrorsOf(t *testing.T, err error) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, e := range detail(t, err, &metrov1.ValidationDetail{}).Errors {
		out[e.Field] = e.Reason
	}
	return out
}

func TestProfileValidation(t *testing.T) {
	db := testutil.DB(t)
	svc := application.NewRiderService(db)
	bad := &metrov1.ProfileInput{
		Handle: "ab", DisplayName: " padded", Bio: string(make([]rune, 501)), HeaderImages: []string{"http://x/y.png"},
		Twitter: "way_too_long_for_twitter", Instagram: "https://evil.example/insta", Weibo: "http://weibo.com/x",
		Facebook: "a", Github: "-bad-",
	}
	_, err := svc.UpdateMyProfile(as("a"), &metrov1.UpdateMyProfileRequest{Profile: bad})
	got := fieldErrorsOf(t, err)
	for _, f := range []string{"handle", "display_name", "bio", "header_images[0]", "twitter", "instagram", "weibo", "facebook", "github"} {
		if got[f] == "" {
			t.Errorf("no error for %s: %v", f, got)
		}
	}
	if _, err = svc.UpdateMyProfile(as("a"), &metrov1.UpdateMyProfileRequest{Profile: &metrov1.ProfileInput{Handle: "admin"}}); fieldErrorsOf(t, err)["handle"] == "" {
		t.Fatalf("reserved handle: %v", err)
	}
	ok := &metrov1.ProfileInput{
		Twitter: "https://x.com/alice", Instagram: "https://www.instagram.com/alice/", Weibo: "小明", Facebook: "alice.smith",
		Github: "alice-smith", HeaderImages: []string{"https://img.example/1.jpg"},
	}
	if _, err = svc.UpdateMyProfile(as("a"), &metrov1.UpdateMyProfileRequest{Profile: ok}); err != nil {
		t.Fatalf("valid social accounts: %v", err)
	}
	if _, err = svc.UpdateMyProfile(as("a"), &metrov1.UpdateMyProfileRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing profile: %v", err)
	}
}

func TestCleanupUserDataRemovesOnlyThatUsersData(t *testing.T) {
	db := testutil.DB(t)
	s := seed{db}
	c := s.city(t, "shanghai", "上海", 0)
	s.line(t, c, "sh-1", lineOpts{number: 1})
	riders := application.NewRiderService(db)
	cleanup := application.NewAccountCleanupService(db)
	for _, u := range []string{"a", "b"} {
		if _, err := riders.UpdateMyProfile(as(u), &metrov1.UpdateMyProfileRequest{Profile: &metrov1.ProfileInput{Handle: "user_" + u + "x", IsPublic: true}}); err != nil {
			t.Fatal(err)
		}
		if _, err := riders.MarkLine(as(u), &metrov1.MarkLineRequest{LineCode: "sh-1"}); err != nil {
			t.Fatal(err)
		}
	}
	// The audit log keeps its actor.
	admin := application.NewAdminService(db)
	if _, err := admin.CreateRollingStock(rpcauth.WithCall(context.Background(), rpcauth.Call{UserID: "a"}), &metrov1.CreateRollingStockRequest{RollingStock: &metrov1.RollingStockInput{Code: "x"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := cleanup.CleanupUserData(as("a"), &metrov1.CleanupUserDataRequest{UserId: "b"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cleaning another user: %v", err)
	}
	if _, err := cleanup.CleanupUserData(context.Background(), &metrov1.CleanupUserDataRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("no user: %v", err)
	}
	for range 2 { // idempotent
		if _, err := cleanup.CleanupUserData(as("a"), &metrov1.CleanupUserDataRequest{UserId: "a"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cleanup.CleanupUserData(as("never-seen"), &metrov1.CleanupUserDataRequest{}); err != nil {
		t.Fatalf("unknown user: %v", err)
	}
	if _, err := riders.GetPublicProfile(context.Background(), &metrov1.GetPublicProfileRequest{Handle: "user_ax"}); status.Code(err) != codes.NotFound {
		t.Fatalf("a's profile survived: %v", err)
	}
	if list, _ := riders.ListMyLines(as("a"), &metrov1.ListMyLinesRequest{}); list.Stats.LineCount != 0 {
		t.Fatal("a's records survived")
	}
	if _, err := riders.GetPublicProfile(context.Background(), &metrov1.GetPublicProfileRequest{Handle: "user_bx"}); err != nil {
		t.Fatalf("b's profile was removed: %v", err)
	}
	if list, _ := riders.ListMyLines(as("b"), &metrov1.ListMyLinesRequest{}); list.Stats.LineCount != 1 {
		t.Fatal("b's records were removed")
	}
	if n := auditCount(t, db); n != 1 {
		t.Fatalf("audit rows %d, want 1 (kept)", n)
	}
}
