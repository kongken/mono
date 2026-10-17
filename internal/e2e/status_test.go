package e2e_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

func TestAnonymousBrowserReadsRealTripmapStatus(t *testing.T) {
	s := newStack(t)
	res, err := s.status.GetStatus(context.Background(), connect.NewRequest(&bffv1.GetStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	got := res.Msg
	if !got.Ready || got.FootprintSchema < 2 || got.RegionVersion == "" || got.ShareEnabled {
		t.Fatalf("status from the real tripmap: %v", got)
	}
	names := map[string]bool{}
	for _, d := range got.Dependencies {
		names[d.Name] = d.Ready
	}
	if len(names) != 3 || !names["postgres"] || !names["redis"] || !names["s3"] {
		t.Fatalf("dependencies: %v", got.Dependencies)
	}
}

func TestDependencyFailuresAreReportedThroughTheChain(t *testing.T) {
	s := newStack(t)
	s.failing["redis"].Store(true)
	res, err := s.status.GetStatus(context.Background(), connect.NewRequest(&bffv1.GetStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	ready := map[string]bool{}
	for _, d := range res.Msg.Dependencies {
		ready[d.Name] = d.Ready
	}
	if res.Msg.Ready || ready["redis"] || !ready["postgres"] || !ready["s3"] {
		t.Fatalf("one dependency down: ready=%v deps=%v", res.Msg.Ready, ready)
	}
	s.failing["redis"].Store(false)
	if res, err = s.status.GetStatus(context.Background(), connect.NewRequest(&bffv1.GetStatusRequest{})); err != nil || !res.Msg.Ready {
		t.Fatalf("recovered: %v %v", res, err)
	}

	// With tripmap itself gone the browser gets a clean UNAVAILABLE, not a stale status.
	s.tripmap.StopGRPC()
	_, err = s.status.GetStatus(context.Background(), connect.NewRequest(&bffv1.GetStatusRequest{}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("tripmap stopped: %v", err)
	}
}

func TestOldPublicProcedureIsGone(t *testing.T) {
	s := newStack(t)
	res, err := s.bff.Client().Post(s.bff.URL+"/api/tripmap.v1.SystemService/GetStatus", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("tripmap.v1.SystemService is still reachable from the browser: %d", res.StatusCode)
	}
}
