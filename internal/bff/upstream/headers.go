package upstream

import (
	"context"
	"net/http"

	"github.com/kongken/mono/internal/platform/rpcauth"
)

func WithTripmapHeaders(ctx context.Context, h http.Header) context.Context {
	call := rpcauth.CallFrom(ctx)
	call.TripmapSchema = h.Get("X-Tripmap-Schema")
	call.TripmapRegions = h.Get("X-Tripmap-Regions")
	return rpcauth.WithCall(ctx, call)
}
