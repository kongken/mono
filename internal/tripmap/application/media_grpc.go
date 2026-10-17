package application

import (
	"context"

	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type mediaGRPC struct {
	tripmapv1.UnimplementedMediaServiceServer
	*MediaService
}

// MediaGRPC adapts MediaService for the native gRPC server.
func MediaGRPC(s *MediaService) tripmapv1.MediaServiceServer {
	return mediaGRPC{MediaService: s}
}

func (m mediaGRPC) PrepareUpload(ctx context.Context, req *tripmapv1.PrepareUploadRequest) (*tripmapv1.PrepareUploadResponse, error) {
	return connectUnary(m.MediaService.PrepareUpload, nil, ctx, req)
}

func (m mediaGRPC) CompleteUpload(ctx context.Context, req *tripmapv1.CompleteUploadRequest) (*tripmapv1.CompleteUploadResponse, error) {
	return connectUnary(m.MediaService.CompleteUpload, nil, ctx, req)
}

func (m mediaGRPC) ListMedia(ctx context.Context, req *tripmapv1.ListMediaRequest) (*tripmapv1.ListMediaResponse, error) {
	return connectUnary(m.MediaService.ListMedia, nil, ctx, req)
}

func (m mediaGRPC) ReadMedia(ctx context.Context, req *tripmapv1.ReadMediaRequest) (*tripmapv1.ReadMediaResponse, error) {
	return connectUnary(m.MediaService.ReadMedia, nil, ctx, req)
}

func (m mediaGRPC) DeleteMedia(ctx context.Context, req *tripmapv1.DeleteMediaRequest) (*tripmapv1.DeleteMediaResponse, error) {
	return connectUnary(m.MediaService.DeleteMedia, nil, ctx, req)
}
