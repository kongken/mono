package upstream

import (
	"context"

	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"github.com/kongken/mono/internal/platform/health"
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
		probeName := name + "_grpc"
		probes = append(probes, health.Probe{
			Name: probeName,
			Check: func(ctx context.Context) error {
				_, err := tripmapv1.NewSystemServiceClient(conn).GetStatus(ctx, &tripmapv1.GetStatusRequest{})
				return err
			},
		})
	}
	return probes
}
