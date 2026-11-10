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
	return &timelinev1.MediaInfo{MediaRef: m.PublicRef, EntryRef: m.EntryPublicRef, Filename: m.Filename, ContentType: m.ContentType, SizeBytes: m.SizeBytes, Status: m.Status, CreatedAt: timestamppb.New(m.CreatedAt)}
}
func (s *MediaService) PrepareUpload(c context.Context, r *timelinev1.PrepareUploadRequest) (*timelinev1.PrepareUploadResponse, error) {
	u := rpcauth.CallFrom(c).UserID
	x, e := media.Prepare(c, s.store, u, media.Upload{EntryRef: r.EntryRef, Filename: r.Filename, ContentType: r.ContentType, SizeBytes: r.SizeBytes, MutationID: r.MutationId})
	if e != nil {
		return nil, status.Error(codes.InvalidArgument, e.Error())
	}
	return &timelinev1.PrepareUploadResponse{
		Media: &timelinev1.MediaInfo{MediaRef: x.Ref, EntryRef: x.EntryRef, Filename: x.Filename, ContentType: x.ContentType, SizeBytes: x.SizeBytes, Status: x.Status},
		// The signature covers these headers: a client that does not replay them verbatim is refused by the store.
		UploadUrl: x.UploadURL,
		Headers:   x.Headers,
		ExpiresAt: timestamppb.New(x.ExpiresAt),
	}, nil
}
func (s *MediaService) CompleteUpload(c context.Context, r *timelinev1.CompleteUploadRequest) (*timelinev1.CompleteUploadResponse, error) {
	x, e := media.Complete(c, s.store, rpcauth.CallFrom(c).UserID, r.MediaRef, r.MutationId)
	if e != nil {
		return nil, status.Error(codes.InvalidArgument, e.Error())
	}
	return &timelinev1.CompleteUploadResponse{Media: &timelinev1.MediaInfo{MediaRef: x.Ref, EntryRef: x.EntryRef, Filename: x.Filename, ContentType: x.ContentType, SizeBytes: x.SizeBytes, Status: x.Status}}, nil
}
func (s *MediaService) ListMedia(c context.Context, r *timelinev1.ListMediaRequest) (*timelinev1.ListMediaResponse, error) {
	if s.store == nil || s.store.DB == nil {
		return nil, status.Error(codes.Unavailable, "media storage unavailable")
	}
	rows, e := s.store.DB.MediaObject.Query().Where(mediaobject.UserID(rpcauth.CallFrom(c).UserID), mediaobject.EntryPublicRefEQ(r.EntryRef), mediaobject.StatusEQ("ready")).All(c)
	if e != nil {
		return nil, e
	}
	o := &timelinev1.ListMediaResponse{}
	for _, m := range rows {
		o.Media = append(o.Media, info(m))
	}
	return o, nil
}
func (s *MediaService) ReadMedia(c context.Context, r *timelinev1.ReadMediaRequest) (*timelinev1.ReadMediaResponse, error) {
	if s.store == nil || s.store.DB == nil || s.store.Blobs == nil {
		return nil, status.Error(codes.Unavailable, "media storage unavailable")
	}
	m, e := s.store.DB.MediaObject.Query().Where(mediaobject.UserID(rpcauth.CallFrom(c).UserID), mediaobject.PublicRefEQ(r.MediaRef), mediaobject.StatusEQ("ready")).Only(c)
	if e != nil {
		return nil, status.Error(codes.NotFound, "media not found")
	}
	f, e := s.store.Blobs.Get(c, m.ObjectKey)
	if e != nil {
		return nil, status.Error(codes.NotFound, "media not found")
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, m.SizeBytes+1))
	if e != nil || int64(len(b)) != m.SizeBytes {
		return nil, status.Error(codes.DataLoss, "media read failed")
	}
	return &timelinev1.ReadMediaResponse{Data: b, ContentType: m.ContentType, Filename: m.Filename}, nil
}
func (s *MediaService) DeleteMedia(c context.Context, r *timelinev1.DeleteMediaRequest) (*timelinev1.DeleteMediaResponse, error) {
	if e := media.Delete(c, s.store, rpcauth.CallFrom(c).UserID, r.MediaRef, r.MutationId, int(r.ExpectedRevision)); e != nil {
		return nil, status.Error(codes.InvalidArgument, e.Error())
	}
	return &timelinev1.DeleteMediaResponse{Receipt: &timelinev1.MutationReceipt{MutationId: r.MutationId, Operation: "media_delete", ResultRevision: 1}}, nil
}
