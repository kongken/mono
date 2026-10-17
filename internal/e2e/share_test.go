package e2e_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/tripmap/config"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	bfftripmapv1connect "github.com/kongken/mono/pkg/proto/bff/tripmap/v1/bfftripmapv1connect"
)

func TestSharePublishAndPublicPageViaBFFChain(t *testing.T) {
	h := NewChainHarness(t, func(trip *config.AppConfig) {
		trip.Share = config.ShareConfig{
			PublicBaseURL: "https://trip.example.com/",
			AppIndexURL:   "https://trip.example.com/index.html",
		}
	})
	b := h.FreshBrowser()
	ctx := context.Background()
	if _, err := b.Register(ctx, "share_owner", "password1", "Owner"); err != nil {
		t.Fatal(err)
	}
	share := bfftripmapv1connect.NewShareServiceClient(b.HTTP, b.APIURL)
	getReq := connect.NewRequest(&bfftripmapv1.GetShareRequest{})
	b.withTripmapHeaders(getReq)
	got, err := share.GetShare(ctx, getReq)
	if err != nil || !got.Msg.GetEnabled() {
		t.Fatalf("get share: err=%v enabled=%v", err, got.Msg.GetEnabled())
	}
	prevReq := connect.NewRequest(&bfftripmapv1.PreviewShareRequest{Settings: &bfftripmapv1.ShareSettings{
		Visibility: "link",
	}})
	b.withTripmapHeaders(prevReq)
	prev, err := share.PreviewShare(ctx, prevReq)
	if err != nil {
		t.Fatal(err)
	}
	saveReq := connect.NewRequest(&bfftripmapv1.SaveShareRequest{
		MutationId: "pub-1", Settings: &bfftripmapv1.ShareSettings{Visibility: "link"},
		RefreshContent: true, PreviewRevision: prev.Msg.GetFootprintRevision(),
	})
	csrf := b.refreshCSRF(ctx)
	b.writeHeaders(saveReq, csrf, b.Origin)
	saved, err := share.SaveShare(ctx, saveReq)
	if err != nil || !saved.Msg.GetShare().GetPublished() || saved.Msg.GetShare().GetUrl() == "" {
		t.Fatalf("save share: err=%v published=%v url=%q", err, saved.Msg.GetShare().GetPublished(), saved.Msg.GetShare().GetUrl())
	}
	token := strings.TrimPrefix(saved.Msg.GetShare().GetUrl(), "https://trip.example.com/s/")
	anon := &http.Client{}
	page, err := anon.Get(h.BFFURL + "/s/" + token)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()
	if page.StatusCode != http.StatusOK || !strings.Contains(string(body), `property="og:title"`) {
		t.Fatalf("public page: %d %s", page.StatusCode, body)
	}
	data, err := anon.Get(h.BFFURL + "/api/share/" + token + "/data")
	if err != nil {
		t.Fatal(err)
	}
	dataBody, _ := io.ReadAll(data.Body)
	_ = data.Body.Close()
	if data.StatusCode != http.StatusOK || !strings.Contains(string(dataBody), `"v"`) {
		t.Fatalf("public data: %d %s", data.StatusCode, dataBody)
	}
	revReq := connect.NewRequest(&bfftripmapv1.RevokeShareRequest{MutationId: "rev-1"})
	csrf = b.refreshCSRF(ctx)
	b.writeHeaders(revReq, csrf, b.Origin)
	if _, err := share.RevokeShare(ctx, revReq); err != nil {
		t.Fatal(err)
	}
	gone, err := anon.Get(h.BFFURL + "/s/" + token)
	if err != nil {
		t.Fatal(err)
	}
	_ = gone.Body.Close()
	if gone.StatusCode != http.StatusNotFound {
		t.Fatalf("revoked link: %d", gone.StatusCode)
	}
}
