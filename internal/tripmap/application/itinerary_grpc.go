package application

import (
	"context"

	"connectrpc.com/connect"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type itineraryGRPC struct {
	tripmapv1.UnimplementedItineraryServiceServer
	*ItineraryService
}

// ItineraryGRPC adapts ItineraryService for the native gRPC server.
func ItineraryGRPC(s *ItineraryService) tripmapv1.ItineraryServiceServer {
	return itineraryGRPC{ItineraryService: s}
}

func (i itineraryGRPC) ListItineraries(ctx context.Context, req *tripmapv1.ListItinerariesRequest) (*tripmapv1.ListItinerariesResponse, error) {
	res, err := i.ItineraryService.ListItineraries(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (i itineraryGRPC) GetItinerary(ctx context.Context, req *tripmapv1.GetItineraryRequest) (*tripmapv1.GetItineraryResponse, error) {
	res, err := i.ItineraryService.GetItinerary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (i itineraryGRPC) SaveItinerary(ctx context.Context, req *tripmapv1.SaveItineraryRequest) (*tripmapv1.SaveItineraryResponse, error) {
	res, err := i.ItineraryService.SaveItinerary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (i itineraryGRPC) DeleteItinerary(ctx context.Context, req *tripmapv1.DeleteItineraryRequest) (*tripmapv1.DeleteItineraryResponse, error) {
	res, err := i.ItineraryService.DeleteItinerary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}
