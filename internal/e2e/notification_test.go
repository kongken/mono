package e2e_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/tripmap/config"
	"github.com/kongken/mono/internal/tripmap/notification"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	bfftripmapv1connect "github.com/kongken/mono/pkg/proto/bff/tripmap/v1/bfftripmapv1connect"
)

func TestNotificationSettingsViaBFFChain(t *testing.T) {
	h := NewChainHarness(t)
	b := h.FreshBrowser()
	ctx := context.Background()
	if _, err := b.Register(ctx, "notify_user", "password1", "Notify"); err != nil {
		t.Fatal(err)
	}
	notify := bfftripmapv1connect.NewNotificationServiceClient(b.HTTP, b.APIURL)
	req := connect.NewRequest(&bfftripmapv1.GetNotificationSettingsRequest{})
	b.withTripmapHeaders(req)
	res, err := notify.GetNotificationSettings(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Msg.GetSettings() == nil || len(res.Msg.GetSettings().GetPreferences()) == 0 {
		t.Fatal("expected default preferences")
	}
}

func TestEmailUnsubscribeViaBFFPublicHTTP(t *testing.T) {
	secret := strings.Repeat("u", 32)
	h := NewChainHarness(t, func(trip *config.AppConfig) {
		trip.Notification = config.NotificationConfig{
			PublicBaseURL: "https://trip.example.com",
			Secret:        secret,
			SMTP:          config.SMTPConfig{Host: "smtp.test", From: "noreply@example.com"},
		}
	})
	b := h.FreshBrowser()
	ctx := context.Background()
	userID, err := b.Register(ctx, "unsub_chain", "password1", "Unsub")
	if err != nil {
		t.Fatal(err)
	}
	token := notification.SignUnsubscribe(secret, notification.Unsubscribe{UserID: userID, Kind: notification.AllKinds})

	getURL := h.BFFURL + "/api/notifications/unsubscribe?t=" + url.QueryEscape(token)
	rec, err := b.HTTP.Get(getURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rec.Body)
	_ = rec.Body.Close()
	if rec.StatusCode != http.StatusOK || !strings.Contains(string(body), `method="post"`) {
		t.Fatalf("GET preview: %d %s", rec.StatusCode, body)
	}

	postReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, getURL, strings.NewReader("List-Unsubscribe=One-Click"))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postRec, err := b.HTTP.Do(postReq)
	if err != nil {
		t.Fatal(err)
	}
	postBody, _ := io.ReadAll(postRec.Body)
	_ = postRec.Body.Close()
	if postRec.StatusCode != http.StatusOK || !strings.Contains(string(postBody), "已退订") {
		t.Fatalf("POST confirm: %d %s", postRec.StatusCode, postBody)
	}
}
