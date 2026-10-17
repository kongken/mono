package application

import (
	"context"
	"errors"
	"strings"

	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/tripmap/accountcleanup"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AccountCleanupService is the internal gRPC adapter for business data removal.
type AccountCleanupService struct {
	tripmapv1.UnimplementedAccountCleanupServiceServer
	src StoreSource
}

func NewAccountCleanupService(src StoreSource) *AccountCleanupService {
	return &AccountCleanupService{src: src}
}

func (s *AccountCleanupService) CleanupUserData(ctx context.Context, req *tripmapv1.CleanupUserDataRequest) (*tripmapv1.CleanupUserDataResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id required")
	}
	caller, ok := rpcauth.UserIDFrom(ctx)
	if !ok || caller != userID {
		return nil, status.Error(codes.PermissionDenied, "user mismatch")
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, grpcError(err)
	}
	if err := accountcleanup.Run(ctx, &accountcleanup.Store{DB: st.DB, Blobs: st.Blobs}, userID); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Error(codes.Canceled, err.Error())
		}
		if errors.Is(err, accountcleanup.ErrObjectStorageUnavailable) {
			return nil, status.Error(codes.Unavailable, "object cleanup unavailable; retry account deletion")
		}
		return nil, status.Error(codes.Unavailable, "object cleanup incomplete; retry account deletion")
	}
	return &tripmapv1.CleanupUserDataResponse{}, nil
}
