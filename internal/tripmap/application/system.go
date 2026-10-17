package application

import (
	"context"

	"github.com/kongken/mono/internal/platform/health"
	"github.com/kongken/mono/internal/tripmap/regions"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type StatusSource interface {
	Check(context.Context) []health.Status
}

// SystemService answers the BFF's readiness query over gRPC. It carries no user
// data and is the first service migrated to the internal transport.
type SystemService struct {
	tripmapv1.UnimplementedSystemServiceServer
	source StatusSource
	share  func() bool
}

// NewSystemService reports readiness; shareEnabled tells clients whether the
// server has public sharing configured.
func NewSystemService(source StatusSource, shareEnabled func() bool) *SystemService {
	return &SystemService{source: source, share: shareEnabled}
}

func (s *SystemService) GetStatus(ctx context.Context, _ *tripmapv1.GetStatusRequest) (*tripmapv1.GetStatusResponse, error) {
	statuses := s.source.Check(ctx)
	response := &tripmapv1.GetStatusResponse{Ready: health.Ready(statuses), FootprintSchema: schemaTrips, ShareEnabled: s.share != nil && s.share(), RegionVersion: regions.CurrentVersion()}
	for _, status := range statuses {
		response.Dependencies = append(response.Dependencies, &tripmapv1.DependencyStatus{Name: status.Name, Ready: status.Ready})
	}
	return response, nil
}
