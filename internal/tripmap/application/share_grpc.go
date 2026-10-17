package application

import (
	"context"

	"connectrpc.com/connect"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type shareGRPC struct {
	tripmapv1.UnimplementedShareServiceServer
	*ShareService
}

// ShareGRPC adapts ShareService for the native gRPC server.
func ShareGRPC(s *ShareService) tripmapv1.ShareServiceServer {
	return shareGRPC{ShareService: s}
}

func (s shareGRPC) GetShare(ctx context.Context, req *tripmapv1.GetShareRequest) (*tripmapv1.GetShareResponse, error) {
	res, err := s.ShareService.GetShare(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (s shareGRPC) PreviewShare(ctx context.Context, req *tripmapv1.PreviewShareRequest) (*tripmapv1.PreviewShareResponse, error) {
	res, err := s.ShareService.PreviewShare(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (s shareGRPC) SaveShare(ctx context.Context, req *tripmapv1.SaveShareRequest) (*tripmapv1.SaveShareResponse, error) {
	res, err := s.ShareService.SaveShare(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (s shareGRPC) RevokeShare(ctx context.Context, req *tripmapv1.RevokeShareRequest) (*tripmapv1.RevokeShareResponse, error) {
	res, err := s.ShareService.RevokeShare(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}
