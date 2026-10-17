package application

import (
	"context"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/tripmap/ent"
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

func (f footprintGRPC) GetChanges(ctx context.Context, req *tripmapv1.GetChangesRequest) (*tripmapv1.GetChangesResponse, error) {
	return connectUnary(f.FootprintService.GetChanges, f.withAccountSchema, ctx, req)
}

func (f footprintGRPC) SyncChanges(ctx context.Context, req *tripmapv1.SyncChangesRequest) (*tripmapv1.SyncChangesResponse, error) {
	return connectUnary(f.FootprintService.SyncChanges, f.withAccountSchema, ctx, req)
}

func (f footprintGRPC) UpsertVisit(ctx context.Context, req *tripmapv1.UpsertVisitRequest) (*tripmapv1.UpsertVisitResponse, error) {
	return connectUnary(f.FootprintService.UpsertVisit, f.withAccountSchema, ctx, req)
}

func (f footprintGRPC) DeleteVisit(ctx context.Context, req *tripmapv1.DeleteVisitRequest) (*tripmapv1.DeleteVisitResponse, error) {
	return connectUnary(f.FootprintService.DeleteVisit, f.withAccountSchema, ctx, req)
}

func (f footprintGRPC) UpsertProvinceMark(ctx context.Context, req *tripmapv1.UpsertProvinceMarkRequest) (*tripmapv1.UpsertProvinceMarkResponse, error) {
	return connectUnary(f.FootprintService.UpsertProvinceMark, f.withAccountSchema, ctx, req)
}

func (f footprintGRPC) DeleteProvinceMark(ctx context.Context, req *tripmapv1.DeleteProvinceMarkRequest) (*tripmapv1.DeleteProvinceMarkResponse, error) {
	return connectUnary(f.FootprintService.DeleteProvinceMark, f.withAccountSchema, ctx, req)
}

func (f footprintGRPC) withAccountSchema(ctx context.Context) (context.Context, error) {
	p, err := principal(ctx)
	if err != nil {
		return ctx, grpcError(err)
	}
	st, err := storeFrom(f.src)
	if err != nil {
		return ctx, grpcError(err)
	}
	u, err := st.DB.User.Get(ctx, p.UserID)
	if err != nil {
		if ent.IsNotFound(err) {
			return ctx, grpcError(errUnauthenticated)
		}
		return ctx, grpcError(internalError(err))
	}
	return withSchemasFromRPC(ctx, u.SchemaVersion), nil
}

func connectUnary[Req, Res any](
	fn func(context.Context, *connect.Request[Req]) (*connect.Response[Res], error),
	prepare func(context.Context) (context.Context, error),
	ctx context.Context, msg *Req,
) (*Res, error) {
	var err error
	if prepare != nil {
		ctx, err = prepare(ctx)
		if err != nil {
			return nil, err
		}
	}
	res, err := fn(ctx, connect.NewRequest(msg))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}
