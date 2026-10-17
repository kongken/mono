package application_test

import (
	"context"
	"testing"

	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/tripmap/application"
	"github.com/kongken/mono/internal/tripmap/testutil"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func profileHarness(t *testing.T) (*application.ProfileService, *application.Store) {
	t.Helper()
	db, rdb, prefix := testutil.Backing(t)
	st := &application.Store{DB: db, Redis: rdb, KeyPrefix: prefix}
	return application.NewProfileService(storeSource{st}), st
}

func TestEnsureProfileRejectsForgedUserID(t *testing.T) {
	svc, _ := profileHarness(t)
	ctx := rpcauth.WithCall(context.Background(), rpcauth.Call{UserID: "user-a"})
	_, err := svc.EnsureProfile(ctx, &tripmapv1.EnsureProfileRequest{
		UserId:      "user-b",
		DisplayName: "Evil",
	})
	if err == nil {
		t.Fatal("expected permission denied")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %v", status.Code(err))
	}
}

func TestEnsureProfileUsesPrincipalOnly(t *testing.T) {
	svc, st := profileHarness(t)
	ctx := rpcauth.WithCall(context.Background(), rpcauth.Call{UserID: "user-a"})
	out, err := svc.EnsureProfile(ctx, &tripmapv1.EnsureProfileRequest{
		DisplayName: "Alice",
		Username:    "alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.GetCreated() {
		t.Fatal("expected created")
	}
	u, err := st.DB.User.Get(ctx, "user-a")
	if err != nil || u.DisplayName != "Alice" {
		t.Fatalf("profile row: %v err=%v", u, err)
	}
}
