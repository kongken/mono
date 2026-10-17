package media

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/bff/upstream"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type Handler struct {
	client         tripmapv1.MediaServiceClient
	allowedOrigins []string
}

func New(client tripmapv1.MediaServiceClient, allowedOrigins []string) *Handler {
	return &Handler{client: client, allowedOrigins: allowedOrigins}
}

func (h *Handler) tripmap(ctx context.Context, req connect.AnyRequest) context.Context {
	return upstream.WithTripmapHeaders(ctx, req.Header())
}

func (h *Handler) checkWrite(header connect.AnyRequest) error {
	if err := session.CheckWrite(header.Header(), h.allowedOrigins, header.Header().Get("Host")); err != nil {
		return connect.NewError(connect.CodePermissionDenied, errors.New("request rejected"))
	}
	return nil
}

func (h *Handler) PrepareUpload(ctx context.Context, req *connect.Request[bffv1.PrepareUploadRequest]) (*connect.Response[bffv1.PrepareUploadResponse], error) {
	if err := h.checkWrite(req); err != nil {
		return nil, err
	}
	in := req.Msg
	out, err := h.client.PrepareUpload(h.tripmap(ctx, req), &tripmapv1.PrepareUploadRequest{
		RegionCode: in.GetRegionCode(), Filename: in.GetFilename(), ContentType: in.GetContentType(),
		SizeBytes: in.GetSizeBytes(), UploadId: in.GetUploadId(), TripId: in.GetTripId(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.PrepareUploadResponse{
		Media: mapMedia(out.GetMedia()), UploadUrl: out.GetUploadUrl(),
		Headers: out.GetHeaders(), ExpiresAt: out.GetExpiresAt(),
	}), nil
}

func (h *Handler) CompleteUpload(ctx context.Context, req *connect.Request[bffv1.CompleteUploadRequest]) (*connect.Response[bffv1.CompleteUploadResponse], error) {
	if err := h.checkWrite(req); err != nil {
		return nil, err
	}
	out, err := h.client.CompleteUpload(h.tripmap(ctx, req), &tripmapv1.CompleteUploadRequest{Id: req.Msg.GetId()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.CompleteUploadResponse{Media: mapMedia(out.GetMedia())}), nil
}

func (h *Handler) ListMedia(ctx context.Context, req *connect.Request[bffv1.ListMediaRequest]) (*connect.Response[bffv1.ListMediaResponse], error) {
	out, err := h.client.ListMedia(h.tripmap(ctx, req), &tripmapv1.ListMediaRequest{
		RegionCode: req.Msg.GetRegionCode(), TripId: req.Msg.GetTripId(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.ListMediaResponse{Media: mapMediaList(out.GetMedia())}), nil
}

func (h *Handler) ReadMedia(ctx context.Context, req *connect.Request[bffv1.ReadMediaRequest]) (*connect.Response[bffv1.ReadMediaResponse], error) {
	out, err := h.client.ReadMedia(h.tripmap(ctx, req), &tripmapv1.ReadMediaRequest{
		Id: req.Msg.GetId(), Thumbnail: req.Msg.GetThumbnail(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.ReadMediaResponse{
		Data: out.GetData(), ContentType: out.GetContentType(), Filename: out.GetFilename(),
	}), nil
}

func (h *Handler) DeleteMedia(ctx context.Context, req *connect.Request[bffv1.DeleteMediaRequest]) (*connect.Response[bffv1.DeleteMediaResponse], error) {
	if err := h.checkWrite(req); err != nil {
		return nil, err
	}
	_, err := h.client.DeleteMedia(h.tripmap(ctx, req), &tripmapv1.DeleteMediaRequest{Id: req.Msg.GetId()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.DeleteMediaResponse{}), nil
}
