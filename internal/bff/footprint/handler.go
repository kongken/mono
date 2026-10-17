package footprint

import (
	"context"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/upstream"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type Handler struct {
	client tripmapv1.FootprintServiceClient
}

func New(client tripmapv1.FootprintServiceClient) *Handler {
	return &Handler{client: client}
}

func (h *Handler) tripmap(ctx context.Context, req connect.AnyRequest) context.Context {
	return upstream.WithTripmapHeaders(ctx, req.Header())
}

func (h *Handler) GetFootprints(ctx context.Context, req *connect.Request[bffv1.GetFootprintsRequest]) (*connect.Response[bffv1.GetFootprintsResponse], error) {
	out, err := h.client.GetFootprints(h.tripmap(ctx, req), &tripmapv1.GetFootprintsRequest{})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(mapFootprints(out)), nil
}

func (h *Handler) GetChanges(ctx context.Context, req *connect.Request[bffv1.GetChangesRequest]) (*connect.Response[bffv1.GetChangesResponse], error) {
	out, err := h.client.GetChanges(h.tripmap(ctx, req), &tripmapv1.GetChangesRequest{
		SinceRevision: req.Msg.GetSinceRevision(), Limit: req.Msg.GetLimit(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(mapChanges(out)), nil
}

func (h *Handler) SyncChanges(ctx context.Context, req *connect.Request[bffv1.SyncChangesRequest]) (*connect.Response[bffv1.SyncChangesResponse], error) {
	out, err := h.client.SyncChanges(h.tripmap(ctx, req), &tripmapv1.SyncChangesRequest{
		MutationId: req.Msg.GetMutationId(), BaseRevision: req.Msg.GetBaseRevision(), Mutations: toMutations(req.Msg.GetMutations()),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.SyncChangesResponse{
		Revision: out.GetRevision(), ChangedCount: out.GetChangedCount(), Replayed: out.GetReplayed(),
	}), nil
}

func (h *Handler) UpsertVisit(ctx context.Context, req *connect.Request[bffv1.UpsertVisitRequest]) (*connect.Response[bffv1.UpsertVisitResponse], error) {
	out, err := h.client.UpsertVisit(h.tripmap(ctx, req), &tripmapv1.UpsertVisitRequest{
		Visit: toVisit(req.Msg.GetVisit()), BaseRevision: req.Msg.GetBaseRevision(), RequireRevision: req.Msg.GetRequireRevision(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.UpsertVisitResponse{
		Visit: mapVisit(out.GetVisit()), Revision: out.GetRevision(), Changed: out.GetChanged(),
	}), nil
}

func (h *Handler) DeleteVisit(ctx context.Context, req *connect.Request[bffv1.DeleteVisitRequest]) (*connect.Response[bffv1.DeleteVisitResponse], error) {
	out, err := h.client.DeleteVisit(h.tripmap(ctx, req), &tripmapv1.DeleteVisitRequest{
		Code: req.Msg.GetCode(), BaseRevision: req.Msg.GetBaseRevision(), RequireRevision: req.Msg.GetRequireRevision(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.DeleteVisitResponse{Revision: out.GetRevision(), Deleted: out.GetDeleted()}), nil
}

func (h *Handler) UpsertProvinceMark(ctx context.Context, req *connect.Request[bffv1.UpsertProvinceMarkRequest]) (*connect.Response[bffv1.UpsertProvinceMarkResponse], error) {
	out, err := h.client.UpsertProvinceMark(h.tripmap(ctx, req), &tripmapv1.UpsertProvinceMarkRequest{
		Mark: toMark(req.Msg.GetMark()), BaseRevision: req.Msg.GetBaseRevision(), RequireRevision: req.Msg.GetRequireRevision(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.UpsertProvinceMarkResponse{
		Mark: mapMark(out.GetMark()), Revision: out.GetRevision(), Changed: out.GetChanged(),
	}), nil
}

func (h *Handler) DeleteProvinceMark(ctx context.Context, req *connect.Request[bffv1.DeleteProvinceMarkRequest]) (*connect.Response[bffv1.DeleteProvinceMarkResponse], error) {
	out, err := h.client.DeleteProvinceMark(h.tripmap(ctx, req), &tripmapv1.DeleteProvinceMarkRequest{
		Province: req.Msg.GetProvince(), BaseRevision: req.Msg.GetBaseRevision(), RequireRevision: req.Msg.GetRequireRevision(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.DeleteProvinceMarkResponse{Revision: out.GetRevision(), Deleted: out.GetDeleted()}), nil
}
