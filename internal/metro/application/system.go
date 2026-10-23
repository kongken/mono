package application

import (
	"context"

	"github.com/kongken/mono/internal/platform/health"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
)

// StatusSource reports dependency readiness.
type StatusSource interface {
	Check(context.Context) []health.Status
}

// SystemService answers the BFF's readiness query over gRPC. It carries no user data.
type SystemService struct {
	metrov1.UnimplementedSystemServiceServer
	source StatusSource
}

func NewSystemService(source StatusSource) *SystemService { return &SystemService{source: source} }

func (s *SystemService) GetStatus(ctx context.Context, _ *metrov1.GetStatusRequest) (*metrov1.GetStatusResponse, error) {
	statuses := s.source.Check(ctx)
	out := &metrov1.GetStatusResponse{Ready: health.Ready(statuses)}
	for _, st := range statuses {
		out.Dependencies = append(out.Dependencies, &metrov1.DependencyStatus{Name: st.Name, Ready: st.Ready})
	}
	return out, nil
}
