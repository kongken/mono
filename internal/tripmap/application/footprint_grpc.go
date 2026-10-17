package application

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type footprintGRPC struct {
	tripmapv1.UnimplementedFootprintServiceServer
	*FootprintService
}

// FootprintGRPC adapts FootprintService for the native gRPC server.
func FootprintGRPC(s *FootprintService) tripmapv1.FootprintServiceServer {
	return footprintGRPC{FootprintService: s}
}

func (f footprintGRPC) GetFootprints(ctx context.Context, req *tripmapv1.GetFootprintsRequest) (*tripmapv1.GetFootprintsResponse, error) {
	return f.FootprintService.GetFootprintsGRPC(ctx, req)
}

func (f footprintGRPC) GetChanges(context.Context, *tripmapv1.GetChangesRequest) (*tripmapv1.GetChangesResponse, error) {
	return nil, grpcError(connectUnimplemented())
}
func (f footprintGRPC) SyncChanges(context.Context, *tripmapv1.SyncChangesRequest) (*tripmapv1.SyncChangesResponse, error) {
	return nil, grpcError(connectUnimplemented())
}
func (f footprintGRPC) UpsertVisit(context.Context, *tripmapv1.UpsertVisitRequest) (*tripmapv1.UpsertVisitResponse, error) {
	return nil, grpcError(connectUnimplemented())
}
func (f footprintGRPC) DeleteVisit(context.Context, *tripmapv1.DeleteVisitRequest) (*tripmapv1.DeleteVisitResponse, error) {
	return nil, grpcError(connectUnimplemented())
}
func (f footprintGRPC) UpsertProvinceMark(context.Context, *tripmapv1.UpsertProvinceMarkRequest) (*tripmapv1.UpsertProvinceMarkResponse, error) {
	return nil, grpcError(connectUnimplemented())
}
func (f footprintGRPC) DeleteProvinceMark(context.Context, *tripmapv1.DeleteProvinceMarkRequest) (*tripmapv1.DeleteProvinceMarkResponse, error) {
	return nil, grpcError(connectUnimplemented())
}

func connectUnimplemented() error {
	return connect.NewError(connect.CodeUnimplemented, errUnimplementedRPC)
}

var errUnimplementedRPC = errors.New("use connect proxy until migrated")
