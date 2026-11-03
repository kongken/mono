package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/ent"
	"github.com/kongken/mono/internal/bff/ent/browsercontext"
	"github.com/kongken/mono/internal/bff/ent/oauthbrowserstate"
	entsession "github.com/kongken/mono/internal/bff/ent/session"
	"github.com/kongken/mono/internal/bff/navigation"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/bff/usersrv"
	"github.com/kongken/mono/internal/platform/httpx"
	"github.com/kongken/mono/internal/platform/rpcauth"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	usersrvv1 "github.com/kongken/mono/pkg/proto/usersrv/v1"
)

type ExternalUsers interface {
	BeginExternal(context.Context, string, *usersrvv1.BeginExternalLoginRequest) (*usersrvv1.BeginExternalLoginResponse, error)
	CompleteExternal(context.Context, string, *usersrvv1.CompleteExternalLoginRequest) (*usersrvv1.CompleteExternalLoginResponse, error)
	ExternalRegistration(context.Context, *usersrvv1.ExternalChallengeReference) (*usersrvv1.ExternalPending, error)
	RequestExternalEmail(context.Context, *usersrvv1.ExternalChallengeReference, string) error
	ConfirmExternalEmail(context.Context, string, string) error
	CreateExternalAccount(context.Context, *usersrvv1.CompleteExternalRegistrationRequest) (string, error)
}

// ExternalHandler owns durable browser state. Protocol secrets exist only in
// short-lived encrypted payloads, removed before the code exchange RPC.
type ExternalHandler struct {
	svc                                   *Service
	users                                 ExternalUsers
	cfg                                   config.ExternalConfig
	apiOrigin, ozidOrigin, clientIPHeader string
	navigation                            *navigation.Registry
	keys                                  map[string][]byte
}

func NewExternalHandler(ctx context.Context, svc *Service, users ExternalUsers, cfg config.ExternalConfig, nav navigation.Config, registry *navigation.Registry, ipHeader string) (*ExternalHandler, error) {
	cfg.Providers = maps.Clone(cfg.Providers)
	cfg.ProviderConfigVersions = maps.Clone(cfg.ProviderConfigVersions)
	keys, err := cfg.LoadKeys()
	if err != nil {
		return nil, err
	}
	// Retain old decryption keys until all their active states expire, including
	// when disabled. A rollout may not silently strand an encrypted live state.
	var keyIDs []string
	if svc.db == nil && cfg.Enabled {
		return nil, errors.New("auth.external requires BFF storage")
	}
	if svc.db != nil {
		keyIDs, err = svc.db.OAuthBrowserState.Query().Where(oauthbrowserstate.ExpiresAtGT(svc.now()), oauthbrowserstate.PkceVerifierCiphertextNotNil()).Unique(true).Select(oauthbrowserstate.FieldKeyID).Strings(ctx)
	}
	if err != nil {
		return nil, err
	}
	for _, id := range keyIDs {
		if len(keys[id]) != 32 {
			return nil, errors.New("auth.external: active state needs an unavailable decryption key")
		}
	}
	return &ExternalHandler{svc: svc, users: users, cfg: cfg, apiOrigin: nav.APIOrigin, ozidOrigin: nav.Apps[navigation.AppOzID].Origin, navigation: registry, keys: keys, clientIPHeader: ipHeader}, nil
}
func stateInvalid() error { return connect.NewError(connect.CodeFailedPrecondition, errStateInvalid) }
func providerDisabled() error {
	return connect.NewError(connect.CodeFailedPrecondition, errors.New("PROVIDER_DISABLED"))
}
func providerEnum(p string) usersrvv1.ExternalProvider {
	if p == "google" {
		return usersrvv1.ExternalProvider_EXTERNAL_PROVIDER_GOOGLE
	}
	if p == "github" {
		return usersrvv1.ExternalProvider_EXTERNAL_PROVIDER_GITHUB
	}
	return 0
}
func digest(b []byte) []byte { s := sha256.Sum256(b); return s[:] }
func randomSecret() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), err
}
func flowCookie(id string) string { return "kongken_ext_" + strings.ReplaceAll(id, "-", "") }
func (h *ExternalHandler) cookie(header http.Header, id string) string {
	return uniqueCookie(header, privateCookieName(h.svc.cookies, flowCookie(id)))
}
func (h *ExternalHandler) ref(row *ent.OAuthBrowserState) *usersrvv1.ExternalChallengeReference {
	b := &usersrvv1.ExternalFlowBinding{FlowId: row.ID, ContextHash: row.ContextHash}
	if row.Intent == oauthbrowserstate.IntentLink {
		// Identity operations bind to the persisted BFF session row ID. Keep the
		// external proof on that same derivation; the initiating token hash is a
		// separate bearer credential and cannot satisfy the usersrv operation
		// challenge binding.
		if row.SessionID != nil {
			b.SessionBindingHash = digest([]byte(*row.SessionID))
		}
		if row.AuthVersion != nil {
			b.ExpectedAuthVersion = *row.AuthVersion
		}
	}
	return &usersrvv1.ExternalChallengeReference{ChallengeId: row.UsersrvChallengeID, Binding: b}
}
func rowUser(row *ent.OAuthBrowserState) string {
	if row.Intent == oauthbrowserstate.IntentLink && row.UserID != nil {
		return *row.UserID
	}
	return ""
}

type protocolPayload struct{ Verifier, Nonce string }

func (p protocolPayload) String() string       { return "[redacted external protocol payload]" }
func (p protocolPayload) GoString() string     { return p.String() }
func (p protocolPayload) LogValue() slog.Value { return slog.StringValue(p.String()) }
func (h *ExternalHandler) allowsFlow(row *ent.OAuthBrowserState) bool {
	return h.cfg.Allows(string(row.Provider)) && h.cfg.ProviderVersion(string(row.Provider)) == row.ProviderConfigVersion
}
func stateAAD(id, key string, ctx []byte) []byte {
	return append([]byte("bff.external.state.v1\x00"+id+"\x00"+key+"\x00"), ctx...)
}
func sealState(key []byte, id, kid string, ctx []byte, p protocolPayload) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	a, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, raw, stateAAD(id, kid, ctx)), nil
}
func (h *ExternalHandler) openState(row *ent.OAuthBrowserState) (protocolPayload, error) {
	var p protocolPayload
	block, err := aes.NewCipher(h.keys[row.KeyID])
	if err != nil {
		return p, stateInvalid()
	}
	a, err := cipher.NewGCM(block)
	if err != nil {
		return p, stateInvalid()
	}
	data := row.PkceVerifierCiphertext
	if len(data) < a.NonceSize() {
		return p, stateInvalid()
	}
	raw, err := a.Open(nil, data[:a.NonceSize()], data[a.NonceSize():], stateAAD(row.ID, row.KeyID, row.ContextHash))
	if err != nil {
		return p, stateInvalid()
	}
	if json.Unmarshal(raw, &p) != nil || session.HashToken(p.Nonce) != row.NonceHash {
		return protocolPayload{}, stateInvalid()
	}
	return p, nil
}

func (h *ExternalHandler) StartExternalLogin(ctx context.Context, req *connect.Request[bffv1.StartExternalLoginRequest]) (*connect.Response[bffv1.StartExternalLoginResponse], error) {
	p, intent := req.Msg.GetProvider(), req.Msg.GetIntent()
	if providerEnum(p) == 0 || (intent != "login" && intent != "link") {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid provider or intent"))
	}
	if !h.cfg.Allows(p) {
		return nil, providerDisabled()
	}
	target, err := h.navigation.Resolve(req.Msg.GetAppId(), req.Msg.GetReturnPath())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("navigation target not allowed"))
	}
	if err := h.svc.allow(ctx, "external:start:"+rpcauth.CallFrom(ctx).ClientIP, 30, 15*time.Minute); err != nil {
		return nil, err
	}
	browserID := h.svc.browserID(req.Header())
	if browserID == "" {
		return nil, stateInvalid()
	}
	browser, err := h.svc.db.BrowserContext.Query().Where(browsercontext.ID(browserID), browsercontext.ExpiresAtGT(h.svc.now())).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, stateInvalid()
		}
		return nil, internal(err)
	}
	var bs session.BrowserSession
	if intent == "link" || session.TokenFromRequest(req.Header()) != "" {
		bs, err = h.svc.ResolveSession(ctx, session.TokenFromRequest(req.Header()))
		if err != nil && (intent == "link" || connect.CodeOf(err) != connect.CodeUnauthenticated) {
			return nil, err
		}
	}
	if expected := req.Header().Get("X-Kongken-User"); intent == "link" && expected != "" && expected != bs.UserID {
		return nil, errUnauth
	}
	id := uuid.NewString()
	state, err := randomSecret()
	if err != nil {
		return nil, internal(err)
	}
	binding, err := randomSecret()
	if err != nil {
		return nil, internal(err)
	}
	verifier, err := randomSecret()
	if err != nil {
		return nil, internal(err)
	}
	nonce := ""
	if p == "google" {
		nonce, err = randomSecret()
		if err != nil {
			return nil, internal(err)
		}
	}
	tokenHash := session.HashToken(session.TokenFromRequest(req.Header()))
	// JSON array is canonical and unambiguous; every authority-bearing start
	// field is server-derived and included. usersrv also binds its config version.
	canonical, _ := json.Marshal([]any{id, p, intent, h.cfg.ProviderVersion(p), h.apiOrigin + "/api/identity/callback/" + p, session.HashToken(binding), browserID, browser.Generation, tokenHash, bs.UserID, bs.SessionID, bs.AuthVersion, target.AppID, target.Path})
	contextHash := digest(canonical)
	flowBinding := &usersrvv1.ExternalFlowBinding{FlowId: id, ContextHash: contextHash}
	if intent == "link" {
		flowBinding.SessionBindingHash = digest([]byte(bs.SessionID))
		flowBinding.ExpectedAuthVersion = bs.AuthVersion
	}
	caller := ""
	if intent == "link" {
		caller = bs.UserID
	}
	begun, err := h.users.BeginExternal(ctx, caller, &usersrvv1.BeginExternalLoginRequest{Provider: providerEnum(p), Intent: intent, Binding: flowBinding, RedirectUri: h.apiOrigin + "/api/identity/callback/" + p, State: state, Nonce: nonce, CodeChallenge: base64.RawURLEncoding.EncodeToString(digest([]byte(verifier))), AppId: target.AppID, ReturnPath: target.Path})
	if err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(begun.GetChallengeId()); err != nil || begun.GetProviderConfigVersion() != h.cfg.ProviderVersion(p) {
		return nil, internal(errors.New("invalid external start result"))
	}
	// Never reflect an arbitrary upstream URL as a browser redirect.
	u, err := url.Parse(begun.GetAuthorizationUrl())
	allowed := p == "google" && u != nil && u.Host == "accounts.google.com" && u.Path == "/o/oauth2/v2/auth" || p == "github" && u != nil && u.Host == "github.com" && u.Path == "/login/oauth/authorize"
	if err != nil || !allowed || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Query().Get("state") != state {
		return nil, internal(errors.New("invalid external authorization URL"))
	}
	ciphertext, err := sealState(h.keys[h.cfg.StateEncryptionKeyID], id, h.cfg.StateEncryptionKeyID, contextHash, protocolPayload{verifier, nonce})
	if err != nil {
		return nil, internal(err)
	}
	expires := h.svc.now().Add(h.cfg.Lifetime())
	create := h.svc.db.OAuthBrowserState.Create().SetID(id).SetUsersrvChallengeID(begun.GetChallengeId()).SetStateHash(session.HashToken(state)).SetProvider(oauthbrowserstate.Provider(p)).SetIntent(oauthbrowserstate.Intent(intent)).SetBrowserBindingHash(session.HashToken(binding)).SetBrowserContextID(browserID).SetBrowserGeneration(browser.Generation).SetInitiatingTokenHash(tokenHash).SetProviderConfigVersion(begun.GetProviderConfigVersion()).SetContextHash(contextHash).SetNonceHash(session.HashToken(nonce)).SetKeyID(h.cfg.StateEncryptionKeyID).SetPkceVerifierCiphertext(ciphertext).SetReturnAppID(target.AppID).SetReturnPath(target.Path).SetExpiresAt(expires)
	if bs.UserID != "" {
		create.SetUserID(bs.UserID).SetSessionID(bs.SessionID).SetAuthVersion(bs.AuthVersion)
	}
	if _, err = create.Save(ctx); err != nil {
		return nil, internal(err)
	}
	res := connect.NewResponse(&bffv1.StartExternalLoginResponse{FlowId: id, AuthorizationUrl: begun.GetAuthorizationUrl()})
	noSecrets(res.Header())
	setPrivateCookie(res.Header(), h.svc.cookies, flowCookie(id), binding, expires)
	return res, nil
}

func noSecrets(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
}
func (h *ExternalHandler) bound(ctx context.Context, header http.Header, row *ent.OAuthBrowserState, authenticated bool) error {
	if !row.ExpiresAt.After(h.svc.now()) || h.svc.browserID(header) != row.BrowserContextID {
		return stateInvalid()
	}
	if !authenticated && row.ConfirmationExpiresAt != nil && !row.ConfirmationExpiresAt.After(h.svc.now()) {
		return stateInvalid()
	}
	raw := h.cookie(header, row.ID)
	if raw == "" || subtle.ConstantTimeCompare([]byte(session.HashToken(raw)), []byte(row.BrowserBindingHash)) != 1 {
		return stateInvalid()
	}
	gen := row.BrowserGeneration
	if authenticated {
		gen++
	} else if session.HashToken(session.TokenFromRequest(header)) != row.InitiatingTokenHash {
		return stateInvalid()
	}
	ok, err := h.svc.db.BrowserContext.Query().Where(browsercontext.ID(row.BrowserContextID), browsercontext.Generation(gen), browsercontext.ExpiresAtGT(h.svc.now())).Exist(ctx)
	if err != nil {
		return internal(err)
	}
	if !ok {
		return stateInvalid()
	}
	if row.SessionID != nil && !authenticated {
		bs, err := h.svc.ResolveSession(ctx, session.TokenFromRequest(header))
		if err != nil {
			return err
		}
		if row.SessionID == nil || row.UserID == nil || row.AuthVersion == nil || bs.SessionID != *row.SessionID || bs.UserID != *row.UserID || bs.AuthVersion != *row.AuthVersion {
			return stateInvalid()
		}
	}
	return nil
}
func (h *ExternalHandler) flow(ctx context.Context, header http.Header, id string) (*ent.OAuthBrowserState, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, stateInvalid()
	}
	row, err := h.svc.db.OAuthBrowserState.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, stateInvalid()
		}
		return nil, internal(err)
	}
	if err = h.bound(ctx, header, row, row.ResultStatus == "authenticated"); err != nil {
		return nil, err
	}
	return row, nil
}

// Callback is the sole exception to Origin/CSRF: fixed provider path, GET only,
// per-flow state and HttpOnly binding replace those checks. No ID/profile/return
// fields from the provider callback are accepted as authority.
func (h *ExternalHandler) Callback(w http.ResponseWriter, r *http.Request) {
	noSecrets(w.Header())
	httpx.RedactSpan(r, "/api/identity/callback/[redacted]")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.svc.db == nil {
		h.callbackFailure(w, r, "PROVIDER_DISABLED", "")
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/identity/callback/")
	if providerEnum(p) == 0 {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	ctx = rpcauth.WithCall(ctx, rpcauth.Call{Channel: rpcauth.ChannelWeb, ClientIP: httpx.ClientAddr(h.clientIPHeader, r.Header, r.RemoteAddr), RequestID: rpcauth.NormalizeRequestID(r.Header.Get("X-Request-Id"))})
	if len(r.URL.RawQuery) > 8192 {
		h.callbackFailure(w, r, "STATE_INVALID", "")
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q["state"]) != 1 || q.Get("state") == "" || len(q.Get("state")) > 256 {
		h.callbackFailure(w, r, "STATE_INVALID", "")
		return
	}
	row, err := h.svc.db.OAuthBrowserState.Query().Where(oauthbrowserstate.StateHash(session.HashToken(q.Get("state")))).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			h.callbackFailure(w, r, "STATE_INVALID", "")
		} else {
			h.callbackFailure(w, r, "UNAVAILABLE", "")
		}
		return
	}
	if string(row.Provider) != p || row.ConsumedAt != nil {
		h.callbackFailure(w, r, "STATE_INVALID", "")
		return
	}
	if err = h.bound(ctx, r.Header, row, false); err != nil {
		h.callbackFailure(w, r, callbackReason(err), "")
		return
	}
	payload, openErr := h.openState(row)
	// Every valid callback, including cancellation or malformed code, terminates
	// its own state. Cross-browser/invalid callbacks leave the rightful flow intact.
	n, err := h.svc.db.OAuthBrowserState.Update().Where(oauthbrowserstate.ID(row.ID), oauthbrowserstate.ConsumedAtIsNil(), oauthbrowserstate.ExpiresAtGT(h.svc.now())).SetConsumedAt(h.svc.now()).SetResultStatus("exchanging").ClearPkceVerifierCiphertext().Save(ctx)
	if err != nil {
		h.callbackFailure(w, r, "UNAVAILABLE", "")
		return
	}
	if n != 1 {
		h.callbackFailure(w, r, "STATE_INVALID", "")
		return
	}
	if openErr != nil || len(q["code"]) != 1 || q.Get("code") == "" || len(q.Get("code")) > 4096 || len(q["error"]) > 0 {
		h.failFlow(ctx, row)
		h.callbackFailure(w, r, "PROVIDER_REJECTED", row.ID)
		return
	}
	if !h.allowsFlow(row) {
		h.failFlow(ctx, row)
		h.callbackFailure(w, r, "PROVIDER_DISABLED", row.ID)
		return
	}
	res, err := h.users.CompleteExternal(ctx, rowUser(row), &usersrvv1.CompleteExternalLoginRequest{Challenge: h.ref(row), Provider: providerEnum(p), ProviderConfigVersion: row.ProviderConfigVersion, Code: q.Get("code"), Verifier: payload.Verifier, Nonce: payload.Nonce, CodeChallenge: base64.RawURLEncoding.EncodeToString(digest([]byte(payload.Verifier)))})
	if err != nil {
		h.failFlow(ctx, row)
		reason := "PROVIDER_REJECTED"
		if connect.CodeOf(err) == connect.CodeUnavailable || connect.CodeOf(err) == connect.CodeDeadlineExceeded {
			reason = "UNAVAILABLE"
		}
		if usersrv.BrowserReason(err) == usersrv.ReasonProviderDisabled {
			reason = "PROVIDER_DISABLED"
		}
		if usersrv.BrowserReason(err) == usersrv.ReasonRegistrationUnavailable {
			reason = "REGISTRATION_UNAVAILABLE"
		}
		h.callbackFailure(w, r, reason, row.ID)
		return
	}
	if a := res.GetAuthenticated(); a != nil && row.Intent == oauthbrowserstate.IntentLogin {
		token, expires, err := h.openExternalSession(ctx, r.Header, row, a.GetUserId(), a.GetAuthVersion())
		if err != nil {
			h.failFlow(ctx, row)
			h.callbackFailure(w, r, callbackReason(err), row.ID)
			return
		}
		session.SetSessionCookie(w.Header(), h.svc.cookies, token, expires)
	} else if pending := res.GetPending(); pending != nil && pending.GetChallengeId() == row.UsersrvChallengeID && ((row.Intent == oauthbrowserstate.IntentLogin && pending.GetIntent() == "registration_pending") || (row.Intent == oauthbrowserstate.IntentLink && pending.GetIntent() == "link_pending")) {
		if err = h.bound(ctx, r.Header, row, false); err != nil {
			h.failFlow(ctx, row)
			h.callbackFailure(w, r, callbackReason(err), row.ID)
			return
		}
		if err = h.svc.db.OAuthBrowserState.UpdateOne(row).SetResultStatus(pending.GetIntent()).Exec(ctx); err != nil {
			h.callbackFailure(w, r, "UNAVAILABLE", row.ID)
			return
		}
	} else {
		h.failFlow(ctx, row)
		h.callbackFailure(w, r, "PROVIDER_REJECTED", row.ID)
		return
	}
	http.Redirect(w, r, h.landing(row.ID, ""), http.StatusSeeOther)
}
func (h *ExternalHandler) failFlow(ctx context.Context, row *ent.OAuthBrowserState) {
	_ = h.svc.db.OAuthBrowserState.UpdateOne(row).SetResultStatus("failed").ClearPkceVerifierCiphertext().Exec(ctx)
}
func (h *ExternalHandler) landing(id, reason string) string {
	q := url.Values{}
	if id != "" {
		q.Set("flow", id)
	}
	if reason != "" {
		q.Set("result", reason)
	}
	return h.ozidOrigin + "/account/external?" + q.Encode()
}
func (h *ExternalHandler) callbackFailure(w http.ResponseWriter, r *http.Request, reason, id string) {
	http.Redirect(w, r, h.landing(id, reason), http.StatusSeeOther)
}
func callbackReason(err error) string {
	switch connect.CodeOf(err) {
	case connect.CodeInternal, connect.CodeUnavailable, connect.CodeDeadlineExceeded, connect.CodeCanceled:
		return "UNAVAILABLE"
	}
	return "STATE_INVALID"
}

func (h *ExternalHandler) openExternalSession(ctx context.Context, header http.Header, row *ent.OAuthBrowserState, uid string, version int64) (string, time.Time, error) {
	if _, err := uuid.Parse(uid); err != nil || version <= 0 {
		return "", time.Time{}, stateInvalid()
	}
	// Re-read authoritative state immediately before creating a session. A creation
	// receipt alone cannot supply a version or bypass disabled/deleted accounts.
	state, err := h.svc.users.AuthState(ctx, uid)
	if err != nil {
		return "", time.Time{}, err
	}
	if state.Status != usersrv.StatusActive || state.AuthVersion != version {
		return "", time.Time{}, stateInvalid()
	}
	if err = h.bound(ctx, header, row, false); err != nil {
		return "", time.Time{}, err
	}
	tx, err := h.svc.db.Tx(ctx)
	if err != nil {
		return "", time.Time{}, internal(err)
	}
	defer tx.Rollback()
	b, err := tx.BrowserContext.Query().Where(browsercontext.ID(row.BrowserContextID), browsercontext.Generation(row.BrowserGeneration), browsercontext.ExpiresAtGT(h.svc.now())).ForUpdate().Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return "", time.Time{}, stateInvalid()
		}
		return "", time.Time{}, internal(err)
	}
	// Lock the initiating session too: logout/revocation cannot race a callback
	// that was started from a live account and silently switch it afterwards.
	if row.SessionID != nil {
		if row.AuthVersion == nil {
			return "", time.Time{}, stateInvalid()
		}
		old, err := tx.Session.Query().Where(entsession.ID(*row.SessionID), entsession.Revoked(false), entsession.ExpiresAtGT(h.svc.now())).ForUpdate().Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return "", time.Time{}, internal(err)
		}
		if err != nil || old.AuthVersion != *row.AuthVersion {
			return "", time.Time{}, stateInvalid()
		}
		if err = tx.Session.UpdateOne(old).SetRevoked(true).Exec(ctx); err != nil {
			return "", time.Time{}, internal(err)
		}
	}
	if !row.ExpiresAt.After(h.svc.now()) || (row.ConfirmationExpiresAt != nil && !row.ConfirmationExpiresAt.After(h.svc.now())) {
		return "", time.Time{}, stateInvalid()
	}
	token, hash, err := session.NewToken()
	if err != nil {
		return "", time.Time{}, internal(err)
	}
	expires := h.svc.now().Add(h.svc.SessionLifetime())
	if err = tx.BrowserContext.UpdateOne(b).AddGeneration(1).SetExpiresAt(h.svc.now().Add(h.svc.browserLifetime())).Exec(ctx); err != nil {
		return "", time.Time{}, internal(err)
	}
	if _, err = tx.Session.Create().SetUserID(uid).SetTokenHash(hash).SetExpiresAt(expires).SetAuthVersion(version).SetBrowserContextID(b.ID).SetBrowserGeneration(b.Generation + 1).Save(ctx); err != nil {
		return "", time.Time{}, internal(err)
	}
	if err = tx.OAuthBrowserState.UpdateOneID(row.ID).SetResultStatus("authenticated").ClearPkceVerifierCiphertext().Exec(ctx); err != nil {
		return "", time.Time{}, internal(err)
	}
	if err = tx.Commit(); err != nil {
		return "", time.Time{}, internal(err)
	}
	return token, expires, nil
}

func (h *ExternalHandler) returnURL(row *ent.OAuthBrowserState) string {
	target, err := h.navigation.Resolve(row.ReturnAppID, row.ReturnPath)
	if err != nil {
		return ""
	}
	return target.URL
}
func (h *ExternalHandler) GetExternalFlow(ctx context.Context, req *connect.Request[bffv1.GetExternalFlowRequest]) (*connect.Response[bffv1.GetExternalFlowResponse], error) {
	row, err := h.flow(ctx, req.Header(), req.Msg.GetFlowId())
	if err != nil {
		return nil, err
	}
	msg := &bffv1.GetExternalFlowResponse{Status: row.ResultStatus, ReturnUrl: h.returnURL(row)}
	if row.ResultStatus == "registration_pending" {
		if !h.allowsFlow(row) {
			return nil, providerDisabled()
		}
		pending, err := h.users.ExternalRegistration(ctx, h.ref(row))
		if err != nil {
			return nil, err
		}
		msg.CandidateEmail = pending.GetCandidateEmail()
		msg.DisplayName = pending.GetDisplayName()
		msg.AvatarUrl = pending.GetAvatarUrl()
		msg.EmailConfirmed = pending.GetEmailConfirmed()
	}
	res := connect.NewResponse(msg)
	noSecrets(res.Header())
	return res, nil
}
func (h *ExternalHandler) RequestExternalRegistrationEmail(ctx context.Context, req *connect.Request[bffv1.RequestExternalRegistrationEmailRequest]) (*connect.Response[bffv1.RequestExternalRegistrationEmailResponse], error) {
	row, err := h.flow(ctx, req.Header(), req.Msg.GetFlowId())
	if err != nil {
		return nil, err
	}
	if row.ResultStatus != "registration_pending" || row.Intent != oauthbrowserstate.IntentLogin {
		return nil, stateInvalid()
	}
	if !h.allowsFlow(row) {
		return nil, providerDisabled()
	}
	if err = h.svc.db.OAuthBrowserState.UpdateOneID(row.ID).SetConfirmationExpiresAt(h.svc.now().Add(h.cfg.ConfirmationLifetime())).Exec(ctx); err != nil {
		return nil, internal(err)
	}
	if err = h.users.RequestExternalEmail(ctx, h.ref(row), req.Msg.GetEmail()); err != nil {
		return nil, err
	}
	res := connect.NewResponse(&bffv1.RequestExternalRegistrationEmailResponse{Message: registrationNotice})
	noSecrets(res.Header())
	return res, nil
}
func (h *ExternalHandler) ConfirmExternalRegistrationEmail(ctx context.Context, req *connect.Request[bffv1.ConfirmExternalRegistrationEmailRequest]) (*connect.Response[bffv1.ConfirmExternalRegistrationEmailResponse], error) {
	if _, err := uuid.Parse(req.Msg.GetChallengeId()); err != nil || len(req.Msg.GetToken()) > 512 {
		return nil, stateInvalid()
	}
	if err := h.users.ConfirmExternalEmail(ctx, req.Msg.GetChallengeId(), req.Msg.GetToken()); err != nil {
		return nil, err
	}
	res := connect.NewResponse(&bffv1.ConfirmExternalRegistrationEmailResponse{})
	noSecrets(res.Header())
	return res, nil
}
func (h *ExternalHandler) CompleteExternalRegistration(ctx context.Context, req *connect.Request[bffv1.CompleteExternalRegistrationRequest]) (*connect.Response[bffv1.CompleteExternalRegistrationResponse], error) {
	row, err := h.flow(ctx, req.Header(), req.Msg.GetFlowId())
	if err != nil {
		return nil, err
	}
	if row.Intent != oauthbrowserstate.IntentLogin || row.ConsumedAt == nil {
		return nil, stateInvalid()
	}
	if row.ConfirmationExpiresAt != nil && !row.ConfirmationExpiresAt.After(h.svc.now()) {
		return nil, stateInvalid()
	}
	if !h.allowsFlow(row) {
		return nil, providerDisabled()
	}
	body, _ := json.Marshal([]string{req.Msg.GetMutationId(), req.Msg.GetDisplayName(), req.Msg.GetAvatarUrl()})
	hash := session.HashToken(string(body))
	first := row.FinalClaimedAt == nil
	if first {
		if row.ResultStatus != "registration_pending" {
			return nil, stateInvalid()
		}
		pending, err := h.users.ExternalRegistration(ctx, h.ref(row))
		if err != nil {
			return nil, err
		}
		if !pending.GetEmailConfirmed() {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("email confirmation required"))
		}
		n, err := h.svc.db.OAuthBrowserState.Update().Where(oauthbrowserstate.ID(row.ID), oauthbrowserstate.FinalClaimedAtIsNil()).SetFinalClaimedAt(h.svc.now()).SetFinalMutationID(req.Msg.GetMutationId()).SetFinalRequestHash(hash).SetResultStatus("registering").Save(ctx)
		if err != nil {
			return nil, internal(err)
		}
		if n != 1 {
			return nil, stateInvalid()
		}
	} else if row.FinalMutationID != req.Msg.GetMutationId() || row.FinalRequestHash != hash {
		return nil, stateInvalid()
	}
	uid, err := h.users.CreateExternalAccount(ctx, &usersrvv1.CompleteExternalRegistrationRequest{Challenge: h.ref(row), MutationId: req.Msg.GetMutationId(), DisplayName: req.Msg.GetDisplayName(), AvatarUrl: req.Msg.GetAvatarUrl()})
	if err != nil {
		// A definitive validation/transaction refusal admits a corrected explicit
		// submission. Unknown delivery outcomes retain the claim permanently, so
		// retrying a creation receipt can never become a second login authority.
		if first && (connect.CodeOf(err) == connect.CodeInvalidArgument || connect.CodeOf(err) == connect.CodeFailedPrecondition || connect.CodeOf(err) == connect.CodeAlreadyExists) {
			_, _ = h.svc.db.OAuthBrowserState.Update().Where(oauthbrowserstate.ID(row.ID), oauthbrowserstate.ResultStatus("registering"), oauthbrowserstate.FinalRequestHash(hash)).ClearFinalClaimedAt().ClearFinalMutationID().ClearFinalRequestHash().SetResultStatus("registration_pending").Save(ctx)
		}
		return nil, err
	}
	res := connect.NewResponse(&bffv1.CompleteExternalRegistrationResponse{ReturnUrl: h.returnURL(row), LoginRequired: !first})
	noSecrets(res.Header())
	if first {
		// First admitted commit can establish one session; replayed result references
		// never mint one. usersrv's fresh account always starts at auth_version 1.
		token, expires, err := h.openExternalSession(ctx, req.Header(), row, uid, 1)
		if err != nil {
			return nil, err
		}
		session.SetSessionCookie(res.Header(), h.svc.cookies, token, expires)
	}
	return res, nil
}

// PurgeExternal removes expired temporary material even if no callback arrived.
func (s *Service) PurgeExternal(ctx context.Context) error {
	if _, err := s.db.OAuthBrowserState.Delete().Where(oauthbrowserstate.ExpiresAtLTE(s.now())).Exec(ctx); err != nil {
		return internal(err)
	}
	_, err := s.db.BrowserContext.Delete().Where(browsercontext.ExpiresAtLTE(s.now())).Exec(ctx)
	if err != nil {
		return internal(err)
	}
	return nil
}
