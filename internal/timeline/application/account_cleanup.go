package application

import (
	"context"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/timeline/accountcleanup"
	timelinev1 "github.com/kongken/mono/pkg/proto/timeline/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strings"
)

type AccountCleanupService struct {
	timelinev1.UnimplementedAccountCleanupServiceServer
	store *accountcleanup.Store
}

func NewAccountCleanupService(store *accountcleanup.Store) *AccountCleanupService {
	return &AccountCleanupService{store: store}
}
func (s *AccountCleanupService) CleanupUserData(ctx context.Context, req *timelinev1.CleanupUserDataRequest) (*timelinev1.CleanupUserDataResponse, error) {
	userID, ok := rpcauth.UserIDFrom(ctx)
	if !ok || userID == "" {
		return nil, status.Error(codes.Unauthenticated, "sign in required")
	}
	if other := strings.TrimSpace(req.GetUserId()); other != "" && other != userID {
		return nil, status.Error(codes.PermissionDenied, "user mismatch")
	}
	if err := accountcleanup.Run(ctx, s.store, userID); err != nil {
		return nil, status.Error(codes.Unavailable, "timeline cleanup incomplete; retry account deletion")
	}
	return &timelinev1.CleanupUserDataResponse{}, nil
}
