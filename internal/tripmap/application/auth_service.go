package application

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/platform/httpx"
	"github.com/kongken/mono/internal/tripmap/auth"
	"github.com/kongken/mono/internal/tripmap/auth/provider"
	"github.com/kongken/mono/internal/tripmap/config"
	"github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/ent/session"
	"github.com/kongken/mono/internal/tripmap/ent/user"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	maxDisplayRunes   = 40
	maxAvatarURLBytes = 500
	minPasswordBytes  = 8
	maxPasswordBytes  = 72 // bcrypt ignores everything after 72 bytes
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

// Rate limits; windows are fixed. Login is limited per account and per client
// address, so one address cannot guess many accounts and many addresses cannot
// hammer one account without bound.
const (
	loginWindow       = 15 * time.Minute
	loginPerUser      = 10
	loginPerIP        = 40
	registerWindow    = time.Hour
	registerPerIP     = 10
	oauthWindow       = 15 * time.Minute
	oauthPerIP        = 60
	invalidCredential = "invalid username or password"
)

// AuthService implements tripmap.v1.AuthService.
type AuthService struct {
	cfg *config.AppConfig
	src StoreSource

	providersOnce sync.Once
	providers     *provider.Registry
}

func NewAuthService(cfg *config.AppConfig, src StoreSource) *AuthService {
	return &AuthService{cfg: cfg, src: src}
}

// SetProviders overrides the OAuth registry (used by tests).
func (s *AuthService) SetProviders(r *provider.Registry) {
	s.providersOnce.Do(func() {})
	s.providers = r
}

func (s *AuthService) registry() *provider.Registry {
	s.providersOnce.Do(func() { s.providers = provider.BuildRegistry(s.cfg.Auth) })
	return s.providers
}

var (
	dummyHashOnce sync.Once
	dummyHash     []byte
)

// burnPassword spends a bcrypt comparison so unknown usernames take as long as
// known ones.
func burnPassword(password string) {
	dummyHashOnce.Do(func() { dummyHash, _ = bcrypt.GenerateFromPassword([]byte("unused-password"), bcrypt.DefaultCost) })
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
}

func (s *AuthService) clientIP(req connect.AnyRequest) string {
	return httpx.ClientAddr(s.cfg.Auth.ClientIPHeader, req.Header(), req.Peer().Addr)
}

func (s *AuthService) allow(ctx context.Context, st *Store, key string, limit int64, window time.Duration) error {
	ok, err := auth.NewLimiter(st.Redis, st.prefix()).Allow(ctx, key, limit, window)
	if err != nil {
		slog.Error("rate limiter unavailable", "error", err)
		return connect.NewError(connect.CodeUnavailable, errors.New("service temporarily unavailable"))
	}
	if !ok {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("too many attempts, try again later"))
	}
	return nil
}

func toUser(u *ent.User) *tripmapv1.User {
	out := &tripmapv1.User{Id: u.ID, DisplayName: u.DisplayName, AvatarUrl: u.AvatarURL, CreatedAt: timestamppb.New(u.CreatedAt), DeletionPending: u.DeletionRequestedAt != nil}
	if u.Username != nil {
		out.Username = *u.Username
	}
	return out
}

func normalizeUsername(raw string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(raw))
	if !usernamePattern.MatchString(name) {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("username must be 3-32 characters of a-z, 0-9 or underscore"))
	}
	return name, nil
}

func cleanDisplayName(raw, fallback string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return fallback, nil
	}
	if utf8.RuneCountInString(name) > maxDisplayRunes {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("display name is too long"))
	}
	return name, nil
}

// issueSession creates a session for the user and returns the bearer token.
func (s *AuthService) issueSession(ctx context.Context, st *Store, userID string) (string, time.Time, error) {
	token, hash, err := auth.NewToken()
	if err != nil {
		return "", time.Time{}, internalError(err)
	}
	expires := time.Now().UTC().Add(s.cfg.SessionLifetime())
	if _, err = st.DB.Session.Create().SetUserID(userID).SetTokenHash(hash).SetExpiresAt(expires).Save(ctx); err != nil {
		slog.Error("create session", "error", err)
		return "", time.Time{}, internalError(err)
	}
	return token, expires, nil
}

func (s *AuthService) Register(ctx context.Context, req *connect.Request[tripmapv1.RegisterRequest]) (*connect.Response[tripmapv1.RegisterResponse], error) {
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if s.cfg.Auth.RegistrationDisabled {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("registration is closed"))
	}
	if err = s.allow(ctx, st, "register:ip:"+s.clientIP(req), registerPerIP, registerWindow); err != nil {
		return nil, err
	}
	username, err := normalizeUsername(req.Msg.GetUsername())
	if err != nil {
		return nil, err
	}
	password := req.Msg.GetPassword()
	if len(password) < minPasswordBytes || len(password) > maxPasswordBytes {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("password must be 8-72 bytes"))
	}
	display, err := cleanDisplayName(req.Msg.GetDisplayName(), username)
	if err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, internalError(err)
	}
	u, err := st.DB.User.Create().SetUsername(username).SetDisplayName(display).SetPasswordHash(string(hash)).Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("username is taken"))
		}
		slog.Error("create user", "error", err)
		return nil, internalError(err)
	}
	token, expires, err := s.issueSession(ctx, st, u.ID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&tripmapv1.RegisterResponse{Token: token, User: toUser(u), ExpiresAt: timestamppb.New(expires)}), nil
}

func (s *AuthService) Login(ctx context.Context, req *connect.Request[tripmapv1.LoginRequest]) (*connect.Response[tripmapv1.LoginResponse], error) {
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	username := strings.ToLower(strings.TrimSpace(req.Msg.GetUsername()))
	password := req.Msg.GetPassword()
	if username == "" || password == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("username and password are required"))
	}
	if len(username) > 64 || len(password) > 1024 {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New(invalidCredential))
	}
	if err = s.allow(ctx, st, "login:ip:"+s.clientIP(req), loginPerIP, loginWindow); err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "login:user:"+username, loginPerUser, loginWindow); err != nil {
		return nil, err
	}
	u, err := st.DB.User.Query().Where(user.UsernameEQ(username)).Only(ctx)
	switch {
	case ent.IsNotFound(err):
		burnPassword(password)
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New(invalidCredential))
	case err != nil:
		slog.Error("login lookup", "error", err)
		return nil, internalError(err)
	}
	if u.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		if u.PasswordHash == "" {
			burnPassword(password)
		}
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New(invalidCredential))
	}
	if u.Disabled && u.DeletionRequestedAt == nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("account is disabled"))
	}
	token, expires, err := s.issueSession(ctx, st, u.ID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&tripmapv1.LoginResponse{Token: token, User: toUser(u), ExpiresAt: timestamppb.New(expires)}), nil
}

func principal(ctx context.Context) (auth.Principal, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return p, errUnauthenticated
	}
	return p, nil
}

func (s *AuthService) Logout(ctx context.Context, _ *connect.Request[tripmapv1.LogoutRequest]) (*connect.Response[tripmapv1.LogoutResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = st.DB.Session.UpdateOneID(p.SessionID).SetRevoked(true).Exec(ctx); err != nil && !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.LogoutResponse{}), nil
}

func (s *AuthService) Me(ctx context.Context, _ *connect.Request[tripmapv1.MeRequest]) (*connect.Response[tripmapv1.MeResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	u, err := st.DB.User.Get(ctx, p.UserID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, errUnauthenticated
		}
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.MeResponse{User: toUser(u)}), nil
}

func (s *AuthService) ListSessions(ctx context.Context, _ *connect.Request[tripmapv1.ListSessionsRequest]) (*connect.Response[tripmapv1.ListSessionsResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	rows, err := st.DB.Session.Query().
		Where(session.UserID(p.UserID), session.Revoked(false), session.ExpiresAtGT(time.Now().UTC())).
		Order(ent.Desc(session.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	out := &tripmapv1.ListSessionsResponse{}
	for _, r := range rows {
		item := &tripmapv1.Session{Id: r.ID, CreatedAt: timestamppb.New(r.CreatedAt), ExpiresAt: timestamppb.New(r.ExpiresAt), Current: r.ID == p.SessionID}
		if r.LastUsedAt != nil {
			item.LastUsedAt = timestamppb.New(*r.LastUsedAt)
		}
		out.Sessions = append(out.Sessions, item)
	}
	return connect.NewResponse(out), nil
}

func (s *AuthService) RevokeSession(ctx context.Context, req *connect.Request[tripmapv1.RevokeSessionRequest]) (*connect.Response[tripmapv1.RevokeSessionResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	// Scoping by user_id makes other users' sessions indistinguishable from
	// missing ones.
	n, err := st.DB.Session.Update().Where(session.ID(req.Msg.GetId()), session.UserID(p.UserID)).SetRevoked(true).Save(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	if n == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("session not found"))
	}
	return connect.NewResponse(&tripmapv1.RevokeSessionResponse{}), nil
}

func (s *AuthService) ListOAuthProviders(context.Context, *connect.Request[tripmapv1.ListOAuthProvidersRequest]) (*connect.Response[tripmapv1.ListOAuthProvidersResponse], error) {
	list := s.registry().List()
	sort.Slice(list, func(i, j int) bool { return list[i].Name() < list[j].Name() })
	out := &tripmapv1.ListOAuthProvidersResponse{}
	for _, p := range list {
		out.Providers = append(out.Providers, &tripmapv1.OAuthProvider{Name: p.Name(), DisplayName: p.DisplayName()})
	}
	return connect.NewResponse(out), nil
}

func (s *AuthService) BeginOAuthFlow(ctx context.Context, req *connect.Request[tripmapv1.BeginOAuthFlowRequest]) (*connect.Response[tripmapv1.BeginOAuthFlowResponse], error) {
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	p, err := s.registry().Get(strings.TrimSpace(req.Msg.GetProvider()))
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("unknown oauth provider"))
	}
	if err = s.allow(ctx, st, "oauth:ip:"+s.clientIP(req), oauthPerIP, oauthWindow); err != nil {
		return nil, err
	}
	pk, usesPKCE := p.(provider.PKCEProvider)
	var verifier, challenge string
	if usesPKCE {
		if verifier, challenge, err = provider.NewPKCE(); err != nil {
			slog.Error("create pkce verifier", "error", err)
			return nil, internalError(err)
		}
	}
	state, err := auth.NewStateStore(st.Redis, st.prefix()).Create(ctx, p.Name(), verifier)
	if err != nil {
		slog.Error("create oauth state", "error", err)
		return nil, internalError(err)
	}
	authorizeURL := ""
	if usesPKCE {
		authorizeURL = pk.AuthorizePKCEURL(state, challenge)
	} else {
		authorizeURL = p.AuthorizeURL(state)
	}
	return connect.NewResponse(&tripmapv1.BeginOAuthFlowResponse{AuthorizeUrl: authorizeURL, State: state}), nil
}

func (s *AuthService) CompleteOAuthFlow(ctx context.Context, req *connect.Request[tripmapv1.CompleteOAuthFlowRequest]) (*connect.Response[tripmapv1.CompleteOAuthFlowResponse], error) {
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	name, code, state := strings.TrimSpace(req.Msg.GetProvider()), strings.TrimSpace(req.Msg.GetCode()), strings.TrimSpace(req.Msg.GetState())
	if name == "" || code == "" || state == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("provider, code and state are required"))
	}
	if err = s.allow(ctx, st, "oauth:ip:"+s.clientIP(req), oauthPerIP, oauthWindow); err != nil {
		return nil, err
	}
	attempt, err := auth.NewStateStore(st.Redis, st.prefix()).Consume(ctx, state)
	if errors.Is(err, auth.ErrStateNotFound) || (err == nil && attempt.Provider != name) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("invalid or expired state"))
	}
	if err != nil {
		slog.Error("consume oauth state", "error", err)
		return nil, internalError(err)
	}
	p, err := s.registry().Get(name)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("unknown oauth provider"))
	}
	var claims *provider.Claims
	if pk, ok := p.(provider.PKCEProvider); ok {
		claims, err = pk.ExchangePKCE(ctx, code, attempt.CodeVerifier)
	} else {
		claims, err = p.Exchange(ctx, code)
	}
	if err != nil || claims == nil || claims.ExternalID == "" {
		slog.Warn("oauth exchange failed", "provider", name, "error", err)
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("oauth sign-in failed"))
	}
	u, err := s.oauthUser(ctx, st, name, claims)
	if err != nil {
		return nil, err
	}
	if u.Disabled && u.DeletionRequestedAt == nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("account is disabled"))
	}
	token, expires, err := s.issueSession(ctx, st, u.ID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&tripmapv1.CompleteOAuthFlowResponse{Token: token, User: toUser(u), ExpiresAt: timestamppb.New(expires)}), nil
}

// oauthUser finds the account for a provider identity, creating it when
// registration is open. Accounts are never linked by email address.
func (s *AuthService) oauthUser(ctx context.Context, st *Store, providerName string, claims *provider.Claims) (*ent.User, error) {
	find := func() (*ent.User, error) {
		return st.DB.User.Query().Where(user.Provider(providerName), user.ExternalID(claims.ExternalID)).Only(ctx)
	}
	u, err := find()
	if err == nil {
		return u, nil
	}
	if !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	if s.cfg.Auth.RegistrationDisabled {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("registration is closed"))
	}
	display := strings.TrimSpace(claims.Name)
	if display == "" {
		display = strings.TrimSpace(claims.Login)
	}
	if display == "" {
		display = "旅行者"
	}
	if r := []rune(display); len(r) > maxDisplayRunes {
		display = string(r[:maxDisplayRunes])
	}
	avatar := claims.AvatarURL
	if len(avatar) > maxAvatarURLBytes || !(strings.HasPrefix(avatar, "https://") || strings.HasPrefix(avatar, "http://")) {
		avatar = ""
	}
	u, err = st.DB.User.Create().SetProvider(providerName).SetExternalID(claims.ExternalID).SetDisplayName(display).SetAvatarURL(avatar).Save(ctx)
	if ent.IsConstraintError(err) { // a concurrent first sign-in created it
		if u, err = find(); err != nil {
			return nil, internalError(err)
		}
		return u, nil
	}
	if err != nil {
		slog.Error("create oauth user", "error", err)
		return nil, internalError(err)
	}
	return u, nil
}
