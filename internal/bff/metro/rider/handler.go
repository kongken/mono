// Package admin adapts the browser-facing bff.metro.v1.RiderService to the
// internal metro.v1.RiderService; see package relay for how calls are carried.
package rider

import (
	"context"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/metro/relay"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/protobuf/proto"
)

type Handler struct{ client metrov1.RiderServiceClient }

func New(client metrov1.RiderServiceClient) *Handler { return &Handler{client: client} }

func (h *Handler) GetPublicProfile(ctx context.Context, req *connect.Request[bffmetrov1.GetPublicProfileRequest]) (*connect.Response[bffmetrov1.GetPublicProfileResponse], error) {
	in, res := &metrov1.GetPublicProfileRequest{}, &bffmetrov1.GetPublicProfileResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.GetPublicProfile(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) GetMyProfile(ctx context.Context, req *connect.Request[bffmetrov1.GetMyProfileRequest]) (*connect.Response[bffmetrov1.GetMyProfileResponse], error) {
	in, res := &metrov1.GetMyProfileRequest{}, &bffmetrov1.GetMyProfileResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.GetMyProfile(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) UpdateMyProfile(ctx context.Context, req *connect.Request[bffmetrov1.UpdateMyProfileRequest]) (*connect.Response[bffmetrov1.UpdateMyProfileResponse], error) {
	in, res := &metrov1.UpdateMyProfileRequest{}, &bffmetrov1.UpdateMyProfileResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.UpdateMyProfile(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) ListMyLines(ctx context.Context, req *connect.Request[bffmetrov1.ListMyLinesRequest]) (*connect.Response[bffmetrov1.ListMyLinesResponse], error) {
	in, res := &metrov1.ListMyLinesRequest{}, &bffmetrov1.ListMyLinesResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.ListMyLines(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) MarkLine(ctx context.Context, req *connect.Request[bffmetrov1.MarkLineRequest]) (*connect.Response[bffmetrov1.MarkLineResponse], error) {
	in, res := &metrov1.MarkLineRequest{}, &bffmetrov1.MarkLineResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.MarkLine(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) UnmarkLine(ctx context.Context, req *connect.Request[bffmetrov1.UnmarkLineRequest]) (*connect.Response[bffmetrov1.UnmarkLineResponse], error) {
	in, res := &metrov1.UnmarkLineRequest{}, &bffmetrov1.UnmarkLineResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.UnmarkLine(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}
