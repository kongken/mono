package rpcauth

import (
	"context"

	"github.com/kongken/mono/internal/platform/servicetoken"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// UnaryClientInterceptor signs one credential per call for audience and sends the
// fixed metadata set built from the Call in ctx. The outgoing metadata is
// rebuilt from scratch, so nothing a caller put in ctx reaches the service.
// Anonymous calls (empty UserID) are signed too.
func UnaryClientInterceptor(signer *servicetoken.Signer, audience string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		call := CallFrom(ctx)
		token, err := signer.Sign(audience, method, call.UserID)
		if err != nil {
			return status.Error(codes.Internal, "sign service credential")
		}
		md := metadata.Pairs(MetadataAuthorization, "Bearer "+token, MetadataRequestID, NormalizeRequestID(call.RequestID))
		if ip := validIP(call.ClientIP); ip != "" {
			md.Set(MetadataClientIP, ip)
		}
		if call.TripmapSchema != "" {
			md.Set(MetadataTripmapSchema, call.TripmapSchema)
		}
		if call.TripmapRegions != "" {
			md.Set(MetadataTripmapRegions, call.TripmapRegions)
		}
		propagation.TraceContext{}.Inject(ctx, mdCarrier(md))
		if len(md.Get(MetadataTraceparent)) == 0 {
			md.Set(MetadataTraceparent, newTraceparent())
		}
		return invoker(metadata.NewOutgoingContext(ctx, md), method, req, reply, cc, opts...)
	}
}
