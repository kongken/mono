package application

import (
	"connectrpc.com/connect"
	"context"
	"go.orx.me/apps/trip-map/internal/health"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
)

type StatusSource interface {
	Check(context.Context) []health.Status
}
type SystemService struct {
	source StatusSource
	share  func() bool
}

// NewSystemService reports readiness; shareEnabled tells clients whether the
// server has public sharing configured.
func NewSystemService(source StatusSource, shareEnabled func() bool) *SystemService {
	return &SystemService{source: source, share: shareEnabled}
}
func (s *SystemService) GetStatus(ctx context.Context, _ *connect.Request[tripmapv1.GetStatusRequest]) (*connect.Response[tripmapv1.GetStatusResponse], error) {
	statuses := s.source.Check(ctx)
	response := &tripmapv1.GetStatusResponse{Ready: health.Ready(statuses), FootprintSchema: schemaTrips, ShareEnabled: s.share != nil && s.share()}
	for _, status := range statuses {
		response.Dependencies = append(response.Dependencies, &tripmapv1.DependencyStatus{Name: status.Name, Ready: status.Ready})
	}
	return connect.NewResponse(response), nil
}
