package share

import (
	"context"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/upstream"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	client tripmapv1.ShareServiceClient
}

func New(client tripmapv1.ShareServiceClient) *Handler {
	return &Handler{client: client}
}

func (h *Handler) tripmap(ctx context.Context, req connect.AnyRequest) context.Context {
	return upstream.WithTripmapHeaders(ctx, req.Header())
}

func (h *Handler) GetShare(ctx context.Context, req *connect.Request[bfftripmapv1.GetShareRequest]) (*connect.Response[bfftripmapv1.GetShareResponse], error) {
	out, err := h.client.GetShare(h.tripmap(ctx, req), &tripmapv1.GetShareRequest{})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.GetShareResponse{Share: mapShare(out.GetShare()), Enabled: out.GetEnabled()}), nil
}

func (h *Handler) PreviewShare(ctx context.Context, req *connect.Request[bfftripmapv1.PreviewShareRequest]) (*connect.Response[bfftripmapv1.PreviewShareResponse], error) {
	out, err := h.client.PreviewShare(h.tripmap(ctx, req), &tripmapv1.PreviewShareRequest{Settings: toSettings(req.Msg.GetSettings())})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	res := &bfftripmapv1.PreviewShareResponse{
		ContentJson:       out.GetContentJson(),
		FootprintRevision: out.GetFootprintRevision(),
	}
	for _, ph := range out.GetPhotos() {
		res.Photos = append(res.Photos, &bfftripmapv1.PreviewPhoto{Handle: ph.GetHandle(), MediaId: ph.GetMediaId()})
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) SaveShare(ctx context.Context, req *connect.Request[bfftripmapv1.SaveShareRequest]) (*connect.Response[bfftripmapv1.SaveShareResponse], error) {
	in := req.Msg
	out, err := h.client.SaveShare(h.tripmap(ctx, req), &tripmapv1.SaveShareRequest{
		MutationId: in.GetMutationId(), ExpectedVersion: in.GetExpectedVersion(), Settings: toSettings(in.GetSettings()),
		RefreshContent: in.GetRefreshContent(), PreviewRevision: in.GetPreviewRevision(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.SaveShareResponse{Share: mapShare(out.GetShare()), Replayed: out.GetReplayed()}), nil
}

func (h *Handler) RevokeShare(ctx context.Context, req *connect.Request[bfftripmapv1.RevokeShareRequest]) (*connect.Response[bfftripmapv1.RevokeShareResponse], error) {
	out, err := h.client.RevokeShare(h.tripmap(ctx, req), &tripmapv1.RevokeShareRequest{MutationId: req.Msg.GetMutationId()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.RevokeShareResponse{Share: mapShare(out.GetShare()), Replayed: out.GetReplayed()}), nil
}

func toSettings(in *bfftripmapv1.ShareSettings) *tripmapv1.ShareSettings {
	if in == nil {
		return &tripmapv1.ShareSettings{}
	}
	return &tripmapv1.ShareSettings{
		Visibility: in.GetVisibility(), ShowCities: in.GetShowCities(), ShowDates: in.GetShowDates(),
		ShowNotes: in.GetShowNotes(), ShowPhotos: in.GetShowPhotos(), PhotoIds: in.GetPhotoIds(),
	}
}

func mapShare(in *tripmapv1.ShareInfo) *bfftripmapv1.ShareInfo {
	if in == nil {
		return &bfftripmapv1.ShareInfo{Settings: &bfftripmapv1.ShareSettings{}}
	}
	out := &bfftripmapv1.ShareInfo{
		Settings: &bfftripmapv1.ShareSettings{
			Visibility: in.GetSettings().GetVisibility(), ShowCities: in.GetSettings().GetShowCities(),
			ShowDates: in.GetSettings().GetShowDates(), ShowNotes: in.GetSettings().GetShowNotes(),
			ShowPhotos: in.GetSettings().GetShowPhotos(), PhotoIds: in.GetSettings().GetPhotoIds(),
		},
		Version: in.GetVersion(), Url: in.GetUrl(), Published: in.GetPublished(),
		PublishedRevision: in.GetPublishedRevision(), CurrentRevision: in.GetCurrentRevision(),
	}
	if t := in.GetPublishedAt(); t != nil {
		out.PublishedAt = timestamppb.New(t.AsTime())
	}
	return out
}
