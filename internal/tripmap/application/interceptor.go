package application

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/tripmap/auth"
	"github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/ent/accountdeletionreceipt"
	"github.com/kongken/mono/internal/tripmap/ent/session"
	"github.com/kongken/mono/pkg/proto/tripmap/v1/tripmapv1connect"
)

// PublicProcedures can be called without a session.
var PublicProcedures = map[string]bool{
	tripmapv1connect.AuthServiceRegisterProcedure:           true,
	tripmapv1connect.AuthServiceLoginProcedure:              true,
}

// touchInterval limits how often last_used_at is written.
const touchInterval = time.Minute

var errUnauthenticated = connect.NewError(connect.CodeUnauthenticated, errors.New("sign in required"))

// NewAuthInterceptor authenticates every procedure not in PublicProcedures with
// a bearer session and puts the Principal in the request context. Unknown,
// revoked, expired or disabled-user sessions are all reported identically.
func NewAuthInterceptor(src StoreSource) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if PublicProcedures[req.Spec().Procedure] {
				return next(ctx, req)
			}
			st, err := storeFrom(src)
			if err != nil {
				return nil, err
			}
			header := req.Header().Get("Authorization")
			token, ok := strings.CutPrefix(header, "Bearer ")
			if token = strings.TrimSpace(token); !ok || token == "" {
				return nil, errUnauthenticated
			}
			now := time.Now().UTC()
			sess, err := st.DB.Session.Query().
				Where(session.TokenHash(auth.HashToken(token)), session.Revoked(false), session.ExpiresAtGT(now)).
				WithUser().Only(ctx)
			if err != nil {
				if !ent.IsNotFound(err) {
					slog.Error("session lookup failed", "error", err)
					return nil, internalError(err)
				}
				if req.Spec().Procedure == tripmapv1connect.AuthServiceDeleteAccountProcedure {
					exists, receiptErr := st.DB.AccountDeletionReceipt.Query().Where(accountdeletionreceipt.TokenHash(auth.HashToken(token)), accountdeletionreceipt.ExpiresAtGT(now)).Exist(ctx)
					if receiptErr != nil {
						return nil, internalError(receiptErr)
					}
					if exists {
						return next(context.WithValue(ctx, deletionReceiptContextKey{}, true), req)
					}
				}
				return nil, errUnauthenticated
			}
			if sess.Edges.User == nil || (sess.Edges.User.Disabled && !(sess.Edges.User.DeletionRequestedAt != nil && (req.Spec().Procedure == tripmapv1connect.AuthServiceDeleteAccountProcedure || req.Spec().Procedure == tripmapv1connect.AuthServiceMeProcedure || req.Spec().Procedure == tripmapv1connect.AuthServiceLogoutProcedure))) {
				return nil, errUnauthenticated
			}
			if sess.LastUsedAt == nil || now.Sub(*sess.LastUsedAt) > touchInterval {
				if err := st.DB.Session.UpdateOneID(sess.ID).SetLastUsedAt(now).Exec(ctx); err != nil {
					slog.Warn("touch session failed", "error", err)
				}
			}
			procedure := req.Spec().Procedure
			if !strings.Contains(procedure, ".AuthService/") && !strings.Contains(procedure, ".RegionService/") && procedure != tripmapv1connect.FootprintServiceSyncChangesProcedure {
				if err := checkRegionVersion(req.Header().Get(regionVersionHeader)); err != nil {
					return nil, err
				}
			}
			ctx = withSchemas(ctx, req.Header(), sess.Edges.User.SchemaVersion)
			return next(auth.WithPrincipal(ctx, auth.Principal{UserID: sess.UserID, SessionID: sess.ID}), req)
		}
	})
}
