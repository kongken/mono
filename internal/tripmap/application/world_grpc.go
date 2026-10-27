package application

import (
	"context"

	"connectrpc.com/connect"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type worldGRPC struct {
	tripmapv1.UnimplementedWorldServiceServer
	*WorldService
}

// WorldGRPC adapts WorldService for the native gRPC server.
func WorldGRPC(s *WorldService) tripmapv1.WorldServiceServer {
	return worldGRPC{WorldService: s}
}

func (w worldGRPC) GetWorld(ctx context.Context, req *tripmapv1.GetWorldRequest) (*tripmapv1.GetWorldResponse, error) {
	res, err := w.WorldService.GetWorld(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (w worldGRPC) ReplaceWorld(ctx context.Context, req *tripmapv1.ReplaceWorldRequest) (*tripmapv1.ReplaceWorldResponse, error) {
	res, err := w.WorldService.ReplaceWorld(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}
