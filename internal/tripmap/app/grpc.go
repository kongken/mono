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

// StartGRPC starts the gRPC listener the BFF calls (TLS unless grpc.transport is
// h2c). Every call, in either transport, must carry a
// credential from a trusted BFF key (rpcauth interceptors run before any
// handler). extra registers further services before serving starts; tests use it
// for probes, production services are listed here as they migrate.
func (h *Handlers) StartGRPC(extra ...func(grpc.ServiceRegistrar)) error {
	verifier, err := h.cfg.InternalAuth.NewVerifier()
	if err != nil {
		return err
	}
	unary := []grpc.UnaryServerInterceptor{rpcauth.UnaryServerInterceptor(verifier)}
	unary = append(unary, h.grpcUnaryExtra...)
	srv, err := grpcx.Start(h.cfg.GRPC, func(r grpc.ServiceRegistrar) {
		tripmapv1.RegisterSystemServiceServer(r, application.NewSystemService(h, func() bool { return h.cfg.Share.Enabled() }))
		if st := h.Store(); st != nil {
			idx, _ := regions.Default()
			foot := application.NewFootprintService(h.cfg, h, idx)
			tripmapv1.RegisterFootprintServiceServer(r, application.FootprintGRPC(foot))
			tripmapv1.RegisterItineraryServiceServer(r, application.ItineraryGRPC(application.NewItineraryService(h, idx)))
			if worldSvc, werr := application.NewWorldService(h); werr != nil {
				slog.Error("world service disabled", "error", werr)
			} else {
				tripmapv1.RegisterWorldServiceServer(r, application.WorldGRPC(worldSvc))
			}
			tripmapv1.RegisterProfileServiceServer(r, application.NewProfileService(h))
			notif := application.NewNotificationService(h, h.notifier)
			tripmapv1.RegisterNotificationServiceServer(r, application.NotificationGRPC(notif))
			tripmapv1.RegisterNotificationPublicServiceServer(r, application.NotificationPublicGRPC(application.NewNotificationPublic(h.cfg, h, h.notifier)))
			shareSvc := application.NewShareService(h.cfg, h, idx)
			tripmapv1.RegisterShareServiceServer(r, application.ShareGRPC(shareSvc))
			tripmapv1.RegisterSharePublicServiceServer(r, application.SharePublicGRPC(application.NewSharePublic(h.cfg, h)))
			collab := application.NewCollaborationService(h, idx)
			tripmapv1.RegisterCollaborationServiceServer(r, application.CollaborationGRPC(collab))
			tripmapv1.RegisterMediaServiceServer(r, application.MediaGRPC(application.NewMediaService(h, idx)))
			tripmapv1.RegisterRegionServiceServer(r, application.RegionGRPC(application.NewRegionService(h, idx)))
			tripmapv1.RegisterAccountCleanupServiceServer(r, application.NewAccountCleanupService(h))
			tripmapv1.RegisterSnapshotServiceServer(r, application.SnapshotGRPC(application.NewSnapshotService(h.cfg, h, idx)))
			report := application.NewReportService(h, idx)
			tripmapv1.RegisterReportServiceServer(r, application.ReportGRPC(report))
			tripmapv1.RegisterSearchServiceServer(r, application.SearchGRPC(application.NewSearchService(h, idx)))
		}
		for _, register := range extra {
			register(r)
		}
	}, unary, []grpc.StreamServerInterceptor{rpcauth.StreamServerInterceptor(verifier)})
	if err != nil {
		return fmt.Errorf("start grpc: %w", err)
	}
	h.grpc.Store(srv)
	slog.Info("tripmap grpc listening", "addr", srv.Addr(), "transport", srv.Transport())
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
