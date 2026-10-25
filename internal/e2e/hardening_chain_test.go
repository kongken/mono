package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/rpcauth"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	bfftripmapv1connect "github.com/kongken/mono/pkg/proto/bff/tripmap/v1/bfftripmapv1connect"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	statusGRPCMethod = "/tripmap.v1.SystemService/GetStatus"
	syncGRPCMethod   = "/tripmap.v1.FootprintService/SyncChanges"
)

func TestChainConnectProtocolVersionRequired(t *testing.T) {
	h := NewChainHarness(t)
	req, err := http.NewRequest(http.MethodPost, h.BFFURL+"/api/bff.tripmap.v1.StatusService/GetStatus", strings.NewReader("{}"))
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

func TestChainRejectsGRPCAndGRPCWeb(t *testing.T) {
	h := NewChainHarness(t)
	for name, ct := range map[string]string{"grpc": "application/grpc", "grpc-web": "application/grpc-web+proto"} {
		req, _ := http.NewRequest(http.MethodPost, h.BFFURL+"/api/bff.tripmap.v1.StatusService/GetStatus", strings.NewReader("{}"))
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Connect-Protocol-Version", "1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatalf("%s: HTTP %d, want 415", name, resp.StatusCode)
		}
	}
}

func TestChainAnonymousFootprintDenied(t *testing.T) {
	h := NewChainHarness(t)
	_, err := h.FreshBrowser().GetFootprints(context.Background())
	if err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous read: %v", err)
	}
}

func TestChainRequestIDAndTraceparent(t *testing.T) {
	spy := &downstreamSpy{}
	h := NewChainHarness(t, []grpc.UnaryServerInterceptor{spy.interceptor()})
	const tp = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	req, _ := http.NewRequest(http.MethodPost, h.BFFURL+"/api/bff.tripmap.v1.StatusService/GetStatus", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("traceparent", tp)
	req.Header.Set("X-Request-Id", "bad id!")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status HTTP %d", resp.StatusCode)
	}
	if id := resp.Header.Get("X-Request-Id"); len(id) != 32 {
		t.Fatalf("invalid client request id replaced: %q", id)
	}
	seen, ok := spy.lastMethod(statusGRPCMethod)
	if !ok {
		t.Fatal("no downstream GetStatus")
	}
	if seen.call.TraceParent != tp {
		t.Fatalf("traceparent downstream: %q", seen.call.TraceParent)
	}
}

func TestLegitimatePayloadLimitsAreConsistent(t *testing.T) {
	if grpcx.LegitimatePayloadBytes != 16*1024*1024 {
		t.Fatalf("LegitimatePayloadBytes = %d", grpcx.LegitimatePayloadBytes)
	}
	if grpcx.DefaultMaxMessageBytes < grpcx.LegitimatePayloadBytes {
		t.Fatal("internal gRPC max below public cap")
	}
	// The sync batch (up to 16 MiB) travels to the API host, the one origin that carries the session: its gateway
	// must accept the whole batch. The trip-map image no longer proxies the browser API (only share, unsubscribe,
	// tile and probe paths, all small GETs), so its template needs no body limit of that size.
	caddy, err := os.ReadFile("../../deploy/reverse-proxy/Caddyfile.example")
	if err != nil {
		t.Fatal(err)
	}
	api := string(caddy)
	start := strings.Index(api, "\napi.example.com {")
	if start < 0 {
		t.Fatal("the Caddyfile example has no API site")
	}
	m := regexp.MustCompile(`max_size (\d+)MB`).FindStringSubmatch(api[start:])
	if m == nil {
		t.Fatal("the API site of the Caddyfile example sets no request_body max_size")
	}
	mb, _ := strconv.Atoi(m[1])
	if int64(mb)*1024*1024 < grpcx.LegitimatePayloadBytes {
		t.Fatalf("the API site accepts %d MB bodies, below the public cap of %d bytes", mb, grpcx.LegitimatePayloadBytes)
	}
	raw, err := os.ReadFile("../../deploy/trip-map-web/default.conf.template")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`location \^~ /api/ \{ return 404; \}`).Match(raw) {
		t.Fatal("the trip-map image must answer the retired same-origin /api/ with 404")
	}
}

func migrationBatch(t *testing.T) []*bfftripmapv1.FootprintMutation {
	t.Helper()
	data, err := os.ReadFile("../../internal/tripmap/regions/regions.json")
	if err != nil {
		t.Fatal(err)
	}
	var list []struct {
		Code   string `json:"code"`
		Cities []struct {
			Code string `json:"code"`
			Name string `json:"name"`
		} `json:"cities"`
	}
	if err = json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	var out []*bfftripmapv1.FootprintMutation
	for _, p := range list {
		out = append(out, &bfftripmapv1.FootprintMutation{Operation: &bfftripmapv1.FootprintMutation_UpsertMark{
			UpsertMark: &bfftripmapv1.ProvinceMark{Province: p.Code, Color: "#4b937d"},
		}})
		for _, c := range p.Cities {
			out = append(out, &bfftripmapv1.FootprintMutation{Operation: &bfftripmapv1.FootprintMutation_UpsertCity{
				UpsertCity: &bfftripmapv1.Visit{Code: c.Code, Name: c.Name, Province: p.Code, Color: "#e2af5c"},
			}})
		}
	}
	if len(out) == 0 || len(out) > 2000 {
		t.Fatalf("region batch size %d", len(out))
	}
	return out
}

func TestChainMaxLegitimateSyncBatchPersists(t *testing.T) {
	spy := &downstreamSpy{}
	h := NewChainHarness(t, []grpc.UnaryServerInterceptor{spy.interceptor()})
	ctx := context.Background()
	u := h.FreshBrowser()
	if _, err := u.Register(ctx, "migrate_chain", "password1", "Migrate"); err != nil {
		t.Fatal(err)
	}
	batch := migrationBatch(t)
	csrf := u.refreshCSRF(ctx)
	req := connect.NewRequest(&bfftripmapv1.SyncChangesRequest{MutationId: "chain-max-batch", BaseRevision: 0, Mutations: batch})
	req.Header().Set("Connect-Timeout-Ms", "30000")
	u.writeHeaders(req, csrf, u.Origin)
	res, err := u.Foot.SyncChanges(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if int(res.Msg.GetChangedCount()) != len(batch) {
		t.Fatalf("changed %d want %d", res.Msg.GetChangedCount(), len(batch))
	}
	fp, err := u.GetFootprints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fp.GetVisits())+len(fp.GetProvinceMarks()) != len(batch) {
		t.Fatalf("persisted count mismatch")
	}
	ev, ok := spy.lastMethod(syncGRPCMethod)
	if !ok {
		t.Fatal("no SyncChanges downstream call")
	}
	if d := ev.deadline; d < 20*time.Second || d > 30*time.Second {
		t.Fatalf("SyncChanges deadline %v, want about 25s", d)
	}
}

func TestChainSyncBatchCountOverLimitRejected(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	u := h.FreshBrowser()
	if _, err := u.Register(ctx, "too_many", "password1", "Many"); err != nil {
		t.Fatal(err)
	}
	batch := migrationBatch(t)
	for len(batch) <= 2000 {
		batch = append(batch, batch...)
	}
	csrf := u.refreshCSRF(ctx)
	req := connect.NewRequest(&bfftripmapv1.SyncChangesRequest{MutationId: "too-many", BaseRevision: 0, Mutations: batch[:2001]})
	u.writeHeaders(req, csrf, u.Origin)
	_, err := u.Foot.SyncChanges(ctx, req)
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("2001 mutations: %v", err)
	}
}

func TestChainConnectBodyOverReadMaxRejected(t *testing.T) {
	h := NewChainHarness(t)
	body := bytes.Repeat([]byte("x"), grpcx.LegitimatePayloadBytes+1)
	req, _ := http.NewRequest(http.MethodPost, h.BFFURL+"/api/bff.tripmap.v1.StatusService/GetStatus", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusRequestEntityTooLarge, http.StatusBadRequest, http.StatusServiceUnavailable, http.StatusTooManyRequests:
	default:
		t.Fatalf("oversized Connect body: HTTP %d", resp.StatusCode)
	}
}

func TestChainDeadlineBudgets(t *testing.T) {
	spy := &downstreamSpy{}
	h := NewChainHarness(t, []grpc.UnaryServerInterceptor{spy.interceptor()})
	ctx := context.Background()
	client := bfftripmapv1connect.NewStatusServiceClient(http.DefaultClient, h.BFFURL+"/api")
	if _, err := client.GetStatus(ctx, connect.NewRequest(&bfftripmapv1.GetStatusRequest{})); err != nil {
		t.Fatal(err)
	}
	ev, _ := spy.lastMethod(statusGRPCMethod)
	if d := ev.deadline; d < 8*time.Second || d > 10*time.Second {
		t.Fatalf("GetStatus deadline %v, want about 10s", d)
	}
	shortCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req := connect.NewRequest(&bfftripmapv1.GetStatusRequest{})
	req.Header().Set("Connect-Timeout-Ms", "3000")
	if _, err := client.GetStatus(shortCtx, req); err != nil {
		t.Fatal(err)
	}
	ev, _ = spy.lastMethod(statusGRPCMethod)
	if d := ev.deadline; d < time.Second || d > 3*time.Second {
		t.Fatalf("short client deadline became %v", d)
	}
	longReq := connect.NewRequest(&bfftripmapv1.GetStatusRequest{})
	longReq.Header().Set("Connect-Timeout-Ms", "60000")
	if _, err := client.GetStatus(ctx, longReq); err != nil {
		t.Fatal(err)
	}
	ev, _ = spy.lastMethod(statusGRPCMethod)
	if d := ev.deadline; d < 8*time.Second || d > 10*time.Second {
		t.Fatalf("client cannot raise above cap: %v", d)
	}
}

func TestChainClientCancellationPropagates(t *testing.T) {
	downstreamBlocked := make(chan struct{})
	block := grpc.UnaryServerInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		if info.FullMethod == statusGRPCMethod {
			close(downstreamBlocked)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return next(ctx, req)
	})
	h := NewChainHarness(t, []grpc.UnaryServerInterceptor{block})
	client := bfftripmapv1connect.NewStatusServiceClient(http.DefaultClient, h.BFFURL+"/api")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.GetStatus(ctx, connect.NewRequest(&bfftripmapv1.GetStatusRequest{}))
		done <- err
	}()
	<-downstreamBlocked
	cancel()
	select {
	case err := <-done:
		if connect.CodeOf(err) != connect.CodeCanceled {
			t.Fatalf("client: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("call hung after cancel")
	}
}

func TestChainNoAutomaticRetryOnUnavailable(t *testing.T) {
	spy := &downstreamSpy{}
	h := NewChainHarness(t, []grpc.UnaryServerInterceptor{spy.interceptor(), failFirstUnavailable(statusGRPCMethod)})
	client := bfftripmapv1connect.NewStatusServiceClient(http.DefaultClient, h.BFFURL+"/api")
	_, err := client.GetStatus(context.Background(), connect.NewRequest(&bfftripmapv1.GetStatusRequest{}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("code %v", err)
	}
	if n := spy.countMethod(statusGRPCMethod); n != 1 {
		t.Fatalf("GetStatus invoked %d times, want 1", n)
	}
}

func TestChainSensitiveValuesStayOutOfLogsAndResponses(t *testing.T) {
	h := NewChainHarness(t)
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)
	ctx := context.Background()
	u := h.FreshBrowser()
	if _, err := u.Register(ctx, "redact_u", "password1", "Redact"); err != nil {
		t.Fatal(err)
	}
	csrf := u.refreshCSRF(ctx)
	sessionTok := u.SessionCookie()
	req := connect.NewRequest(&bfftripmapv1.SyncChangesRequest{MutationId: "redact-1", BaseRevision: 0, Mutations: []*bfftripmapv1.FootprintMutation{{
		Operation: &bfftripmapv1.FootprintMutation_UpsertCity{
			UpsertCity: &bfftripmapv1.Visit{Code: "330100", Name: "杭州市", Province: "330000", Color: "#4b937d"},
		},
	}}})
	u.writeHeaders(req, csrf, u.Origin)
	res, err := u.Foot.SyncChanges(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(res.Msg)
	for _, s := range []string{sessionTok, csrf} {
		if s != "" && (strings.Contains(string(body), s) || strings.Contains(logBuf.String(), s)) {
			t.Fatal("credential leaked to response or logs")
		}
	}
}

func TestChainGrpcOversizedMessageRejected(t *testing.T) {
	h := NewChainHarness(t)
	conn := h.DirectTripmapGRPC()
	tok, err := h.signer.Sign(audience, syncGRPCMethod, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	huge := &tripmapv1.SyncChangesRequest{
		MutationId: "huge",
		Mutations: []*tripmapv1.FootprintMutation{{Operation: &tripmapv1.FootprintMutation_UpsertCity{
			UpsertCity: &tripmapv1.Visit{Code: "330100", Name: "杭", Province: "330000", Color: "#4", Note: strings.Repeat("z", grpcx.DefaultMaxMessageBytes)},
		}}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = conn.Invoke(metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+tok)), syncGRPCMethod, huge, &tripmapv1.SyncChangesResponse{})
	if status.Code(err) != codes.ResourceExhausted && status.Code(err) != codes.InvalidArgument {
		t.Fatalf("oversized grpc: %v", err)
	}
}

func TestChainForgedClientIPIgnored(t *testing.T) {
	spy := &downstreamSpy{}
	h := NewChainHarness(t, []grpc.UnaryServerInterceptor{spy.interceptor()})
	req, _ := http.NewRequest(http.MethodPost, h.BFFURL+"/api/bff.tripmap.v1.StatusService/GetStatus", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set(rpcauth.MetadataClientIP, "198.51.100.9")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	ev, _ := spy.lastMethod(statusGRPCMethod)
	if ev.call.ClientIP == "198.51.100.9" {
		t.Fatalf("forged client IP trusted")
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
	req := connect.NewRequest(&bfftripmapv1.SyncChangesRequest{MutationId: "batch-limit", BaseRevision: 0, Mutations: []*bfftripmapv1.FootprintMutation{{
		Operation: &bfftripmapv1.FootprintMutation_UpsertCity{
			UpsertCity: &bfftripmapv1.Visit{Code: "330100", Name: "杭州市", Province: "330000", Color: "#4b937d"},
		},
	}}})
	u.writeHeaders(req, csrf, u.Origin)
	res, err := u.Foot.SyncChanges(ctx, req)
	if err != nil || res.Msg.GetChangedCount() != 1 {
		t.Fatalf("sync: %v", err)
	}
}
