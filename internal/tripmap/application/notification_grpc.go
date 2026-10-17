package application

import (
	"context"

	"connectrpc.com/connect"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type notificationGRPC struct {
	tripmapv1.UnimplementedNotificationServiceServer
	*NotificationService
}

// NotificationGRPC adapts NotificationService for the native gRPC server.
func NotificationGRPC(s *NotificationService) tripmapv1.NotificationServiceServer {
	return notificationGRPC{NotificationService: s}
}

func (n notificationGRPC) GetNotificationSettings(ctx context.Context, req *tripmapv1.GetNotificationSettingsRequest) (*tripmapv1.GetNotificationSettingsResponse, error) {
	res, err := n.NotificationService.GetNotificationSettings(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (n notificationGRPC) UpdateNotificationSettings(ctx context.Context, req *tripmapv1.UpdateNotificationSettingsRequest) (*tripmapv1.UpdateNotificationSettingsResponse, error) {
	res, err := n.NotificationService.UpdateNotificationSettings(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (n notificationGRPC) RequestEmailVerification(ctx context.Context, req *tripmapv1.RequestEmailVerificationRequest) (*tripmapv1.RequestEmailVerificationResponse, error) {
	res, err := n.NotificationService.RequestEmailVerification(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (n notificationGRPC) ConfirmEmail(ctx context.Context, req *tripmapv1.ConfirmEmailRequest) (*tripmapv1.ConfirmEmailResponse, error) {
	res, err := n.NotificationService.ConfirmEmail(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (n notificationGRPC) RemoveEmail(ctx context.Context, req *tripmapv1.RemoveEmailRequest) (*tripmapv1.RemoveEmailResponse, error) {
	res, err := n.NotificationService.RemoveEmail(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (n notificationGRPC) RegisterPushSubscription(ctx context.Context, req *tripmapv1.RegisterPushSubscriptionRequest) (*tripmapv1.RegisterPushSubscriptionResponse, error) {
	res, err := n.NotificationService.RegisterPushSubscription(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (n notificationGRPC) RemovePushSubscription(ctx context.Context, req *tripmapv1.RemovePushSubscriptionRequest) (*tripmapv1.RemovePushSubscriptionResponse, error) {
	res, err := n.NotificationService.RemovePushSubscription(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (n notificationGRPC) SendTestNotification(ctx context.Context, req *tripmapv1.SendTestNotificationRequest) (*tripmapv1.SendTestNotificationResponse, error) {
	res, err := n.NotificationService.SendTestNotification(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}
