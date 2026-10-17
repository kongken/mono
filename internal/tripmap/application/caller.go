package application

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/tripmap/ent"
)

// Caller is the authenticated user for a business RPC (from BFF internal credentials).
type Caller struct {
	UserID string
}

var errUnauthenticated = connect.NewError(connect.CodeUnauthenticated, errors.New("sign in required"))

func principal(ctx context.Context) (Caller, error) {
	if id, ok := rpcauth.UserIDFrom(ctx); ok && id != "" {
		return Caller{UserID: id}, nil
	}
	return Caller{}, errUnauthenticated
}

func ctxWithAccountSchema(ctx context.Context, st *Store, userID string) (context.Context, error) {
	u, err := st.DB.User.Get(ctx, userID)
	if ent.IsNotFound(err) {
		return ctx, errUnauthenticated
	}
	if err != nil {
		return ctx, internalError(err)
	}
	return withSchemasFromRPC(ctx, u.SchemaVersion), nil
}
