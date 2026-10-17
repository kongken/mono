package notification

import (
	"context"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/upstream"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type Handler struct {
	notify tripmapv1.NotificationServiceClient
}

func New(notify tripmapv1.NotificationServiceClient) *Handler {
	return &Handler{notify: notify}
}

func (h *Handler) GetNotificationSettings(ctx context.Context, req *connect.Request[bffv1.GetNotificationSettingsRequest]) (*connect.Response[bffv1.GetNotificationSettingsResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.notify.GetNotificationSettings(ctx, &tripmapv1.GetNotificationSettingsRequest{})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.GetNotificationSettingsResponse{
		Settings:        mapSettings(out.GetSettings()),
		EmailAvailable:  out.GetEmailAvailable(),
		PushAvailable:   out.GetPushAvailable(),
		VapidPublicKey:  out.GetVapidPublicKey(),
	}), nil
}

func (h *Handler) UpdateNotificationSettings(ctx context.Context, req *connect.Request[bffv1.UpdateNotificationSettingsRequest]) (*connect.Response[bffv1.UpdateNotificationSettingsResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	in := req.Msg
	out, err := h.notify.UpdateNotificationSettings(ctx, &tripmapv1.UpdateNotificationSettingsRequest{
		Preferences: mapPrefsIn(in.GetPreferences()),
		Timezone:    in.GetTimezone(),
		SendHour:    in.GetSendHour(),
		ShowPlaces:  in.GetShowPlaces(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.UpdateNotificationSettingsResponse{Settings: mapSettings(out.GetSettings())}), nil
}

func (h *Handler) RequestEmailVerification(ctx context.Context, req *connect.Request[bffv1.RequestEmailVerificationRequest]) (*connect.Response[bffv1.RequestEmailVerificationResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.notify.RequestEmailVerification(ctx, &tripmapv1.RequestEmailVerificationRequest{Email: req.Msg.GetEmail()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.RequestEmailVerificationResponse{
		PendingEmailMasked: out.GetPendingEmailMasked(),
		ExpiresAt:          out.GetExpiresAt(),
	}), nil
}

func (h *Handler) ConfirmEmail(ctx context.Context, req *connect.Request[bffv1.ConfirmEmailRequest]) (*connect.Response[bffv1.ConfirmEmailResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.notify.ConfirmEmail(ctx, &tripmapv1.ConfirmEmailRequest{Code: req.Msg.GetCode()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.ConfirmEmailResponse{Settings: mapSettings(out.GetSettings())}), nil
}

func (h *Handler) RemoveEmail(ctx context.Context, req *connect.Request[bffv1.RemoveEmailRequest]) (*connect.Response[bffv1.RemoveEmailResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.notify.RemoveEmail(ctx, &tripmapv1.RemoveEmailRequest{})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.RemoveEmailResponse{Settings: mapSettings(out.GetSettings())}), nil
}

func (h *Handler) RegisterPushSubscription(ctx context.Context, req *connect.Request[bffv1.RegisterPushSubscriptionRequest]) (*connect.Response[bffv1.RegisterPushSubscriptionResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	in := req.Msg
	out, err := h.notify.RegisterPushSubscription(ctx, &tripmapv1.RegisterPushSubscriptionRequest{
		Endpoint: in.GetEndpoint(), P256Dh: in.GetP256Dh(), Auth: in.GetAuth(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.RegisterPushSubscriptionResponse{PushSubscriptionCount: out.GetPushSubscriptionCount()}), nil
}

func (h *Handler) RemovePushSubscription(ctx context.Context, req *connect.Request[bffv1.RemovePushSubscriptionRequest]) (*connect.Response[bffv1.RemovePushSubscriptionResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.notify.RemovePushSubscription(ctx, &tripmapv1.RemovePushSubscriptionRequest{Endpoint: req.Msg.GetEndpoint()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.RemovePushSubscriptionResponse{PushSubscriptionCount: out.GetPushSubscriptionCount()}), nil
}

func (h *Handler) SendTestNotification(ctx context.Context, req *connect.Request[bffv1.SendTestNotificationRequest]) (*connect.Response[bffv1.SendTestNotificationResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.notify.SendTestNotification(ctx, &tripmapv1.SendTestNotificationRequest{Channel: req.Msg.GetChannel()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.SendTestNotificationResponse{Delivered: out.GetDelivered()}), nil
}

func mapPrefsIn(in []*bffv1.NotificationPreference) []*tripmapv1.NotificationPreference {
	out := make([]*tripmapv1.NotificationPreference, 0, len(in))
	for _, p := range in {
		out = append(out, &tripmapv1.NotificationPreference{Kind: p.GetKind(), Channel: p.GetChannel(), Enabled: p.GetEnabled()})
	}
	return out
}

func mapSettings(in *tripmapv1.NotificationSettings) *bffv1.NotificationSettings {
	if in == nil {
		return nil
	}
	out := &bffv1.NotificationSettings{
		Timezone: in.GetTimezone(), SendHour: in.GetSendHour(), ShowPlaces: in.GetShowPlaces(),
		EmailMasked: in.GetEmailMasked(), EmailVerified: in.GetEmailVerified(),
		PendingEmailMasked: in.GetPendingEmailMasked(), PendingExpiresAt: in.GetPendingExpiresAt(),
		PushSubscriptionCount: in.GetPushSubscriptionCount(),
	}
	for _, p := range in.GetPreferences() {
		out.Preferences = append(out.Preferences, &bffv1.NotificationPreference{
			Kind: p.GetKind(), Channel: p.GetChannel(), Enabled: p.GetEnabled(),
		})
	}
	return out
}
