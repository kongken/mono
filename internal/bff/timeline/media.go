package timeline

import (
	"connectrpc.com/connect"
	"context"
	"github.com/kongken/mono/internal/bff/upstream"
	bff "github.com/kongken/mono/pkg/proto/bff/timeline/v1"
	v1 "github.com/kongken/mono/pkg/proto/timeline/v1"
	"google.golang.org/grpc"
)

type MediaHandler struct{ client v1.MediaServiceClient }

func NewMediaHandler(c grpc.ClientConnInterface) *MediaHandler {
	return &MediaHandler{client: v1.NewMediaServiceClient(c)}
}
func (m *MediaHandler) PrepareUpload(c context.Context, r *connect.Request[bff.PrepareUploadRequest]) (*connect.Response[bff.PrepareUploadResponse], error) {
	x, e := m.client.PrepareUpload(c, &v1.PrepareUploadRequest{EntryRef: r.Msg.EntryRef, Filename: r.Msg.Filename, ContentType: r.Msg.ContentType, SizeBytes: r.Msg.SizeBytes, MutationId: r.Msg.MutationId})
	if e != nil {
		return nil, upstream.ToConnect("timeline", e)
	}
	return connect.NewResponse(&bff.PrepareUploadResponse{UploadUrl: x.UploadUrl, Headers: x.Headers, ExpiresAt: x.ExpiresAt, Media: &bff.MediaInfo{MediaRef: x.Media.MediaRef, EntryRef: x.Media.EntryRef, Filename: x.Media.Filename, ContentType: x.Media.ContentType, SizeBytes: x.Media.SizeBytes, Status: x.Media.Status}}), nil
}
func (m *MediaHandler) CompleteUpload(c context.Context, r *connect.Request[bff.CompleteUploadRequest]) (*connect.Response[bff.CompleteUploadResponse], error) {
	x, e := m.client.CompleteUpload(c, &v1.CompleteUploadRequest{MediaRef: r.Msg.MediaRef, MutationId: r.Msg.MutationId})
	if e != nil {
		return nil, upstream.ToConnect("timeline", e)
	}
	return connect.NewResponse(&bff.CompleteUploadResponse{Media: &bff.MediaInfo{MediaRef: x.Media.MediaRef, EntryRef: x.Media.EntryRef, Filename: x.Media.Filename, ContentType: x.Media.ContentType, SizeBytes: x.Media.SizeBytes, Status: x.Media.Status}}), nil
}
func (m *MediaHandler) ListMedia(c context.Context, r *connect.Request[bff.ListMediaRequest]) (*connect.Response[bff.ListMediaResponse], error) {
	x, e := m.client.ListMedia(c, &v1.ListMediaRequest{EntryRef: r.Msg.EntryRef})
	if e != nil {
		return nil, upstream.ToConnect("timeline", e)
	}
	o := &bff.ListMediaResponse{}
	for _, v := range x.Media {
		o.Media = append(o.Media, &bff.MediaInfo{MediaRef: v.MediaRef, EntryRef: v.EntryRef, Filename: v.Filename, ContentType: v.ContentType, SizeBytes: v.SizeBytes, Status: v.Status})
	}
	return connect.NewResponse(o), nil
}
func (m *MediaHandler) ReadMedia(c context.Context, r *connect.Request[bff.ReadMediaRequest]) (*connect.Response[bff.ReadMediaResponse], error) {
	x, e := m.client.ReadMedia(c, &v1.ReadMediaRequest{MediaRef: r.Msg.MediaRef})
	if e != nil {
		return nil, upstream.ToConnect("timeline", e)
	}
	return connect.NewResponse(&bff.ReadMediaResponse{Data: x.Data, ContentType: x.ContentType, Filename: x.Filename}), nil
}
func (m *MediaHandler) DeleteMedia(c context.Context, r *connect.Request[bff.DeleteMediaRequest]) (*connect.Response[bff.DeleteMediaResponse], error) {
	x, e := m.client.DeleteMedia(c, &v1.DeleteMediaRequest{MediaRef: r.Msg.MediaRef, MutationId: r.Msg.MutationId, ExpectedRevision: r.Msg.ExpectedRevision})
	if e != nil {
		return nil, upstream.ToConnect("timeline", e)
	}
	return connect.NewResponse(&bff.DeleteMediaResponse{Receipt: &bff.MutationReceipt{MutationId: x.Receipt.MutationId, Operation: x.Receipt.Operation, ResultRevision: x.Receipt.ResultRevision}}), nil
}
