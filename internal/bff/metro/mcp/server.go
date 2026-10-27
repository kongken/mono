// Package mcp serves the metro management tools over MCP (Model Context
// Protocol, Streamable HTTP, stateless) at POST /mcp/metro.
//
// Authentication and the machine source perimeter remain BFF concerns. Once a
// request is admitted, the official modelcontextprotocol/go-sdk owns the MCP
// JSON-RPC and Streamable HTTP protocol, while each SDK tool handler delegates
// to the same metro AdminService method used by the web admin.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/kongken/mono/internal/bff/accesstoken"
	"github.com/kongken/mono/internal/bff/usersrv"
	"github.com/kongken/mono/internal/platform/rpcauth"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Path is the public route of the endpoint.
const Path = "/mcp/metro"

const (
	// DefaultRatePerMinute is the per-token request limit when none is configured.
	DefaultRatePerMinute = 60
	// maxBodyBytes bounds one request: a 1 MiB catalogue document plus JSON
	// escaping and the envelope.
	maxBodyBytes = 3 << 20
	// failedAuthPerMinute bounds the rejected attempts one client address may make.
	failedAuthPerMinute = 30
	callTimeout         = 10 * time.Second
	heavyCallTimeout    = 25 * time.Second
)

// SDK protocol versions retained by the endpoint's existing contract.
var supportedVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

type requestAuth struct {
	principal accesstoken.Principal
	clientIP  string
}

type requestAuthKey struct{}

// Authenticator verifies a presented token.
type Authenticator interface {
	Authenticate(ctx context.Context, secret, audience string) (accesstoken.Principal, error)
}

// Accounts is the account state usersrv holds. Every tool call asks it, so a
// token never outlives its owner's account, password version or role.
type Accounts interface {
	AuthState(ctx context.Context, userID string) (usersrv.AuthState, error)
}

// AdminRole is the role every tool of this endpoint needs.
const AdminRole = "metro-admin"

// Limiter is a fixed-window counter shared across BFF replicas.
type Limiter interface {
	Allow(ctx context.Context, key string, limit int64, window time.Duration) (bool, error)
}

// Perimeter decides, from the request alone, whether the caller's network may use the endpoint. Allowed
// returns the resolved client address when it may. It is the machine allowlist: it has nothing to do
// with the browser console's.
type Perimeter interface {
	Allowed(h http.Header, remoteAddr string) (netip.Addr, bool)
}

// Config wires a Server.
type Config struct {
	Admin    metrov1.AdminServiceClient
	Tokens   Authenticator
	Accounts Accounts
	Limiter  Limiter
	// RatePerMinute is the per-token limit; zero means DefaultRatePerMinute.
	RatePerMinute int
	// Perimeter is checked first, for every request of every kind. A nil Perimeter admits nobody.
	Perimeter Perimeter
}

// Server is the authenticated BFF wrapper around the SDK's stateless MCP handler.
type Server struct {
	cfg     Config
	tools   []*tool
	handler http.Handler
}

// New returns the endpoint handler.
func New(cfg Config) *Server {
	if cfg.RatePerMinute <= 0 {
		cfg.RatePerMinute = DefaultRatePerMinute
	}
	s := &Server{cfg: cfg, tools: tools()}
	s.handler = sdkmcp.NewStreamableHTTPHandler(func(r *http.Request) *sdkmcp.Server {
		auth, ok := r.Context().Value(requestAuthKey{}).(requestAuth)
		if !ok {
			return nil
		}
		return s.sdkServer(auth)
	}, &sdkmcp.StreamableHTTPOptions{
		Stateless:           true,
		JSONResponse:        true,
		MaxRequestBodyBytes: maxBodyBytes,
	})
	return s
}

// ServeHTTP authenticates and rate-limits the machine caller, then delegates
// MCP protocol parsing, negotiation, batching and notifications to the SDK.
// Cookies are never read and no CORS headers are set.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var ip netip.Addr
	var admitted bool
	if s.cfg.Perimeter != nil {
		ip, admitted = s.cfg.Perimeter.Allowed(r.Header, r.RemoteAddr)
	}
	if !admitted {
		http.Error(w, "request rejected", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	clientIP := ip.String()
	principal, status := s.authenticate(ctx, r, clientIP)
	if status != 0 {
		s.reject(w, status)
		return
	}
	if ok, err := s.cfg.Limiter.Allow(ctx, "mcp:"+principal.TokenID, int64(s.cfg.RatePerMinute), time.Minute); err != nil {
		slog.Error("mcp rate limit", "error", err)
		s.reject(w, http.StatusServiceUnavailable)
		return
	} else if !ok {
		s.reject(w, http.StatusTooManyRequests)
		return
	}
	if media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || media != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	ctx = context.WithValue(ctx, requestAuthKey{}, requestAuth{principal: principal, clientIP: clientIP})
	// The SDK's Streamable HTTP transport requires both response media types in
	// Accept, while the previous endpoint accepted an omitted or JSON-only
	// header. Supply the SDK-compatible value for legacy MCP clients.
	accept := r.Header.Get("Accept")
	if !strings.Contains(accept, "text/event-stream") {
		r.Header.Set("Accept", "application/json, text/event-stream")
	}
	s.handler.ServeHTTP(w, r.WithContext(ctx))
}

func (s *Server) sdkServer(auth requestAuth) *sdkmcp.Server {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "kongken-metro", Version: "1"}, &sdkmcp.ServerOptions{
		Instructions:              "Manage the metro catalogue (cities, lines, rolling stock, topics). Read an entity before changing it and pass its revision as expectedRevision. metro_import_catalog only previews unless dryRun is explicitly false.",
		Capabilities:              &sdkmcp.ServerCapabilities{Tools: &sdkmcp.ToolCapabilities{ListChanged: false}},
		SupportedProtocolVersions: supportedVersions,
	})
	for _, t := range s.tools {
		if !t.allowedFor(auth.principal) {
			continue
		}
		toolDef := &sdkmcp.Tool{
			Name:        t.name,
			Description: t.description,
			InputSchema: t.schema(),
			Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: t.readOnly, DestructiveHint: boolPtr(t.destructive)},
		}
		server.AddTool(toolDef, s.sdkToolHandler(t, auth))
	}
	return server
}

func boolPtr(v bool) *bool { return &v }

func (s *Server) sdkToolHandler(t *tool, auth requestAuth) sdkmcp.ToolHandler {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		roles, terr := s.effectiveRoles(ctx, auth.principal)
		if terr != nil {
			return textResult(terr.text(), true), nil
		}
		limit := callTimeout
		if t.heavy {
			limit = heavyCallTimeout
		}
		ctx, cancel := context.WithTimeout(ctx, limit)
		defer cancel()
		ctx = rpcauth.WithCall(ctx, rpcauth.Call{
			UserID: auth.principal.UserID, Roles: roles, Channel: rpcauth.ChannelMCP,
			TokenID: auth.principal.TokenID, RequestID: rpcauth.NewRequestID(), ClientIP: auth.clientIP,
		})
		args := req.Params.Arguments
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		out, terr := s.run(ctx, t, args)
		slog.Info("mcp tool call", "tool", t.name, "token_id", auth.principal.TokenID, "user_id", auth.principal.UserID, "error", terr != nil)
		if terr != nil {
			return textResult(terr.text(), true), nil
		}
		return textResult(out, false), nil
	}
}

func textResult(text string, isError bool) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: text}}, IsError: isError}
}

// authenticate resolves the Bearer token; a non-zero status is the HTTP rejection.
func (s *Server) authenticate(ctx context.Context, r *http.Request, clientIP string) (accesstoken.Principal, int) {
	secret := bearer(r.Header.Get("Authorization"))
	if secret != "" {
		p, err := s.cfg.Tokens.Authenticate(ctx, secret, accesstoken.AudienceMetro)
		if err == nil {
			return p, 0
		}
		if !errors.Is(err, accesstoken.ErrInvalidToken) {
			slog.Error("mcp token lookup", "error", err)
			return accesstoken.Principal{}, http.StatusServiceUnavailable
		}
	}
	if ok, err := s.cfg.Limiter.Allow(ctx, "mcp-denied:"+clientIP, failedAuthPerMinute, time.Minute); err == nil && !ok {
		return accesstoken.Principal{}, http.StatusTooManyRequests
	}
	return accesstoken.Principal{}, http.StatusUnauthorized
}

func (s *Server) reject(w http.ResponseWriter, status int) {
	switch status {
	case http.StatusUnauthorized:
		w.Header().Set("WWW-Authenticate", `Bearer realm="metro-mcp"`)
		http.Error(w, "a valid management token is required", status)
	case http.StatusTooManyRequests:
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limit exceeded", status)
	default:
		http.Error(w, "service unavailable", status)
	}
}

func bearer(header string) string {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// effectiveRoles asks usersrv about the token's owner for this very call.
func (s *Server) effectiveRoles(ctx context.Context, p accesstoken.Principal) ([]string, *toolError) {
	state, err := s.cfg.Accounts.AuthState(ctx, p.UserID)
	if err != nil {
		if errors.Is(err, usersrv.ErrNotFound) {
			return nil, &toolError{Code: "UNAUTHENTICATED", Message: "this token is no longer valid"}
		}
		slog.Error("mcp account check", "error", err)
		return nil, &toolError{Code: "UNAVAILABLE", Message: "the account service is unavailable; retry"}
	}
	if state.Status != usersrv.StatusActive || state.AuthVersion != p.AuthVersion {
		return nil, &toolError{Code: "UNAUTHENTICATED", Message: "this token is no longer valid"}
	}
	var roles []string
	for _, r := range p.Roles {
		if slices.Contains(state.Roles, r) {
			roles = append(roles, r)
		}
	}
	if !slices.Contains(roles, AdminRole) {
		return nil, &toolError{Code: "PERMISSION_DENIED", Message: "the token's owner no longer holds the " + AdminRole + " role"}
	}
	return roles, nil
}

var marshalOpts = protojson.MarshalOptions{}

func (s *Server) run(ctx context.Context, t *tool, args json.RawMessage) (string, *toolError) {
	if t.custom != nil {
		res, terr := t.custom(ctx, s.cfg.Admin, args)
		if terr != nil {
			return "", terr
		}
		return marshalResult(res)
	}
	raw := map[string]json.RawMessage{}
	if trimmed := strings.TrimSpace(string(args)); trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal(args, &raw); err != nil {
			return "", badArguments(errors.New("arguments must be a JSON object"))
		}
	}
	if t.dryRunDefaultsTrue {
		if _, camel := raw["dryRun"]; !camel {
			if _, snake := raw["dry_run"]; !snake {
				raw["dryRun"] = json.RawMessage("true")
			}
		}
	}
	buf, _ := json.Marshal(raw)
	req := t.newReq()
	if err := (protojson.UnmarshalOptions{}).Unmarshal(buf, req); err != nil {
		return "", badArguments(err)
	}
	if t.requireRevision {
		if fd := field(req, "expected_revision"); fd == nil || req.ProtoReflect().Get(fd).Int() <= 0 {
			return "", &toolError{Code: "INVALID_ARGUMENT", Message: "expectedRevision is required: read the entity first and pass its current revision"}
		}
	}
	res, err := t.call(ctx, s.cfg.Admin, req)
	if err != nil {
		return "", fromRPCError(err)
	}
	return marshalResult(res)
}

func marshalResult(v any) (string, *toolError) {
	if m, ok := v.(proto.Message); ok {
		b, err := marshalOpts.Marshal(m)
		if err != nil {
			return "", &toolError{Code: "INTERNAL", Message: "could not encode the result"}
		}
		return string(b), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", &toolError{Code: "INTERNAL", Message: "could not encode the result"}
	}
	return string(b), nil
}
