// Package status implements bff.v1.StatusService: the anonymous readiness report
// the browser reads. It asks each business service over internal gRPC and adds
// nothing of its own that could leak internals.
package status

import (
	"context"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/upstream"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

// Service adapts tripmap.v1.SystemService to the browser contract.
type Service struct {
	tripmap tripmapv1.SystemServiceClient
}

func New(tripmap tripmapv1.SystemServiceClient) *Service { return &Service{tripmap: tripmap} }

func (s *Service) GetStatus(ctx context.Context, _ *connect.Request[bffv1.GetStatusRequest]) (*connect.Response[bffv1.GetStatusResponse], error) {
	res, err := s.tripmap.GetStatus(ctx, &tripmapv1.GetStatusRequest{})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	out := &bffv1.GetStatusResponse{Ready: res.Ready, FootprintSchema: res.FootprintSchema, ShareEnabled: res.ShareEnabled, RegionVersion: res.RegionVersion}
	for _, d := range res.Dependencies {
		out.Dependencies = append(out.Dependencies, &bffv1.DependencyStatus{Name: d.Name, Ready: d.Ready})
	}
	return connect.NewResponse(out), nil
}
