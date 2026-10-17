package e2e_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

func TestChainConnectProtocolVersionRequired(t *testing.T) {
	h := NewChainHarness(t)
	req, err := http.NewRequest(http.MethodPost, h.BFFURL+"/api/bff.v1.StatusService/GetStatus", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing Connect-Protocol-Version: HTTP %d", resp.StatusCode)
	}
}

func TestChainAnonymousFootprintDenied(t *testing.T) {
	h := NewChainHarness(t)
	b := h.FreshBrowser()
	_, err := b.GetFootprints(context.Background())
	if err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous read: %v", err)
	}
}

func TestChainRequestIDOnSuccess(t *testing.T) {
	h := NewChainHarness(t)
	req, _ := http.NewRequest(http.MethodPost, h.BFFURL+"/api/bff.v1.StatusService/GetStatus", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status HTTP %d", resp.StatusCode)
	}
	if id := resp.Header.Get("X-Request-Id"); len(id) != 32 {
		t.Fatalf("missing request id header: %q", id)
	}
}

func TestChainSyncBatchWithinLimit(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	u := h.FreshBrowser()
	if _, err := u.Register(ctx, "bigbatch", "password1", "Big"); err != nil {
		t.Fatal(err)
	}
	csrf := u.refreshCSRF(ctx)
	req := connect.NewRequest(&bffv1.SyncChangesRequest{MutationId: "batch-limit", BaseRevision: 0, Mutations: []*bffv1.FootprintMutation{{
		Operation: &bffv1.FootprintMutation_UpsertCity{
			UpsertCity: &bffv1.Visit{Code: "330100", Name: "杭州市", Province: "330000", Color: "#4b937d"},
		},
	}}})
	u.writeHeaders(req, csrf, u.Origin)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res, err := u.Foot.SyncChanges(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Msg.GetChangedCount() != 1 {
		t.Fatalf("changed %d want 1", res.Msg.GetChangedCount())
	}
}
