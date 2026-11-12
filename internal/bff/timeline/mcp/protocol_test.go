package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/kongken/mono/internal/bff/accesstoken"
	"github.com/kongken/mono/internal/bff/usersrv"
	timelinev1 "github.com/kongken/mono/pkg/proto/timeline/v1"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// The protocol test drives the real Streamable HTTP endpoint with the official
// SDK client, so negotiation, discovery and one tool call are exercised over the
// wire rather than through the internal helpers.

type protocolTokens struct{}

func (protocolTokens) Authenticate(context.Context, string, string) (accesstoken.Principal, error) {
	return accesstoken.Principal{
		TokenID:     "tok_1",
		UserID:      "user_1",
		Audience:    accesstoken.AudienceTimeline,
		AuthVersion: 1,
		Scopes:      []string{accesstoken.ScopeTimelineRead, accesstoken.ScopeTimelineWrite},
	}, nil
}

type protocolAccounts struct{}

func (protocolAccounts) AuthState(context.Context, string) (usersrv.AuthState, error) {
	return usersrv.AuthState{Status: usersrv.StatusActive, AuthVersion: 1}, nil
}

type protocolLimiter struct{}

func (protocolLimiter) Allow(context.Context, string, int64, time.Duration) (bool, error) {
	return true, nil
}

type protocolPerimeter struct{}

func (protocolPerimeter) Allowed(http.Header, string) (netip.Addr, bool) {
	return netip.MustParseAddr("10.0.0.9"), true
}

// The stub clients embed the generated interfaces so only the calls under test
// need an implementation; any other call is a bug the test would panic on.
type protocolTimelineClient struct {
	timelinev1.TimelineServiceClient
}

type protocolMediaClient struct{ timelinev1.MediaServiceClient }

func (protocolMediaClient) GetMediaDownloadUrl(context.Context, *timelinev1.GetMediaDownloadUrlRequest, ...grpc.CallOption) (*timelinev1.GetMediaDownloadUrlResponse, error) {
	return &timelinev1.GetMediaDownloadUrlResponse{
		Url:         "https://objects.example.com/bkt/med_1?X-Amz-Signature=abc",
		ExpiresAt:   timestamppb.New(time.Now().UTC().Add(5 * time.Minute)),
		ContentType: "image/jpeg",
		Filename:    "photo.jpg",
	}, nil
}

type bearerTransport struct{ token string }

func (t bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(clone)
}

func newProtocolEndpoint(t *testing.T) *httptest.Server {
	t.Helper()
	s := New(Config{
		Admin:     protocolTimelineClient{},
		Media:     protocolMediaClient{},
		Tokens:    protocolTokens{},
		Accounts:  protocolAccounts{},
		Limiter:   protocolLimiter{},
		Perimeter: protocolPerimeter{},
	})
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return srv
}

func TestMCPStreamableHTTPExposesDownloadAddressNotRawBytes(t *testing.T) {
	srv := newProtocolEndpoint(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "timeline-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint:             srv.URL + Path,
		HTTPClient:           &http.Client{Transport: bearerTransport{token: "secret"}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	if session.InitializeResult().ProtocolVersion == "" {
		t.Fatal("initialize negotiated no protocol version")
	}

	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var sawDownload, sawRawBytes bool
	for _, tool := range list.Tools {
		switch tool.Name {
		case "timeline_get_attachment_download":
			sawDownload = true
		case "timeline_read_attachment":
			sawRawBytes = true
		}
	}
	if !sawDownload {
		t.Fatal("tools/list did not advertise timeline_get_attachment_download")
	}
	if sawRawBytes {
		t.Fatal("tools/list advertised a raw attachment byte tool")
	}

	call, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "timeline_get_attachment_download",
		Arguments: map[string]any{"mediaRef": "med_1"},
	})
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	if call.IsError {
		t.Fatalf("tools/call returned an error result: %+v", call.Content)
	}
	if len(call.Content) == 0 {
		t.Fatal("tools/call returned no content")
	}
	text, ok := call.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want text", call.Content[0])
	}
	if !strings.Contains(text.Text, "https://objects.example.com/bkt/med_1") {
		t.Fatalf("download result did not carry the address: %s", text.Text)
	}
	if !strings.Contains(text.Text, "photo.jpg") || !strings.Contains(text.Text, "image/jpeg") {
		t.Fatalf("download result lost attachment metadata: %s", text.Text)
	}
}
