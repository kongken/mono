package application

import (
	"context"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/timeline/ent"
	"github.com/kongken/mono/internal/timeline/ent/mediaobject"
	"github.com/kongken/mono/internal/timeline/media"
	timelinev1 "github.com/kongken/mono/pkg/proto/timeline/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"io"
)

type MediaService struct {
	timelinev1.UnimplementedMediaServiceServer
	store *media.Store
}

func NewMediaService(s *media.Store) *MediaService { return &MediaService{store: s} }
func info(m *ent.MediaObject) *timelinev1.MediaInfo {
	return &timelinev1.MediaInfo{MediaRef: m.PublicRef, EntryRef: m.EntryPublicRef, Filename: m.Filename, ContentType: m.ContentType, SizeBytes: m.SizeBytes, Status: m.State.String(), Revision: int64(m.Revision), CreatedAt: timestamppb.New(m.CreatedAt)}
}
func mediaInfo(m *media.Media) *timelinev1.MediaInfo {
	return &timelinev1.MediaInfo{MediaRef: m.Ref, EntryRef: m.EntryRef, Filename: m.Filename, ContentType: m.ContentType, SizeBytes: m.SizeBytes, Status: m.Status, Revision: int64(m.Revision), CreatedAt: timestamppb.New(m.CreatedAt)}
}
func (s *MediaService) PrepareUpload(ctx context.Context, req *timelinev1.PrepareUploadRequest) (*timelinev1.PrepareUploadResponse, error) {
	m, err := media.Prepare(ctx, s.store, rpcauth.CallFrom(ctx).UserID, media.Upload{EntryRef: req.EntryRef, Filename: req.Filename, ContentType: req.ContentType, SizeBytes: req.SizeBytes, MutationID: req.MutationId})
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &timelinev1.PrepareUploadResponse{Media: mediaInfo(m), UploadUrl: m.UploadURL, Headers: m.Headers, ExpiresAt: timestamppb.New(m.ExpiresAt)}, nil
}
func (s *MediaService) CompleteUpload(ctx context.Context, req *timelinev1.CompleteUploadRequest) (*timelinev1.CompleteUploadResponse, error) {
	m, err := media.Complete(ctx, s.store, rpcauth.CallFrom(ctx).UserID, req.MediaRef, req.MutationId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &timelinev1.CompleteUploadResponse{Media: mediaInfo(m)}, nil
}
func (s *MediaService) ListMedia(ctx context.Context, req *timelinev1.ListMediaRequest) (*timelinev1.ListMediaResponse, error) {
	rows, err := media.List(ctx, s.store, rpcauth.CallFrom(ctx).UserID, req.EntryRef)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	out := &timelinev1.ListMediaResponse{}
	for _, m := range rows {
		out.Media = append(out.Media, &timelinev1.MediaInfo{MediaRef: m.Ref, EntryRef: m.EntryRef, Filename: m.Filename, ContentType: m.ContentType, SizeBytes: m.SizeBytes, Status: m.Status, Revision: int64(m.Revision), CreatedAt: timestamppb.New(m.CreatedAt)})
	}
	return out, nil
}
func (s *MediaService) ReadMedia(ctx context.Context, req *timelinev1.ReadMediaRequest) (*timelinev1.ReadMediaResponse, error) {
	m, r, err := media.Read(ctx, s.store, rpcauth.CallFrom(ctx).UserID, req.MediaRef)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, m.SizeBytes+1))
	if err != nil || int64(len(b)) != m.SizeBytes {
		return nil, status.Error(codes.DataLoss, "media read failed")
	}
	return &timelinev1.ReadMediaResponse{Data: b, ContentType: m.ContentType, Filename: m.Filename}, nil
}
func (s *MediaService) DeleteMedia(ctx context.Context, req *timelinev1.DeleteMediaRequest) (*timelinev1.DeleteMediaResponse, error) {
	r, err := media.Delete(ctx, s.store, rpcauth.CallFrom(ctx).UserID, req.MediaRef, req.MutationId, int(req.ExpectedRevision))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &timelinev1.DeleteMediaResponse{Receipt: receiptResponse(r, false)}, nil
}

// GetMediaDownloadUrl issues a short-lived private address for one attachment.
// It performs the same ownership and trash checks as ReadMedia, so a deleted
// record stops issuing addresses even if the object still exists.
func (s *MediaService) GetMediaDownloadUrl(ctx context.Context, req *timelinev1.GetMediaDownloadUrlRequest) (*timelinev1.GetMediaDownloadUrlResponse, error) {
	d, err := media.DownloadURL(ctx, s.store, rpcauth.CallFrom(ctx).UserID, req.MediaRef, 0)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &timelinev1.GetMediaDownloadUrlResponse{Url: d.URL, ExpiresAt: timestamppb.New(d.ExpiresAt), ContentType: d.ContentType, Filename: d.Filename}, nil
}

var _ = info
var _ = mediaobject.StateReady
