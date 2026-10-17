package upstream

import (
	"context"

	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"github.com/kongken/mono/internal/platform/health"
)

// GRPCProbes returns readiness checks that dial each configured service over TLS
// gRPC with the same credentials used for business calls.
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
