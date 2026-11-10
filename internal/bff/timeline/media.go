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

// info copies an attachment one field at a time: the two contracts are generated from different proto packages on
// purpose, so a field added on one side has to be added here too — the compiler is what catches that.
func info(m *v1.MediaInfo) *bff.MediaInfo {
	if m == nil {
		return nil
	}
	return &bff.MediaInfo{
		MediaRef:    m.MediaRef,
		EntryRef:    m.EntryRef,
		Filename:    m.Filename,
		ContentType: m.ContentType,
		SizeBytes:   m.SizeBytes,
		Status:      m.Status,
		CreatedAt:   m.CreatedAt,
	}
}

func (m *MediaHandler) PrepareUpload(c context.Context, r *connect.Request[bff.PrepareUploadRequest]) (*connect.Response[bff.PrepareUploadResponse], error) {
	x, e := m.client.PrepareUpload(c, &v1.PrepareUploadRequest{EntryRef: r.Msg.EntryRef, Filename: r.Msg.Filename, ContentType: r.Msg.ContentType, SizeBytes: r.Msg.SizeBytes, MutationId: r.Msg.MutationId})
	if e != nil {
		return nil, upstream.ToConnect("timeline", e)
	}
	// Headers and expiry travel with the URL: the presigned PUT signs Content-Type, and a client that guesses its
	// own value is refused by the object store.
	return connect.NewResponse(&bff.PrepareUploadResponse{Media: info(x.Media), UploadUrl: x.UploadUrl, Headers: x.Headers, ExpiresAt: x.ExpiresAt}), nil
}

func (m *MediaHandler) CompleteUpload(c context.Context, r *connect.Request[bff.CompleteUploadRequest]) (*connect.Response[bff.CompleteUploadResponse], error) {
	x, e := m.client.CompleteUpload(c, &v1.CompleteUploadRequest{MediaRef: r.Msg.MediaRef, MutationId: r.Msg.MutationId})
	if e != nil {
		return nil, upstream.ToConnect("timeline", e)
	}
	return connect.NewResponse(&bff.CompleteUploadResponse{Media: info(x.Media)}), nil
}

func (m *MediaHandler) ListMedia(c context.Context, r *connect.Request[bff.ListMediaRequest]) (*connect.Response[bff.ListMediaResponse], error) {
	x, e := m.client.ListMedia(c, &v1.ListMediaRequest{EntryRef: r.Msg.EntryRef})
	if e != nil {
		return nil, upstream.ToConnect("timeline", e)
	}
	o := &bff.ListMediaResponse{}
	for _, v := range x.Media {
		o.Media = append(o.Media, info(v))
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
