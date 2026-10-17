package application

import (
	"context"

	"connectrpc.com/connect"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type searchGRPC struct {
	tripmapv1.UnimplementedSearchServiceServer
	*SearchService
}

// SearchGRPC adapts SearchService for the native gRPC server.
func SearchGRPC(s *SearchService) tripmapv1.SearchServiceServer {
	return searchGRPC{SearchService: s}
}

func (s searchGRPC) SearchNotes(ctx context.Context, req *tripmapv1.SearchNotesRequest) (*tripmapv1.SearchNotesResponse, error) {
	res, err := s.SearchService.SearchNotes(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}
