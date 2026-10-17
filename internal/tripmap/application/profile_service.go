package application

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/tripmap/ent"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ProfileService keeps business user rows aligned with BFF identity.
type ProfileService struct {
	tripmapv1.UnimplementedProfileServiceServer
	src StoreSource
}

func NewProfileService(src StoreSource) *ProfileService { return &ProfileService{src: src} }

func (s *ProfileService) EnsureProfile(ctx context.Context, req *tripmapv1.EnsureProfileRequest) (*tripmapv1.EnsureProfileResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id required")
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, grpcError(err)
	}
	existing, err := st.DB.User.Get(ctx, userID)
	if err == nil {
		up := st.DB.User.UpdateOneID(userID)
		changed := false
		if name := strings.TrimSpace(req.GetDisplayName()); name != "" && existing.DisplayName != name {
			up.SetDisplayName(name)
			changed = true
		}
		if av := strings.TrimSpace(req.GetAvatarUrl()); av != "" && existing.AvatarURL != av {
			up.SetAvatarURL(av)
			changed = true
		}
		if un := strings.TrimSpace(req.GetUsername()); un != "" && (existing.Username == nil || *existing.Username != un) {
			up.SetUsername(un)
			changed = true
		}
		if changed {
			if err := up.Exec(ctx); err != nil {
				return nil, grpcError(internalError(err))
			}
		}
		return &tripmapv1.EnsureProfileResponse{Created: false}, nil
	}
	if !ent.IsNotFound(err) {
		return nil, grpcError(internalError(err))
	}
	create := st.DB.User.Create().SetID(userID).SetDisplayName(strings.TrimSpace(req.GetDisplayName()))
	if un := strings.TrimSpace(req.GetUsername()); un != "" {
		create.SetUsername(un)
	}
	if av := strings.TrimSpace(req.GetAvatarUrl()); av != "" {
		create.SetAvatarURL(av)
	}
	if _, err = create.Save(ctx); err != nil {
		if ent.IsConstraintError(err) {
			// Another caller created the row; treat as success.
			return &tripmapv1.EnsureProfileResponse{Created: false}, nil
		}
		return nil, grpcError(internalError(err))
	}
	return &tripmapv1.EnsureProfileResponse{Created: true}, nil
}

func grpcError(err error) error {
	var ce *connect.Error
	if errors.As(err, &ce) {
		switch ce.Code() {
		case connect.CodeUnauthenticated:
			return status.Error(codes.Unauthenticated, ce.Message())
		case connect.CodeInvalidArgument:
			return status.Error(codes.InvalidArgument, ce.Message())
		case connect.CodeFailedPrecondition:
			return status.Error(codes.FailedPrecondition, ce.Message())
		case connect.CodeResourceExhausted:
			return status.Error(codes.ResourceExhausted, ce.Message())
		case connect.CodeUnavailable:
			return status.Error(codes.Unavailable, ce.Message())
		case connect.CodePermissionDenied:
			return status.Error(codes.PermissionDenied, ce.Message())
		default:
			return status.Error(codes.Internal, "internal error")
		}
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Error(codes.Internal, "internal error")
}
