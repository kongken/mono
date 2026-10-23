package e2e_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"connectrpc.com/connect"
	bffsession "github.com/kongken/mono/internal/bff/ent/session"
	"github.com/kongken/mono/internal/bff/session"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

func TestChainAccountDeletionRetryReceiptAndIsolation(t *testing.T) {
	blobs := &faultInjectingBlobs{fail: true}
	h := NewChainHarness(t, blobs)
	ctx := context.Background()

	a := h.FreshBrowser()
	aID, err := a.Register(ctx, "delete_a", "password1", "Delete A")
	if err != nil {
		t.Fatal(err)
	}
	b := h.FreshBrowser()
	bID, err := b.Register(ctx, "delete_b", "password1", "Delete B")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.TripmapDB.CityVisit.Create().
		SetUserID(aID).SetRegionCode("330100").SetRegionName("杭州市").SetProvinceCode("330000").SetColor("#4b937d").
		Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err = h.TripmapDB.MediaObject.Create().
		SetUserID(aID).SetObjectKey("owned/photo.jpg").SetContentType("image/jpeg").SetSizeBytes(10).
		Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = h.TripmapDB.CityVisit.Create().
		SetUserID(bID).SetRegionCode("330200").SetRegionName("宁波市").SetProvinceCode("330000").SetColor("#4b937d").
		Save(ctx); err != nil {
		t.Fatal(err)
	}

	req := &bffv1.DeleteAccountRequest{Confirmation: "DELETE"}
	if err = a.DeleteAccount(ctx, req, aID); err == nil || connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("expected unavailable on S3 failure: %v", err)
	}
	me, err := a.Me(ctx)
	if err != nil || !me.GetDeletionPending() {
		t.Fatalf("deletion pending me: %v %v", me, err)
	}
	if _, err = a.GetFootprints(ctx); err == nil || connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("footprints blocked during deletion: %v", err)
	}
	blobs.fail = false
	sidToken := a.SessionCookie()
	if err = a.DeleteAccount(ctx, req, aID); err != nil {
		t.Fatal(err)
	}
	if len(blobs.keys) < 1 {
		t.Fatalf("expected S3 deletes, got %v", blobs.keys)
	}
	// Simulate a lost success response: same session cookie, receipt already stored.
	u, _ := url.Parse(a.Origin)
	a.HTTP.Jar.SetCookies(u, []*http.Cookie{{Name: session.CookieName, Value: sidToken, Path: "/"}})
	if err = a.DeleteAccount(ctx, req, aID); err != nil {
		t.Fatal("receipt retry", err)
	}
	if _, err = a.Me(ctx); err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("me after deletion: %v", err)
	}
	if h.TripmapDB.CityVisit.Query().CountX(ctx) != 1 {
		t.Fatal("other user's visit removed")
	}
	if h.BFFDB.User.Query().CountX(ctx) != 1 {
		t.Fatal("bff user row for B should remain")
	}
	if h.BFFDB.Session.Query().Where(bffsession.UserID(aID)).CountX(ctx) != 0 {
		t.Fatal("bff sessions for A should be gone")
	}
}

func TestCloudDataDeletionRequiresRecentSessionAndPreservesLogtoLogin(t *testing.T) {
	h := NewChainHarness(t)
	ctx := t.Context()
	b := h.FreshBrowser()
	id, err := b.Register(ctx, "same-logto-subject", "", "Traveler")
	if err != nil {
		t.Fatal(err)
	}
	token, hash, err := session.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	h.BFFDB.Session.Create().SetUserID(id).SetTokenHash(hash).
		SetCreatedAt(time.Now().Add(-16 * time.Minute)).SetExpiresAt(time.Now().Add(time.Hour)).SaveX(ctx)
	u, _ := url.Parse(b.Origin)
	b.HTTP.Jar.SetCookies(u, []*http.Cookie{{Name: session.CookieName, Value: token, Path: "/"}})
	if err := b.DeleteAccount(ctx, &bffv1.DeleteAccountRequest{Confirmation: "DELETE"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("old BFF session accepted: %v", err)
	}
	if h.BFFDB.User.GetX(ctx, id).DeletionRequestedAt != nil {
		t.Fatal("freshness rejection started deletion")
	}
	if err := b.Login(ctx, "same-logto-subject", "", b.Origin); err != nil {
		t.Fatal(err)
	}
	if err := b.DeleteAccount(ctx, &bffv1.DeleteAccountRequest{Confirmation: "DELETE"}); err != nil {
		t.Fatal(err)
	}
	// The same Logto subject remains usable, but receives a new empty mapping.
	newID, err := b.Register(ctx, "same-logto-subject", "", "Traveler")
	if err != nil || newID == id {
		t.Fatalf("new mapping after cloud deletion: %q %v", newID, err)
	}
	fp, err := b.GetFootprints(ctx)
	if err != nil || len(fp.Visits) != 0 || len(fp.ProvinceMarks) != 0 {
		t.Fatalf("new business profile is not empty: %v %v", fp, err)
	}
}

func (b *Browser) DeleteAccount(ctx context.Context, msg *bffv1.DeleteAccountRequest, expected ...string) error {
	csrf := b.refreshCSRF(ctx)
	req := connect.NewRequest(msg)
	b.writeHeaders(req, csrf, b.Origin)
	if len(expected) > 0 {
		req.Header().Set("X-Kongken-User", expected[0])
	}
	_, err := b.Auth.DeleteAccount(ctx, req)
	return err
}
