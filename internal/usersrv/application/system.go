// Package application implements the usersrv RPC services over the account
// core: it maps the verified credential to the acting user, applies the rate
// limits and turns account errors into gRPC statuses.
package application

import (
	"context"

	"github.com/kongken/mono/internal/platform/health"
	usersrvv1 "github.com/kongken/mono/pkg/proto/usersrv/v1"
)

// StatusSource reports dependency readiness.
type StatusSource interface {
	Check(context.Context) []health.Status
}

// SystemService answers the BFF's readiness query over gRPC. It carries no user
// data and never probes a mail vendor.
type SystemService struct {
	usersrvv1.UnimplementedSystemServiceServer
	source StatusSource
}

func NewSystemService(source StatusSource) *SystemService { return &SystemService{source: source} }

func (s *SystemService) GetStatus(ctx context.Context, _ *usersrvv1.GetStatusRequest) (*usersrvv1.GetStatusResponse, error) {
	statuses := s.source.Check(ctx)
	out := &usersrvv1.GetStatusResponse{Ready: health.Ready(statuses)}
	for _, st := range statuses {
		out.Dependencies = append(out.Dependencies, &usersrvv1.DependencyStatus{Name: st.Name, Ready: st.Ready})
	}
	return out, nil
}
