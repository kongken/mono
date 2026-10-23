package rpcauth

import (
	"context"
	"strings"

	"github.com/kongken/mono/internal/platform/servicetoken"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var (
	errUnauthenticated = status.Error(codes.Unauthenticated, "invalid or missing service credential")
	errPermission      = status.Error(codes.PermissionDenied, "required role missing")
)

type mdCarrier metadata.MD

func (m mdCarrier) Get(key string) string {
	if v := metadata.MD(m).Get(key); len(v) > 0 {
		return v[0]
	}
	return ""
}
func (m mdCarrier) Set(key, value string) { metadata.MD(m).Set(key, value) }
func (m mdCarrier) Keys() []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// authenticate verifies the credential for fullMethod and returns the context
// handlers run under. Every credential failure is the same UNAUTHENTICATED
// error; a verified credential without the declared required_role is
// PERMISSION_DENIED.
func authenticate(ctx context.Context, v *servicetoken.Verifier, fullMethod string) (context.Context, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	auth := md.Get(MetadataAuthorization)
	if len(auth) != 1 {
		return nil, errUnauthenticated
	}
	token, ok := strings.CutPrefix(auth[0], "Bearer ")
	if !ok {
		return nil, errUnauthenticated
	}
	claims, err := v.Verify(strings.TrimSpace(token), fullMethod)
	if err != nil {
		return nil, errUnauthenticated
	}
	// An anonymous credential (no subject) opens anonymous methods only.
	if claims.Subject == "" && !Anonymous(fullMethod) {
		return nil, errUnauthenticated
	}
	// The role requirement is enforced here from the verified credential, so a
	// service does not depend on the BFF having checked it.
	if role := RequiredRole(fullMethod); role != "" && !HasRole(claims.Roles, role) {
		return nil, errPermission
	}
	call := Call{
		UserID:         claims.Subject,
		Roles:          claims.Roles,
		RequestID:      NormalizeRequestID(first(md, MetadataRequestID)),
		TripmapSchema:  strings.TrimSpace(first(md, MetadataTripmapSchema)),
		TripmapRegions: strings.TrimSpace(first(md, MetadataTripmapRegions)),
	}
	call.ClientIP = validIP(first(md, MetadataClientIP))
	if tp := first(md, MetadataTraceparent); traceparentPattern.MatchString(tp) {
		call.TraceParent = tp
		ctx = propagation.TraceContext{}.Extract(ctx, mdCarrier(metadata.Pairs(MetadataTraceparent, tp)))
	}
	return WithCall(ctx, call), nil
}

func first(md metadata.MD, key string) string {
	if v := md.Get(key); len(v) > 0 {
		return v[0]
	}
	return ""
}

// UnaryServerInterceptor authenticates every unary method; there is no way to
// opt out except the anonymous option in proto, which still needs a credential.
func UnaryServerInterceptor(v *servicetoken.Verifier) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		ctx, err := authenticate(ctx, v, info.FullMethod)
		if err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

// StreamServerInterceptor applies the same checks to streaming methods.
func StreamServerInterceptor(v *servicetoken.Verifier) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		ctx, err := authenticate(ss.Context(), v, info.FullMethod)
		if err != nil {
			return err
		}
		return next(srv, &wrappedStream{ServerStream: ss, ctx: ctx})
	}
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }
