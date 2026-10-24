package e2e_test

import (
	"context"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func riderWrite[T any](b *Browser, msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	b.Write(context.Background(), req)
	return req
}

func TestChainMetroRiderProfileAndRecords(t *testing.T) {
	h := NewChainHarness(t, ChainMetro{})
	ctx := context.Background()
	sh := h.MetroDB.City.Create().SetSlug("shanghai").SetName("上海").SaveX(ctx)
	h.MetroDB.Line.Create().SetCode("sh-1").SetCityID(sh.ID).SetName("1号线").SetNumber(1).SetLengthKm(36.9).SetColor("#E4002B").SaveX(ctx)

	alice, bob, visitor := h.FreshBrowser(), h.FreshBrowser(), h.FreshBrowser()
	if err := alice.LoginWithRoles(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := bob.LoginWithRoles(ctx, "bob"); err != nil {
		t.Fatal(err)
	}
	// Anonymous callers cannot reach personal methods.
	if _, err := visitor.MetroRider.ListMyLines(ctx, connect.NewRequest(&bffmetrov1.ListMyLinesRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous list: %v", err)
	}

	if _, err := alice.MetroRider.MarkLine(ctx, riderWrite(alice, &bffmetrov1.MarkLineRequest{LineCode: "sh-1"})); err != nil {
		t.Fatal(err)
	}
	year := int32(2020)
	marked, err := alice.MetroRider.MarkLine(ctx, riderWrite(alice, &bffmetrov1.MarkLineRequest{LineCode: "sh-1", RiddenYear: &year}))
	if err != nil || marked.Msg.Line.RiddenYear != 2020 {
		t.Fatalf("mark twice: %+v %v", marked, err)
	}
	if _, err = alice.MetroRider.UpdateMyProfile(ctx, riderWrite(alice, &bffmetrov1.UpdateMyProfileRequest{Profile: &bffmetrov1.ProfileInput{
		Handle: "alice", DisplayName: "Alice", Github: "alice-dev",
	}})); err != nil {
		t.Fatal(err)
	}

	// Private until published, and "private" looks like "no such user".
	for _, handle := range []string{"alice", "ghost"} {
		if _, err = visitor.MetroRider.GetPublicProfile(ctx, connect.NewRequest(&bffmetrov1.GetPublicProfileRequest{Handle: handle})); connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("%s: %v", handle, err)
		}
	}
	if _, err = alice.MetroRider.UpdateMyProfile(ctx, riderWrite(alice, &bffmetrov1.UpdateMyProfileRequest{Profile: &bffmetrov1.ProfileInput{
		Handle: "alice", DisplayName: "Alice", Github: "alice-dev", IsPublic: true,
	}})); err != nil {
		t.Fatal(err)
	}
	pub, err := visitor.MetroRider.GetPublicProfile(ctx, connect.NewRequest(&bffmetrov1.GetPublicProfileRequest{Handle: "alice"}))
	if err != nil || pub.Msg.Profile.DisplayName != "Alice" || pub.Msg.Stats.LineCount != 1 ||
		pub.Msg.Cities[0].Lines[0].LineCode != "sh-1" || pub.Msg.Cities[0].Lines[0].RiddenYear != 2020 {
		t.Fatalf("public profile: %+v %v", pub, err)
	}

	// Bob cannot take Alice's handle and sees none of her records.
	_, err = bob.MetroRider.UpdateMyProfile(ctx, riderWrite(bob, &bffmetrov1.UpdateMyProfileRequest{Profile: &bffmetrov1.ProfileInput{Handle: "alice"}}))
	if connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("handle clash: %v", err)
	}
	mine, err := bob.MetroRider.ListMyLines(ctx, connect.NewRequest(&bffmetrov1.ListMyLinesRequest{}))
	if err != nil || mine.Msg.Stats.LineCount != 0 {
		t.Fatalf("bob's records: %+v %v", mine, err)
	}
	// Writes need the CSRF token.
	if _, err = bob.MetroRider.MarkLine(ctx, connect.NewRequest(&bffmetrov1.MarkLineRequest{LineCode: "sh-1"})); err == nil {
		t.Fatal("a write without a CSRF token was accepted")
	}
}

// Deleting an account cleans every enabled product; a failing product keeps the
// account pending and the retry finishes it.
func TestChainAccountDeletionCleansMetroAndRetries(t *testing.T) {
	var failCleanup atomic.Bool
	failCleanup.Store(true)
	injectFault := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		if info.FullMethod == "/metro.v1.AccountCleanupService/CleanupUserData" && failCleanup.Load() {
			return nil, status.Error(codes.Unavailable, "injected")
		}
		return next(ctx, req)
	}
	h := NewChainHarness(t, ChainMetro{}, ChainMetroUnary{injectFault})
	ctx := context.Background()
	sh := h.MetroDB.City.Create().SetSlug("shanghai").SetName("上海").SaveX(ctx)
	h.MetroDB.Line.Create().SetCode("sh-1").SetCityID(sh.ID).SetName("1号线").SetColor("#E4002B").SaveX(ctx)

	a, b := h.FreshBrowser(), h.FreshBrowser()
	aID, err := a.Register(ctx, "del_a", "", "A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Register(ctx, "del_b", "", "B"); err != nil {
		t.Fatal(err)
	}
	for _, br := range []*Browser{a, b} {
		if _, err = br.MetroRider.MarkLine(ctx, riderWrite(br, &bffmetrov1.MarkLineRequest{LineCode: "sh-1"})); err != nil {
			t.Fatal(err)
		}
	}
	h.TripmapDB.CityVisit.Create().SetUserID(aID).SetRegionCode("330100").SetRegionName("杭州市").SetProvinceCode("330000").SetColor("#4b937d").SaveX(ctx)

	req := &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "metro-del-1"}
	if err = a.DeleteAccount(ctx, req, aID); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("a failing product must fail the deletion: %v", err)
	}
	// The account stays closed: no access in either product until the deletion ends.
	if _, err = a.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a half-deleted account kept its session: %v", err)
	}
	if d, err := a.GetAccountDeletion(ctx); err != nil || len(d.GetProducts()) != 2 {
		t.Fatalf("the products of the deletion: %v %v", d, err)
	}
	failCleanup.Store(false)
	if err = a.DeleteAccount(ctx, req, aID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if n := h.MetroDB.RiderLine.Query().CountX(ctx); n != 1 {
		t.Fatalf("%d ride records left, want only B's", n)
	}
	if h.TripmapDB.CityVisit.Query().CountX(ctx) != 0 {
		t.Fatal("tripmap data survived the deletion")
	}
}
