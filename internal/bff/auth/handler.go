package auth

import (
	"context"
	"crypto/subtle"
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

// deletionOf finds the deletion this browser can see: the one its recovery Cookie opens, and, for
// a deletion that began before the recovery Cookie existed, the one the session Cookie opened.
// token is the credential that opened it. Only digests are compared; no ID opens a workflow.
func (h *Handler) deletionOf(ctx context.Context, header http.Header) (*ent.AccountDeletion, string, error) {
	var tokens []string
	for _, t := range []string{session.RecoveryTokenFromRequest(header), session.TokenFromRequest(header)} {
		if t != "" && !slices.Contains(tokens, t) {
			tokens = append(tokens, t)
		}
	}
	for _, t := range tokens {
		wf, err := h.svc.DeletionByCookie(ctx, t)
		if err == nil {
			return wf, t, nil
		}
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			return nil, "", err
		}
	}
	return nil, "", h.noDeletion(header)
}

// noDeletion is the answer when the browser holds nothing that opens a deletion; a recovery
// Cookie that opens nothing (a finished deletion whose receipt expired, or one that was never
// begun) is removed with it.
func (h *Handler) noDeletion(header http.Header) error {
	err := connect.NewError(connect.CodeUnauthenticated, errors.New("sign in required"))
	if session.RecoveryTokenFromRequest(header) != "" {
		err.Meta().Add("Set-Cookie", session.ClearRecoveryCookieValue(h.svc.cookies))
	}
	return err
}

// isDeletedSession reports whether the request's session Cookie is the very token the deletion
// began with and its session is revoked for good (the deletion is past its begin step), as opposed
// to an account signed in since.
func isDeletedSession(req http.Header, wf *ent.AccountDeletion) bool {
	sid := session.TokenFromRequest(req)
	return wf.Stage != schema.DeletionStageBegin && sid != "" && subtle.ConstantTimeCompare([]byte(session.HashToken(sid)), []byte(wf.TokenHash)) == 1
}

// deletionCookies applies to a response about wf the opening credential's cookie state: an
// unfinished deletion keeps its recovery Cookie (renewed, and set for a deletion that began
// before it existed); a finished one removes it. The session Cookie is removed only when it is the
// very (revoked) token of the deleted account, never the Cookie of an account signed in since.
func (h *Handler) deletionCookies(header http.Header, req http.Header, wf *ent.AccountDeletion, token string) {
	if wf.Stage != schema.DeletionStageDone {
		session.SetRecoveryCookie(header, h.svc.cookies, token, h.svc.now().Add(recoveryCookieTTL))
	} else {
		session.ClearRecoveryCookie(header, h.svc.cookies)
	}
	if isDeletedSession(req, wf) {
		session.ClearSessionCookie(header, h.svc.cookies)
	}
}

// keepRecovery adds the cookie state to the failure of a deletion step that has a workflow, so a
// failing product does not stop the browser from adopting or renewing its credential.
func (h *Handler) keepRecovery(err error, req http.Header, wf *ent.AccountDeletion, token string) error {
	var ce *connect.Error
	if wf == nil || wf.Stage == schema.DeletionStageDone || !errors.As(err, &ce) {
		return err
	}
	ce.Meta().Add("Set-Cookie", session.RecoveryCookie(h.svc.cookies, token, h.svc.now().Add(recoveryCookieTTL)))
	if isDeletedSession(req, wf) {
		ce.Meta().Add("Set-Cookie", session.ClearSessionCookieValue(h.svc.cookies))
	}
	return err
}

// DeleteAccount starts, or continues with the same mutation_id, the deletion of the account.
// The ingress admits it for a live session or for a browser that already started a deletion (its
// session no longer passes the account check once the account is deleting).
//
// Nothing irreversible starts before the browser has handed back the recovery Cookie: the first
// call of a browser that has not stored it only sets it and answers FAILED_PRECONDITION; the
// repeat carries it and begins. So a lost answer to the beginning can always be recovered.
func (h *Handler) DeleteAccount(ctx context.Context, req *connect.Request[bffv1.DeleteAccountRequest]) (*connect.Response[bffv1.DeleteAccountResponse], error) {
	wf, token, err := h.deletionOf(ctx, req.Header())
	if err != nil && connect.CodeOf(err) != connect.CodeUnauthenticated {
		return nil, err
	}
	if wf != nil && wf.Stage == schema.DeletionStageDone && wf.MutationID != req.Msg.GetMutationId() {
		wf = nil // a finished deletion's receipt does not stop another account from beginning
	}
	if wf != nil {
		if wf.MutationID != req.Msg.GetMutationId() {
			// The browser's recovery Cookie opens another, unfinished deletion; replacing it would
			// lose that deletion's only credential.
			return nil, errDeletionPendingHere
		}
		wf, err = h.svc.ContinueDeletion(ctx, token)
	} else {
		bs, ok := session.FromBrowser(ctx)
		if !ok {
			return nil, errUnauth
		}
		token = session.TokenFromRequest(req.Header())
		if subtle.ConstantTimeCompare([]byte(session.RecoveryTokenFromRequest(req.Header())), []byte(token)) != 1 {
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
	wf, token, err := h.deletionOf(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	res := connect.NewResponse(&bffv1.GetAccountDeletionResponse{Deletion: DeletionMessage(wf)})
	h.deletionCookies(res.Header(), req.Header(), wf, token)
	return res, nil
}

func (h *Handler) RetryAccountDeletion(ctx context.Context, req *connect.Request[bffv1.RetryAccountDeletionRequest]) (*connect.Response[bffv1.RetryAccountDeletionResponse], error) {
	wf, token, err := h.deletionOf(ctx, req.Header())
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
	wf, _, err := h.deletionOf(ctx, header)
	return err == nil && wf != nil
}
