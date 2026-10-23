package accesstoken_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/accesstoken"
	"github.com/kongken/mono/internal/bff/ent"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/bff/testutil"
)

func setup(t *testing.T) (*accesstoken.Service, *ent.Client, session.BrowserSession, *time.Time) {
	t.Helper()
	db, _, _ := testutil.Backing(t)
	u := db.User.Create().SetIssuer("https://idp").SetExternalID("root").SaveX(t.Context())
	svc := accesstoken.New(db)
	now := time.Now().UTC().Truncate(time.Second)
	svc.SetClock(func() time.Time { return now })
	return svc, db, session.BrowserSession{UserID: u.ID, Roles: []string{"metro-admin"}}, &now
}

func TestCreateStoresOnlyTheDigestAndSnapshotsRoles(t *testing.T) {
	svc, db, owner, _ := setup(t)
	row, secret, err := svc.Create(t.Context(), owner, accesstoken.CreateInput{Name: " agent ", Scopes: []string{"metro:write", "metro:read", "metro:write"}, ExpiresInDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	if row.Name != "agent" || row.Audience != "metro" || !slices.Equal(row.Scopes, []string{"metro:read", "metro:write"}) || !slices.Equal(row.Roles, owner.Roles) {
		t.Fatalf("row: %+v", row)
	}
	if row.TokenHash == secret || row.TokenHash != session.HashToken(secret) {
		t.Fatal("the stored value is not the digest of the secret")
	}
	if got := row.ExpiresAt.Sub(row.CreatedAt); got != 7*24*time.Hour {
		t.Fatalf("lifetime: %v", got)
	}
	// An empty scope list means read-only.
	row, _, err = svc.Create(t.Context(), owner, accesstoken.CreateInput{Name: "ro", ExpiresInDays: 1})
	if err != nil || !slices.Equal(row.Scopes, []string{"metro:read"}) {
		t.Fatalf("default scopes: %+v %v", row, err)
	}
	if n := db.AccessToken.Query().CountX(t.Context()); n != 2 {
		t.Fatalf("rows: %d", n)
	}
}

func TestAuthenticateRejectsEveryUnusableToken(t *testing.T) {
	svc, db, owner, now := setup(t)
	ctx := t.Context()
	row, secret, err := svc.Create(ctx, owner, accesstoken.CreateInput{Name: "t", Scopes: []string{"metro:write"}, ExpiresInDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.Authenticate(ctx, secret, "metro")
	if err != nil || p.TokenID != row.ID || p.UserID != owner.UserID || !p.Allows("metro:read") || !p.Allows("metro:write") || !slices.Equal(p.Roles, owner.Roles) {
		t.Fatalf("principal: %+v %v", p, err)
	}
	for name, bad := range map[string]string{"empty": "", "short": "mcp_abc", "wrong prefix": "xyz_" + secret[4:], "unknown": "mcp_" + secret[4:len(secret)-1] + "0"} {
		if _, err := svc.Authenticate(ctx, bad, "metro"); !errors.Is(err, accesstoken.ErrInvalidToken) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := svc.Authenticate(ctx, secret, "tripmap"); !errors.Is(err, accesstoken.ErrInvalidToken) {
		t.Errorf("another audience: %v", err)
	}

	// Expiry.
	*now = now.Add(25 * time.Hour)
	if _, err := svc.Authenticate(ctx, secret, "metro"); !errors.Is(err, accesstoken.ErrInvalidToken) {
		t.Errorf("expired: %v", err)
	}
	*now = now.Add(-25 * time.Hour)

	// A user who asked to delete their data, or was disabled, keeps no access.
	db.User.UpdateOneID(owner.UserID).SetDeletionRequestedAt(*now).ExecX(ctx)
	if _, err := svc.Authenticate(ctx, secret, "metro"); !errors.Is(err, accesstoken.ErrInvalidToken) {
		t.Errorf("deletion pending: %v", err)
	}
	db.User.UpdateOneID(owner.UserID).ClearDeletionRequestedAt().SetDisabled(true).ExecX(ctx)
	if _, err := svc.Authenticate(ctx, secret, "metro"); !errors.Is(err, accesstoken.ErrInvalidToken) {
		t.Errorf("disabled: %v", err)
	}
	db.User.UpdateOneID(owner.UserID).SetDisabled(false).ExecX(ctx)

	// Revocation, and revoking twice keeps the first time.
	if err := svc.Revoke(ctx, owner.UserID, row.ID); err != nil {
		t.Fatal(err)
	}
	first := db.AccessToken.GetX(ctx, row.ID).RevokedAt
	*now = now.Add(time.Hour)
	if err := svc.Revoke(ctx, owner.UserID, row.ID); err != nil {
		t.Fatal(err)
	}
	if got := db.AccessToken.GetX(ctx, row.ID).RevokedAt; !got.Equal(*first) {
		t.Errorf("second revoke moved revoked_at: %v -> %v", first, got)
	}
	if _, err := svc.Authenticate(ctx, secret, "metro"); !errors.Is(err, accesstoken.ErrInvalidToken) {
		t.Errorf("revoked: %v", err)
	}
	if err := svc.Revoke(ctx, owner.UserID, "missing"); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("unknown id: %v", err)
	}
}

func TestCapCountsOnlyValidTokensAndDeletingTheUserRemovesTokens(t *testing.T) {
	svc, db, owner, now := setup(t)
	ctx := t.Context()
	var ids []string
	for i := 0; i < accesstoken.MaxActivePerUser; i++ {
		row, _, err := svc.Create(ctx, owner, accesstoken.CreateInput{Name: "t", ExpiresInDays: 1})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, row.ID)
	}
	if _, _, err := svc.Create(ctx, owner, accesstoken.CreateInput{Name: "over", ExpiresInDays: 1}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("over the cap: %v", err)
	}
	if err := svc.Revoke(ctx, owner.UserID, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Create(ctx, owner, accesstoken.CreateInput{Name: "after revoke", ExpiresInDays: 1}); err != nil {
		t.Fatalf("a revoked token still counts: %v", err)
	}
	*now = now.Add(48 * time.Hour) // everything expires
	if _, _, err := svc.Create(ctx, owner, accesstoken.CreateInput{Name: "after expiry", ExpiresInDays: 1}); err != nil {
		t.Fatalf("expired tokens still count: %v", err)
	}
	if rows, err := svc.List(ctx, owner.UserID, false); err != nil || len(rows) != 12 {
		t.Fatalf("list: %d %v", len(rows), err)
	}
	db.User.DeleteOneID(owner.UserID).ExecX(ctx)
	if n := db.AccessToken.Query().CountX(ctx); n != 0 {
		t.Fatalf("tokens outlived their owner: %d", n)
	}
}

func TestCreateValidatesInput(t *testing.T) {
	svc, _, owner, _ := setup(t)
	long := make([]rune, 65)
	for i := range long {
		long[i] = 'a'
	}
	for name, in := range map[string]accesstoken.CreateInput{
		"blank name":    {Name: "  ", ExpiresInDays: 1},
		"long name":     {Name: string(long), ExpiresInDays: 1},
		"control chars": {Name: "a\nb", ExpiresInDays: 1},
		"no lifetime":   {Name: "a"},
		"too long":      {Name: "a", ExpiresInDays: 91},
		"bad scope":     {Name: "a", ExpiresInDays: 1, Scopes: []string{"admin"}},
		"bad audience":  {Name: "a", ExpiresInDays: 1, Audience: "other"},
	} {
		if _, _, err := svc.Create(t.Context(), owner, in); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
}
