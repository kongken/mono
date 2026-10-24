package upstream

import (
	"context"
	"errors"

	"github.com/kongken/mono/internal/platform/health"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	usersrvv1 "github.com/kongken/mono/pkg/proto/usersrv/v1"
)

// GRPCProbes returns readiness checks that call each configured service over its
// own transport (tls or h2c) with the same signed credentials as business calls.
func GRPCProbes(clients *Clients, serviceNames []string) []health.Probe {
	if clients == nil {
		return nil
	}
	probes := make([]health.Probe, 0, len(serviceNames))
	for _, name := range serviceNames {
		conn := clients.Conn(name)
		if conn == nil {
			continue
		}
		// Each product serves its own SystemService.GetStatus; a service without one
		// is only probed over HTTP /readyz.
		var check func(context.Context) error
		switch name {
		case "usersrv":
			check = func(ctx context.Context) error {
				res, err := usersrvv1.NewSystemServiceClient(conn).GetStatus(ctx, &usersrvv1.GetStatusRequest{})
				if err != nil {
					return err
				}
				if !res.GetReady() {
					return errors.New("usersrv reports it is not ready")
				}
				return nil
			}
		case "tripmap":
			check = func(ctx context.Context) error {
				_, err := tripmapv1.NewSystemServiceClient(conn).GetStatus(ctx, &tripmapv1.GetStatusRequest{})
				return err
			}
		case "metro":
			check = func(ctx context.Context) error {
				_, err := metrov1.NewSystemServiceClient(conn).GetStatus(ctx, &metrov1.GetStatusRequest{})
				return err
			}
		default:
			continue
		}
		probes = append(probes, health.Probe{Name: name + "_grpc", Check: check})
	}
	return probes
}
