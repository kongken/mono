// Package mcp serves the metro management tools over MCP (Model Context
// Protocol, Streamable HTTP, stateless) at POST /mcp/metro.
//
// It is a thin adapter. A caller proves itself with a management token
// (Authorization: Bearer mcp_...), never with a Cookie; each tool call is
// translated into the internal metro AdminService method the web admin also uses,
// with a credential naming the token's owner, the owner's role snapshot and the
// mcp channel. Validation, optimistic concurrency and audit are therefore the
// same as on the web.
//
// The protocol subset is small (initialize, ping, tools/list, tools/call, no
// resources, prompts or sessions), so it is implemented here instead of pulling a
// protocol SDK into the BFF; see docs/metro/spec.md section 8.4.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/kongken/mono/internal/bff/accesstoken"
	"github.com/kongken/mono/internal/bff/usersrv"
	"github.com/kongken/mono/internal/platform/httpx"
	"github.com/kongken/mono/internal/platform/rpcauth"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
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

// supportedVersions are the protocol revisions the server speaks, newest first.
var supportedVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

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

// Config wires a Server.
type Config struct {
	Admin    metrov1.AdminServiceClient
	Tokens   Authenticator
	Accounts Accounts
	Limiter  Limiter
	// RatePerMinute is the per-token limit; zero means DefaultRatePerMinute.
	RatePerMinute int
	// ClientIPHeader is the trusted proxy header carrying the client address.
	ClientIPHeader string
}

// Server is the MCP endpoint handler.
type Server struct {
	cfg    Config
	tools  []*tool
	byName map[string]*tool
}

// New returns the endpoint handler.
func New(cfg Config) *Server {
	if cfg.RatePerMinute <= 0 {
		cfg.RatePerMinute = DefaultRatePerMinute
	}
	s := &Server{cfg: cfg, tools: tools(), byName: map[string]*tool{}}
	for _, t := range s.tools {
		s.byName[t.name] = t
	}
	return s
}

// ServeHTTP implements http.Handler. Cookies are never read, and no CORS headers
// are ever set: the endpoint is for programs, not for pages in a browser.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		// A stateless server offers no SSE stream (GET) and has no session to end (DELETE).
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	clientIP := httpx.ClientAddr(s.cfg.ClientIPHeader, r.Header, r.RemoteAddr)

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
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "bad request", http.StatusBadRequest)
		}
		return
	}
	s.serveJSONRPC(ctx, w, body, principal, clientIP)
}

// authenticate resolves the Bearer token; a non-zero status is the HTTP
// rejection. Failed attempts are counted per client address so a guesser is
// throttled before it can load the token table.
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

// ---- JSON-RPC ----

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

func (s *Server) serveJSONRPC(ctx context.Context, w http.ResponseWriter, body []byte, p accesstoken.Principal, clientIP string) {
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		writeJSON(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeParse, "empty body"}})
		return
	}
	if body[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(body, &batch); err != nil || len(batch) == 0 {
			writeJSON(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeInvalidRequest, "invalid batch"}})
			return
		}
		var out []rpcResponse
		for _, item := range batch {
			if res := s.handleOne(ctx, item, p, clientIP); res != nil {
				out = append(out, *res)
			}
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSON(w, out)
		return
	}
	res := s.handleOne(ctx, body, p, clientIP)
	if res == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, res)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// handleOne answers one message, or returns nil for a notification or a client
// response that needs no reply.
func (s *Server) handleOne(ctx context.Context, raw json.RawMessage, p accesstoken.Principal, clientIP string) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeParse, "parse error"}}
	}
	if req.Method == "" {
		// A response to something the server never asked.
		return nil
	}
	notification := len(req.ID) == 0
	if req.JSONRPC != "2.0" {
		if notification {
			return nil
		}
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{codeInvalidRequest, "jsonrpc must be 2.0"}}
	}
	result, rerr := s.dispatch(ctx, req, p, clientIP)
	if notification {
		return nil
	}
	if rerr != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rerr}
	}
	return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func (s *Server) dispatch(ctx context.Context, req rpcRequest, p accesstoken.Principal, clientIP string) (any, *rpcError) {
	switch {
	case req.Method == "initialize":
		return s.initialize(req.Params), nil
	case req.Method == "ping":
		return map[string]any{}, nil
	case req.Method == "tools/list":
		return s.listTools(p), nil
	case req.Method == "tools/call":
		return s.callTool(ctx, req.Params, p, clientIP)
	case strings.HasPrefix(req.Method, "notifications/"):
		return nil, nil
	}
	return nil, &rpcError{codeMethodNotFound, "method not found"}
}

func (s *Server) initialize(params json.RawMessage) any {
	var in struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &in)
	version := supportedVersions[0]
	if slices.Contains(supportedVersions, in.ProtocolVersion) {
		version = in.ProtocolVersion
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      map[string]any{"name": "kongken-metro", "version": "1"},
		"instructions": "Manage the metro catalogue (cities, lines, rolling stock, topics). " +
			"Read an entity before changing it and pass its revision as expectedRevision. " +
			"metro_import_catalog only previews unless dryRun is explicitly false.",
	}
}

func (s *Server) listTools(p accesstoken.Principal) any {
	out := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		if !t.allowedFor(p) {
			continue
		}
		annotations := map[string]any{"readOnlyHint": t.readOnly}
		if !t.readOnly {
			annotations["destructiveHint"] = t.destructive
		}
		out = append(out, map[string]any{
			"name": t.name, "description": t.description,
			"inputSchema": t.schema(), "annotations": annotations,
		})
	}
	return map[string]any{"tools": out}
}

// ---- tool calls ----

type toolResult struct {
	Content []map[string]string `json:"content"`
	IsError bool                `json:"isError,omitempty"`
}

func textResult(text string, isError bool) toolResult {
	return toolResult{Content: []map[string]string{{"type": "text", "text": text}}, IsError: isError}
}

func (s *Server) callTool(ctx context.Context, params json.RawMessage, p accesstoken.Principal, clientIP string) (any, *rpcError) {
	var in struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &in); err != nil || in.Name == "" {
		return nil, &rpcError{codeInvalidParams, "name is required"}
	}
	t := s.byName[in.Name]
	if t == nil {
		return nil, &rpcError{codeInvalidParams, "unknown tool " + in.Name}
	}
	if !t.allowedFor(p) {
		return textResult((&toolError{Code: "PERMISSION_DENIED",
			Message: "this token does not have the " + t.scope + " scope"}).text(), true), nil
	}
	roles, terr := s.effectiveRoles(ctx, p)
	if terr != nil {
		return textResult(terr.text(), true), nil
	}
	limit := callTimeout
	if t.heavy {
		limit = heavyCallTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	// The credential the metro service verifies: the token's owner and role
	// snapshot, on the mcp channel, tagged with the token for the audit trail.
	ctx = rpcauth.WithCall(ctx, rpcauth.Call{
		UserID: p.UserID, Roles: roles, Channel: rpcauth.ChannelMCP, TokenID: p.TokenID,
		RequestID: rpcauth.NewRequestID(), ClientIP: clientIP,
	})
	out, terr := s.run(ctx, t, in.Arguments)
	slog.Info("mcp tool call", "tool", t.name, "token_id", p.TokenID, "user_id", p.UserID, "error", terr != nil)
	if terr != nil {
		return textResult(terr.text(), true), nil
	}
	return textResult(out, false), nil
}

// effectiveRoles asks usersrv about the token's owner for this very call. The
// account must be active at the auth_version the token was created at, and the
// roles are the token's snapshot narrowed to what the account holds now. When
// usersrv cannot answer, the call fails and nothing is forwarded.
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
