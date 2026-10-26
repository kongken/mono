package e2e_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

// Spec §14 scenario 24 (and §12): the old browser login entry points and the removed OAuth
// RPCs are gone, not redirected to a fallback that still signs anyone in.
func TestChainLegacyLoginPathsAndOAuthRPCsAreGone(t *testing.T) {
	h := NewChainHarness(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/auth/login"},
		{http.MethodGet, "/auth/login?app=trip-map"},
		{http.MethodGet, "/auth/login?app=metro"},
		{http.MethodGet, "/auth/callback/logto?code=x&state=y"},
		{http.MethodGet, "/auth/callback/github?code=x&state=y"},
		{http.MethodPost, "/auth/callback/logto"},
		{http.MethodGet, "/auth/logout"},
		{http.MethodGet, "/api/auth/login"},
	} {
		req, err := http.NewRequest(tc.method, h.BFFURL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s: got %d, want 404", tc.method, tc.path, res.StatusCode)
		}
		if loc := res.Header.Get("Location"); loc != "" {
			t.Errorf("%s %s redirects to %q", tc.method, tc.path, loc)
		}
		if res.Header.Get("Set-Cookie") != "" {
			t.Errorf("%s %s sets a cookie", tc.method, tc.path)
		}
	}

	// The removed RPCs of the old contract (username/password and OAuth) do not exist.
	for _, name := range []string{"Register", "Login", "ListOAuthProviders", "BeginOAuthFlow", "CompleteOAuthFlow"} {
		req, err := http.NewRequest(http.MethodPost, h.BFFURL+"/api/bff.v1.AuthService/"+name, bytes.NewReader([]byte("{}")))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Connect-Protocol-Version", "1")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusNotFound && res.StatusCode != http.StatusNotImplemented {
			t.Errorf("removed RPC %s: got %d %s", name, res.StatusCode, body)
		}
		if res.Header.Get("Set-Cookie") != "" || strings.Contains(string(body), "kongken_sid") {
			t.Errorf("removed RPC %s produced a session", name)
		}
	}
}

// Spec §14 scenario 24: with no Logto service, configuration or credential anywhere in the
// stack, one account goes register -> mail -> verify -> sign in -> product writes -> reset ->
// sign in again -> delete, on a real BFF, usersrv, PostgreSQL, Redis and both product services.
// Only the mail transport is replaced (by the in-process inbox).
func TestChainFullAccountLifecycleWithoutLogto(t *testing.T) {
	h := NewChainHarness(t, ChainMetro{})
	ctx := context.Background()
	const email = "lifecycle@example.com"
	const next = "a second long password"

	// 1. Request registration; nothing exists yet, and the answer carries no token.
	b := h.FreshBrowser()
	if _, err := b.RequestRegistration(ctx, email, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a registration request signed someone in: %v", err)
	}
	if _, err := b.LoginWithEmail(ctx, email, E2EPassword); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("an unverified address could sign in: %v", err)
	}

	// 2. Mail arrives; its link carries the token in the fragment of the app origin.
	h.DeliverMail()
	if got := h.Inbox.To(email); len(got) != 1 {
		t.Fatalf("expected one verification mail, got %d", len(got))
	}
	token := h.Inbox.TokenIn(t, email)

	// 3. Verify: sets the password, creates the account, does not sign in.
	if err := b.CompleteRegistration(ctx, token, E2EPassword, "Lifecycle"); err != nil {
		t.Fatal(err)
	}
	if b.SessionCookie() != "" {
		t.Fatal("verification signed the user in")
	}

	// 4. Sign in, then write in both products.
	login, err := b.LoginWithEmail(ctx, email, E2EPassword)
	if err != nil || login.GetUser().GetId() == "" {
		t.Fatalf("sign in: %v %v", login, err)
	}
	userID := login.GetUser().GetId()
	b.touchTripmap(ctx)
	sync := connect.NewRequest(&bfftripmapv1.SyncChangesRequest{
		MutationId: "lifecycle-1", BaseRevision: 0,
		Mutations: []*bfftripmapv1.FootprintMutation{{
			Operation: &bfftripmapv1.FootprintMutation_UpsertCity{
				UpsertCity: &bfftripmapv1.Visit{Code: "330100", Name: "杭州市", Province: "330000", Color: "#4b937d"},
			},
		}},
	})
	b.writeHeaders(sync, b.refreshCSRF(ctx), b.Origin)
	if res, err := b.Foot.SyncChanges(ctx, sync); err != nil || res.Msg.GetChangedCount() != 1 {
		t.Fatalf("tripmap write: %v %v", res, err)
	}
	sh := h.MetroDB.City.Create().SetSlug("shanghai").SetName("上海").SaveX(ctx)
	h.MetroDB.Line.Create().SetCode("sh-1").SetCityID(sh.ID).SetName("1号线").SetNumber(1).SetLengthKm(36.9).SetColor("#E4002B").SaveX(ctx)
	if _, err = b.MetroRider.MarkLine(ctx, riderWrite(b, &bffmetrov1.MarkLineRequest{LineCode: "sh-1"})); err != nil {
		t.Fatalf("metro write: %v", err)
	}

	// 5. Forgot the password: reset from a mail, which ends the session and does not sign in.
	anon := h.FreshBrowser()
	if _, err = anon.RequestPasswordReset(ctx, email, ""); err != nil {
		t.Fatal(err)
	}
	h.DeliverMail()
	if err = anon.ResetPassword(ctx, h.Inbox.TokenIn(t, email), next); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("the old session survived the reset: %v", err)
	}

	// 6. Sign in again with the new password; the data written before is still there.
	if _, err = b.LoginWithEmail(ctx, email, E2EPassword); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("the old password works after the reset: %v", err)
	}
	if _, err = b.LoginWithEmail(ctx, email, next); err != nil {
		t.Fatal(err)
	}
	fp, err := b.GetFootprints(ctx)
	if err != nil || len(fp.GetVisits()) != 1 {
		t.Fatalf("footprints after sign in again: %v %v", fp, err)
	}
	mine, err := b.MetroRider.ListMyLines(ctx, connect.NewRequest(&bffmetrov1.ListMyLinesRequest{}))
	if err != nil || len(mine.Msg.GetCities()) != 1 {
		t.Fatalf("metro data after sign in again: %v %v", mine, err)
	}

	// 7. Delete the account: needs the password again, then everything is gone.
	if err = b.Reauthenticate(ctx, next); err != nil {
		t.Fatal(err)
	}
	if err = b.DeleteAccount(ctx, &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "lifecycle-delete"}, userID); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	if _, err = h.FreshBrowser().LoginWithEmail(ctx, email, next); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a deleted account signed in: %v", err)
	}
	if n := h.TripmapDB.CityVisit.Query().CountX(ctx); n != 0 {
		t.Fatalf("tripmap kept %d visits of a deleted account", n)
	}
}
