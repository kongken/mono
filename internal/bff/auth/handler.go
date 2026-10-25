package auth

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/ent"
	"github.com/kongken/mono/internal/bff/ent/schema"
	"github.com/kongken/mono/internal/bff/navigation"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/bff/usersrv"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Neutral answers: a well-formed address always gets the same text, whether or not
// the address can register or has an account.
const (
	registrationNotice = "若该邮箱可注册，验证邮件将发送，请检查收件箱。"
	resetNotice        = "若该邮箱对应可用账号，重置邮件将发送，请检查收件箱。"
)

// Handler serves bff.v1.AuthService and bff.v1.UserService. The ingress interceptor
// has already applied CSRF/Origin, the anonymous policy and, for protected
// methods, the usersrv re-check; handlers read the verified session from ctx.
type Handler struct {
	svc           *Service
	csrfCookieTTL time.Duration
	navigation    *navigation.Registry
}

// WithNavigation sets the app registry ResolveNavigation answers from. Without one the
// method refuses every request.
func (h *Handler) WithNavigation(r *navigation.Registry) *Handler {
	h.navigation = r
	return h
}

func NewHandler(svc *Service, csrfTTL time.Duration) *Handler {
	if csrfTTL <= 0 {
		csrfTTL = 24 * time.Hour
	}
	return &Handler{svc: svc, csrfCookieTTL: csrfTTL}
}

// bindReturn validates the return context of a mail request against the navigation
// registry and returns the canonical form to bind to the challenge. No context is
// fine. A context the registry refuses is INVALID_ARGUMENT, and a context sent to a BFF
// that has no registry is FAILED_PRECONDITION (nothing can vouch for it, and silently
// dropping it would send the user to a flow that never returns them); both come before
// anything is counted, written or sent to usersrv.
func (h *Handler) bindReturn(appID, path string) (usersrv.Return, error) {
	if appID == "" && path == "" {
		return usersrv.Return{}, nil
	}
	if h.navigation == nil {
		return usersrv.Return{}, connect.NewError(connect.CodeFailedPrecondition, errors.New("navigation is not configured"))
	}
	target, err := h.navigation.Resolve(appID, path)
	if err != nil {
		return usersrv.Return{}, connect.NewError(connect.CodeInvalidArgument, errors.New("navigation target not allowed"))
	}
	return usersrv.Return{AppID: target.AppID, Path: target.Path}, nil
}

// boundReturn re-validates the context a consumed challenge carries. The registry may
// have changed since the mail was requested, and the challenge may predate the
// context: whatever is not allowed now is dropped, never an error, because the account
// step already succeeded and its user must land somewhere safe (OzID).
func (h *Handler) boundReturn(ret usersrv.Return) (appID, path string) {
	if ret.AppID == "" || h.navigation == nil {
		return "", ""
	}
	target, err := h.navigation.Resolve(ret.AppID, ret.Path)
	if err != nil {
		return "", ""
	}
	return target.AppID, target.Path
}

func (h *Handler) RequestRegistration(ctx context.Context, req *connect.Request[bffv1.RequestRegistrationRequest]) (*connect.Response[bffv1.RequestRegistrationResponse], error) {
	ret, err := h.bindReturn(req.Msg.GetAppId(), req.Msg.GetReturnPath())
	if err != nil {
		return nil, err
	}
	if err := h.svc.users.RequestRegistration(ctx, req.Msg.GetEmail(), ret); err != nil {
		return nil, err
	}
	return connect.NewResponse(&bffv1.RequestRegistrationResponse{Message: registrationNotice}), nil
}

func (h *Handler) CompleteRegistration(ctx context.Context, req *connect.Request[bffv1.CompleteRegistrationRequest]) (*connect.Response[bffv1.CompleteRegistrationResponse], error) {
	ret, err := h.svc.users.CompleteRegistration(ctx, req.Msg.GetToken(), req.Msg.GetPassword(), req.Msg.GetDisplayName())
	if err != nil {
		return nil, err
	}
	// No session: verifying an address proves the mailbox, not a login.
	appID, path := h.boundReturn(ret)
	return connect.NewResponse(&bffv1.CompleteRegistrationResponse{AppId: appID, ReturnPath: path}), nil
}

func (h *Handler) LoginWithEmail(ctx context.Context, req *connect.Request[bffv1.LoginWithEmailRequest]) (*connect.Response[bffv1.LoginWithEmailResponse], error) {
	// A sign-in neither reads nor writes the deletion recovery Cookie: an unfinished deletion of
	// another account in this browser is untouched by it.
	msg, token, err := h.svc.Login(ctx, req.Msg.GetEmail(), req.Msg.GetPassword())
	if err != nil {
		return nil, err
	}
	res := connect.NewResponse(msg)
	session.SetSessionCookie(res.Header(), h.svc.cookies, token, msg.GetExpiresAt().AsTime())
	return res, nil
}

func (h *Handler) RequestPasswordReset(ctx context.Context, req *connect.Request[bffv1.RequestPasswordResetRequest]) (*connect.Response[bffv1.RequestPasswordResetResponse], error) {
	ret, err := h.bindReturn(req.Msg.GetAppId(), req.Msg.GetReturnPath())
	if err != nil {
		return nil, err
	}
	if err := h.svc.users.RequestPasswordReset(ctx, req.Msg.GetEmail(), ret); err != nil {
		return nil, err
	}
	return connect.NewResponse(&bffv1.RequestPasswordResetResponse{Message: resetNotice}), nil
}

func (h *Handler) ResetPassword(ctx context.Context, req *connect.Request[bffv1.ResetPasswordRequest]) (*connect.Response[bffv1.ResetPasswordResponse], error) {
	ret, err := h.svc.users.ResetPassword(ctx, req.Msg.GetToken(), req.Msg.GetNewPassword())
	if err != nil {
		return nil, err
	}
	appID, path := h.boundReturn(ret)
	return connect.NewResponse(&bffv1.ResetPasswordResponse{AppId: appID, ReturnPath: path}), nil
}

func (h *Handler) Logout(ctx context.Context, req *connect.Request[bffv1.LogoutRequest]) (*connect.Response[bffv1.LogoutResponse], error) {
	// Signing out ends the session and clears the session Cookie, nothing else: the deletion
	// recovery Cookie of an unfinished deletion is a different credential and stays.
	if err := h.svc.Logout(ctx, session.TokenFromRequest(req.Header())); err != nil {
		return nil, err
	}
	res := connect.NewResponse(&bffv1.LogoutResponse{})
	session.ClearSessionCookie(res.Header(), h.svc.cookies)
	return res, nil
}

func (h *Handler) Me(ctx context.Context, _ *connect.Request[bffv1.MeRequest]) (*connect.Response[bffv1.MeResponse], error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok {
		return nil, errUnauth
	}
	msg, err := h.svc.Me(ctx, bs)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(msg), nil
}

func (h *Handler) ListSessions(ctx context.Context, _ *connect.Request[bffv1.ListSessionsRequest]) (*connect.Response[bffv1.ListSessionsResponse], error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok {
		return nil, errUnauth
	}
	msg, err := h.svc.ListSessions(ctx, bs.UserID, bs.SessionID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(msg), nil
}

func (h *Handler) RevokeSession(ctx context.Context, req *connect.Request[bffv1.RevokeSessionRequest]) (*connect.Response[bffv1.RevokeSessionResponse], error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok {
		return nil, errUnauth
	}
	if err := h.svc.RevokeSession(ctx, bs.UserID, req.Msg.GetId()); err != nil {
		return nil, err
	}
	res := connect.NewResponse(&bffv1.RevokeSessionResponse{})
	if req.Msg.GetId() == bs.SessionID {
		// The caller ended its own session: same as signing out, so the Cookie goes too.
		session.ClearSessionCookie(res.Header(), h.svc.cookies)
	}
	return res, nil
}

func (h *Handler) RevokeOtherSessions(ctx context.Context, _ *connect.Request[bffv1.RevokeOtherSessionsRequest]) (*connect.Response[bffv1.RevokeOtherSessionsResponse], error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok {
		return nil, errUnauth
	}
	n, err := h.svc.RevokeOtherSessions(ctx, bs)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&bffv1.RevokeOtherSessionsResponse{Revoked: int32(n)}), nil
}

func (h *Handler) Reauthenticate(ctx context.Context, req *connect.Request[bffv1.ReauthenticateRequest]) (*connect.Response[bffv1.ReauthenticateResponse], error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok {
		return nil, errUnauth
	}
	at, err := h.svc.Reauthenticate(ctx, bs, req.Msg.GetPassword())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&bffv1.ReauthenticateResponse{AuthenticatedAt: timestamppb.New(at)}), nil
}

func (h *Handler) ChangePassword(ctx context.Context, req *connect.Request[bffv1.ChangePasswordRequest]) (*connect.Response[bffv1.ChangePasswordResponse], error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok {
		return nil, errUnauth
	}
	if err := h.svc.ChangePassword(ctx, bs, req.Msg.GetCurrentPassword(), req.Msg.GetNewPassword()); err != nil {
		return nil, err
	}
	res := connect.NewResponse(&bffv1.ChangePasswordResponse{})
	// This device must sign in again with the new password too.
	session.ClearSessionCookie(res.Header(), h.svc.cookies)
	return res, nil
}

func (h *Handler) UpdateMyProfile(ctx context.Context, req *connect.Request[bffv1.UpdateMyProfileRequest]) (*connect.Response[bffv1.UpdateMyProfileResponse], error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok {
		return nil, errUnauth
	}
	u, err := h.svc.UpdateProfile(ctx, bs, req.Msg.GetDisplayName(), req.Msg.GetAvatarUrl())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&bffv1.UpdateMyProfileResponse{User: u}), nil
}

// recoveryCookieTTL is how long the deletion recovery Cookie lives; every read or retry of an
// unfinished deletion renews it, and the finished deletion (or an expired receipt) removes it.
const recoveryCookieTTL = 30 * 24 * time.Hour

// errRecoveryNotStored refuses to begin a deletion until the browser has handed back the
// recovery Cookie the response carries; the client repeats the same call (see createAccountApi).
const errRecoveryNotStored = "account deletion recovery cookie is not stored yet; repeat the request"

// deletionCandidates are the credentials a request offers to open a deletion, each paired with the
// deletion it opens: the recovery Cookies of any generation (bounded and validated by
// session.RecoveryTokens), the fixed-name Cookie of the earlier build, and, for a deletion that began
// before recovery Cookies existed, the session Cookie. Only digests are compared; no ID opens a
// workflow. A candidate that opens nothing is simply not one, and never causes a Cookie to be written.
type deletionCandidate struct {
	wf    *ent.AccountDeletion
	token string
}

func (h *Handler) deletionCandidates(ctx context.Context, header http.Header) ([]deletionCandidate, error) {
	var tokens []string
	for _, t := range append(session.RecoveryTokens(header), session.LegacyRecoveryToken(header), session.TokenFromRequest(header)) {
		if t != "" && !slices.Contains(tokens, t) {
			tokens = append(tokens, t)
		}
	}
	var found []deletionCandidate
	for _, t := range tokens {
		wf, err := h.svc.DeletionByCookie(ctx, t)
		if err != nil {
			if connect.CodeOf(err) != connect.CodeUnauthenticated {
				return nil, err
			}
			continue
		}
		found = append(found, deletionCandidate{wf: wf, token: t})
	}
	return found, nil
}

// preferred reports whether a is a better answer than b when both belong to this browser: an
// unfinished deletion beats a receipt, the older unfinished one beats the newer, the newer receipt
// beats the older.
func preferred(a, b *ent.AccountDeletion) bool {
	aDone, bDone := a.Stage == schema.DeletionStageDone, b.Stage == schema.DeletionStageDone
	switch {
	case aDone != bDone:
		return !aDone
	case aDone:
		return a.CreatedAt.After(b.CreatedAt)
	default:
		return a.CreatedAt.Before(b.CreatedAt)
	}
}

// deletionOf chooses the one deletion this request is about, so that several credentials in one
// browser never get mixed up: the candidate whose mutation_id the request names (DeleteAccount), else
// the oldest unfinished one, else the newest receipt. Several unfinished deletions in one browser can
// only arise from two begins racing in different tabs; each is then continued on its own, in turn.
// token is the credential that opened the chosen deletion.
func (h *Handler) deletionOf(ctx context.Context, header http.Header, mutationID string) (*ent.AccountDeletion, string, error) {
	found, err := h.deletionCandidates(ctx, header)
	if err != nil {
		return nil, "", err
	}
	var best *deletionCandidate
	for i := range found {
		c := &found[i]
		switch {
		case mutationID != "" && c.wf.MutationID == mutationID:
			return c.wf, c.token, nil
		case best == nil || preferred(c.wf, best.wf):
			best = c
		}
	}
	if best == nil {
		// Nothing is cleared here, whatever Cookies came: a Cookie that opens nothing may be one whose
		// deletion has not begun yet, and a late answer must never take that credential away.
		return nil, "", errUnauth
	}
	return best.wf, best.token, nil
}

// deletionCookies applies to a response about wf the recovery Cookie state of the generation token
// belongs to, and of no other: an unfinished deletion keeps it (the same name and value, renewed;
// it is also how a deletion that began before recovery Cookies existed gets one); a finished one
// removes it. The Cookie of the earlier build is removed once its generation has moved. The session
// Cookie is never touched by these RPCs: a revoked session is dead on the server.
func (h *Handler) deletionCookies(header http.Header, req http.Header, wf *ent.AccountDeletion, token string) {
	if wf.Stage != schema.DeletionStageDone {
		session.SetRecoveryCookie(header, h.svc.cookies, token, h.svc.now().Add(recoveryCookieTTL))
	} else {
		session.ClearRecoveryCookie(header, h.svc.cookies, token)
	}
	if session.LegacyRecoveryToken(req) == token {
		session.ClearLegacyRecoveryCookie(header, h.svc.cookies)
	}
}

// keepRecovery adds the recovery Cookie state to the failure of a deletion step that has a workflow, so a
// failing product does not stop the browser from adopting or renewing its credential.
func (h *Handler) keepRecovery(err error, req http.Header, wf *ent.AccountDeletion, token string) error {
	var ce *connect.Error
	if wf == nil || wf.Stage == schema.DeletionStageDone || !errors.As(err, &ce) {
		return err
	}
	ce.Meta().Add("Set-Cookie", session.RecoveryCookie(h.svc.cookies, token, h.svc.now().Add(recoveryCookieTTL)))
	if session.LegacyRecoveryToken(req) == token {
		h2 := http.Header{}
		session.ClearLegacyRecoveryCookie(h2, h.svc.cookies)
		ce.Meta().Add("Set-Cookie", h2.Get("Set-Cookie"))
	}
	return err
}

// DeleteAccount starts, or continues with the same mutation_id, the deletion of the account.
// The ingress admits it for a live session or for a browser that already started a deletion (its
// session no longer passes the account check once the account is deleting).
//
// Nothing irreversible starts before the browser has handed back the recovery Cookie of this
// session's generation: the first call of a browser that has not stored it only sets it and answers
// FAILED_PRECONDITION; the repeat carries it and begins. So a lost answer to the beginning can always
// be recovered. No response of this RPC writes the session Cookie.
func (h *Handler) DeleteAccount(ctx context.Context, req *connect.Request[bffv1.DeleteAccountRequest]) (*connect.Response[bffv1.DeleteAccountResponse], error) {
	wf, token, err := h.deletionOf(ctx, req.Header(), req.Msg.GetMutationId())
	if err != nil && connect.CodeOf(err) != connect.CodeUnauthenticated {
		return nil, err
	}
	if wf != nil && wf.Stage == schema.DeletionStageDone && wf.MutationID != req.Msg.GetMutationId() {
		wf = nil // a finished deletion's receipt does not stop another account from beginning
	}
	if wf != nil {
		if wf.MutationID != req.Msg.GetMutationId() {
			// The browser's recovery Cookie opens another, unfinished deletion; beginning one more
			// would leave two credentials for the browser to keep apart.
			return nil, errDeletionPendingHere
		}
		wf, err = h.svc.ContinueDeletion(ctx, token)
	} else {
		bs, ok := session.FromBrowser(ctx)
		if !ok {
			return nil, errUnauth
		}
		token = session.TokenFromRequest(req.Header())
		if !slices.Contains(session.RecoveryTokens(req.Header()), token) && session.LegacyRecoveryToken(req.Header()) != token {
			e := connect.NewError(connect.CodeFailedPrecondition, errors.New(errRecoveryNotStored))
			e.Meta().Add("Set-Cookie", session.RecoveryCookie(h.svc.cookies, token, h.svc.now().Add(recoveryCookieTTL)))
			return nil, e
		}
		wf, err = h.svc.StartDeletion(ctx, bs, token, req.Msg)
	}
	if err != nil {
		return nil, h.keepRecovery(err, req.Header(), wf, token)
	}
	res := connect.NewResponse(&bffv1.DeleteAccountResponse{Deletion: DeletionMessage(wf)})
	h.deletionCookies(res.Header(), req.Header(), wf, token)
	return res, nil
}

func (h *Handler) GetAccountDeletion(ctx context.Context, req *connect.Request[bffv1.GetAccountDeletionRequest]) (*connect.Response[bffv1.GetAccountDeletionResponse], error) {
	wf, token, err := h.deletionOf(ctx, req.Header(), "")
	if err != nil {
		return nil, err
	}
	res := connect.NewResponse(&bffv1.GetAccountDeletionResponse{Deletion: DeletionMessage(wf)})
	h.deletionCookies(res.Header(), req.Header(), wf, token)
	return res, nil
}

func (h *Handler) RetryAccountDeletion(ctx context.Context, req *connect.Request[bffv1.RetryAccountDeletionRequest]) (*connect.Response[bffv1.RetryAccountDeletionResponse], error) {
	wf, token, err := h.deletionOf(ctx, req.Header(), "")
	if err != nil {
		return nil, err
	}
	if wf, err = h.svc.ContinueDeletion(ctx, token); err != nil {
		return nil, h.keepRecovery(err, req.Header(), wf, token)
	}
	res := connect.NewResponse(&bffv1.RetryAccountDeletionResponse{Deletion: DeletionMessage(wf)})
	h.deletionCookies(res.Header(), req.Header(), wf, token)
	return res, nil
}

// ResolveNavigation validates a return target against the registry. It is anonymous and
// read-only: no session, account or usersrv call is involved.
func (h *Handler) ResolveNavigation(_ context.Context, req *connect.Request[bffv1.ResolveNavigationRequest]) (*connect.Response[bffv1.ResolveNavigationResponse], error) {
	if h.navigation == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("navigation is not configured"))
	}
	target, err := h.navigation.Resolve(req.Msg.GetAppId(), req.Msg.GetReturnPath())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("navigation target not allowed"))
	}
	return connect.NewResponse(&bffv1.ResolveNavigationResponse{
		AppId: target.AppID, ReturnPath: target.Path, ReturnUrl: target.URL, OzidOrigin: target.OzIDOrigin,
	}), nil
}

func (h *Handler) IssueCSRFToken(_ context.Context, req *connect.Request[bffv1.IssueCSRFTokenRequest]) (*connect.Response[bffv1.IssueCSRFTokenResponse], error) {
	token, err := h.svc.IssueCSRF()
	if err != nil {
		return nil, err
	}
	expires := time.Now().UTC().Add(h.csrfCookieTTL)
	res := connect.NewResponse(&bffv1.IssueCSRFTokenResponse{Token: token})
	session.SetCSRFCookie(res.Header(), h.svc.cookies, token, expires)
	return res, nil
}

// Authenticate implements ingress session resolution.
func (h *Handler) Authenticate(ctx context.Context, header http.Header) (session.BrowserSession, error) {
	return h.svc.ResolveSession(ctx, session.TokenFromRequest(header))
}

// DeletionKnown implements the ingress hook that lets DeleteAccount through for a browser
// whose deletion is already persisted.
func (h *Handler) DeletionKnown(ctx context.Context, header http.Header) bool {
	wf, _, err := h.deletionOf(ctx, header, "")
	return err == nil && wf != nil
}
