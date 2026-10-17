package application

import (
	"context"

	"github.com/kongken/mono/internal/platform/rpcauth"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type notificationPublicGRPC struct {
	tripmapv1.UnimplementedNotificationPublicServiceServer
	*NotificationPublic
}

// NotificationPublicGRPC serves signed email-unsubscribe actions over gRPC.
func NotificationPublicGRPC(p *NotificationPublic) tripmapv1.NotificationPublicServiceServer {
	return notificationPublicGRPC{NotificationPublic: p}
}

func (p notificationPublicGRPC) ProcessEmailUnsubscribe(ctx context.Context, req *tripmapv1.ProcessEmailUnsubscribeRequest) (*tripmapv1.ProcessEmailUnsubscribeResponse, error) {
	ip := rpcauth.CallFrom(ctx).ClientIP
	if ip == "" {
		ip = "unknown"
	}
	out, err := p.NotificationPublic.processEmailUnsubscribe(ctx, ip, req.GetToken(), req.GetConfirm())
	if err != nil {
		return nil, err
	}
	return &tripmapv1.ProcessEmailUnsubscribeResponse{Status: out.Status, ScopeLabel: out.ScopeLabel}, nil
}

func unavailableUnsubscribe() error {
	return status.Error(codes.Unavailable, "notification service temporarily unavailable")
}
