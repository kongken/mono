package app

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/kongken/mono/internal/metro/application"
	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/rpcauth"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/grpc"
)

// grpcShutdownTimeout is how long running calls get to finish on shutdown.
const grpcShutdownTimeout = 10 * time.Second

// StartGRPC starts the gRPC listener the BFF calls (TLS unless grpc.transport is
// h2c). Every call must carry a credential from a trusted BFF key: the rpcauth
// interceptors run before any handler and enforce the anonymous and
// required_role options declared in proto.
func (h *Handlers) StartGRPC(extra ...func(grpc.ServiceRegistrar)) error {
	verifier, err := h.cfg.InternalAuth.NewVerifier()
	if err != nil {
		return err
	}
	unary := []grpc.UnaryServerInterceptor{rpcauth.UnaryServerInterceptor(verifier)}
	unary = append(unary, h.grpcUnaryExtra...)
	srv, err := grpcx.Start(h.cfg.GRPC, func(r grpc.ServiceRegistrar) {
		metrov1.RegisterSystemServiceServer(r, application.NewSystemService(h))
		if res := h.resources.Load(); res != nil && res.DB != nil {
			metrov1.RegisterCatalogServiceServer(r, application.NewCatalogService(res.DB))
			metrov1.RegisterAdminServiceServer(r, application.NewAdminService(res.DB))
		}
		for _, register := range extra {
			register(r)
		}
	}, unary, []grpc.StreamServerInterceptor{rpcauth.StreamServerInterceptor(verifier)})
	if err != nil {
		return fmt.Errorf("start grpc: %w", err)
	}
	h.grpc.Store(srv)
	slog.Info("metro grpc listening", "addr", srv.Addr(), "transport", srv.Transport())
	return nil
}

// GRPCAddr is the address the gRPC server listens on, or "" when not started.
func (h *Handlers) GRPCAddr() string {
	if srv := h.grpc.Load(); srv != nil {
		return srv.Addr()
	}
	return ""
}

// StopGRPC drains and stops the gRPC server; it is safe to call more than once.
func (h *Handlers) StopGRPC() {
	if srv := h.grpc.Swap(nil); srv != nil {
		srv.Shutdown(grpcShutdownTimeout)
	}
}
