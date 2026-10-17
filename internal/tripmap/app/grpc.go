package app

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/tripmap/application"
	"github.com/kongken/mono/internal/tripmap/regions"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/grpc"
)

// grpcShutdownTimeout is how long running calls get to finish on shutdown.
const grpcShutdownTimeout = 10 * time.Second

// StartGRPC starts the TLS gRPC listener the BFF calls. Every call must carry a
// credential from a trusted BFF key (rpcauth interceptors run before any
// handler). extra registers further services before serving starts; tests use it
// for probes, production services are listed here as they migrate.
func (h *Handlers) StartGRPC(extra ...func(grpc.ServiceRegistrar)) error {
	verifier, err := h.cfg.InternalAuth.NewVerifier()
	if err != nil {
		return err
	}
	srv, err := grpcx.Start(h.cfg.GRPC, func(r grpc.ServiceRegistrar) {
		tripmapv1.RegisterSystemServiceServer(r, application.NewSystemService(h, func() bool { return h.cfg.Share.Enabled() }))
		if st := h.Store(); st != nil {
			idx, _ := regions.Default()
			foot := application.NewFootprintService(h.cfg, h, idx)
			tripmapv1.RegisterFootprintServiceServer(r, application.FootprintGRPC(foot))
			tripmapv1.RegisterItineraryServiceServer(r, application.ItineraryGRPC(application.NewItineraryService(h, idx)))
			tripmapv1.RegisterProfileServiceServer(r, application.NewProfileService(h))
			notif := application.NewNotificationService(h, h.notifier)
			tripmapv1.RegisterNotificationServiceServer(r, application.NotificationGRPC(notif))
			tripmapv1.RegisterNotificationPublicServiceServer(r, application.NotificationPublicGRPC(application.NewNotificationPublic(h.cfg, h, h.notifier)))
			collab := application.NewCollaborationService(h, idx)
			tripmapv1.RegisterCollaborationServiceServer(r, application.CollaborationGRPC(collab))
			tripmapv1.RegisterAccountCleanupServiceServer(r, application.NewAccountCleanupService(h))
		}
		for _, register := range extra {
			register(r)
		}
	}, []grpc.UnaryServerInterceptor{rpcauth.UnaryServerInterceptor(verifier)}, []grpc.StreamServerInterceptor{rpcauth.StreamServerInterceptor(verifier)})
	if err != nil {
		return fmt.Errorf("start grpc: %w", err)
	}
	h.grpc.Store(srv)
	slog.Info("tripmap grpc listening", "addr", srv.Addr())
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
