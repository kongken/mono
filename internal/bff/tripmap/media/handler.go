package media

import (
	"context"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/upstream"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type Handler struct {
	client tripmapv1.MediaServiceClient
}

func New(client tripmapv1.MediaServiceClient) *Handler {
	return &Handler{client: client}
}

func (h *Handler) tripmap(ctx context.Context, req connect.AnyRequest) context.Context {
	return upstream.WithTripmapHeaders(ctx, req.Header())
}

func (h *Handler) PrepareUpload(ctx context.Context, req *connect.Request[bfftripmapv1.PrepareUploadRequest]) (*connect.Response[bfftripmapv1.PrepareUploadResponse], error) {
	in := req.Msg
	out, err := h.client.PrepareUpload(h.tripmap(ctx, req), &tripmapv1.PrepareUploadRequest{
		RegionCode: in.GetRegionCode(), Filename: in.GetFilename(), ContentType: in.GetContentType(),
		SizeBytes: in.GetSizeBytes(), UploadId: in.GetUploadId(), TripId: in.GetTripId(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.PrepareUploadResponse{
		Media: mapMedia(out.GetMedia()), UploadUrl: out.GetUploadUrl(),
		Headers: out.GetHeaders(), ExpiresAt: out.GetExpiresAt(),
	}), nil
}

func (h *Handler) CompleteUpload(ctx context.Context, req *connect.Request[bfftripmapv1.CompleteUploadRequest]) (*connect.Response[bfftripmapv1.CompleteUploadResponse], error) {
	out, err := h.client.CompleteUpload(h.tripmap(ctx, req), &tripmapv1.CompleteUploadRequest{Id: req.Msg.GetId()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.CompleteUploadResponse{Media: mapMedia(out.GetMedia())}), nil
}

func (h *Handler) ListMedia(ctx context.Context, req *connect.Request[bfftripmapv1.ListMediaRequest]) (*connect.Response[bfftripmapv1.ListMediaResponse], error) {
	out, err := h.client.ListMedia(h.tripmap(ctx, req), &tripmapv1.ListMediaRequest{
		RegionCode: req.Msg.GetRegionCode(), TripId: req.Msg.GetTripId(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.ListMediaResponse{Media: mapMediaList(out.GetMedia())}), nil
}

func (h *Handler) ReadMedia(ctx context.Context, req *connect.Request[bfftripmapv1.ReadMediaRequest]) (*connect.Response[bfftripmapv1.ReadMediaResponse], error) {
	out, err := h.client.ReadMedia(h.tripmap(ctx, req), &tripmapv1.ReadMediaRequest{
		Id: req.Msg.GetId(), Thumbnail: req.Msg.GetThumbnail(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	res := connect.NewResponse(&bfftripmapv1.ReadMediaResponse{
		Data: out.GetData(), ContentType: out.GetContentType(), Filename: out.GetFilename(), Url: out.GetUrl(),
	})
	res.Header().Set("Cache-Control", "no-store")
	return res, nil
}

func (h *Handler) DeleteMedia(ctx context.Context, req *connect.Request[bfftripmapv1.DeleteMediaRequest]) (*connect.Response[bfftripmapv1.DeleteMediaResponse], error) {
	_, err := h.client.DeleteMedia(h.tripmap(ctx, req), &tripmapv1.DeleteMediaRequest{Id: req.Msg.GetId()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.DeleteMediaResponse{}), nil
}
