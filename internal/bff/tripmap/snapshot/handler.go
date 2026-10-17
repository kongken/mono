package snapshot

import (
	"context"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/upstream"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type Handler struct {
	client tripmapv1.SnapshotServiceClient
}

func New(client tripmapv1.SnapshotServiceClient) *Handler {
	return &Handler{client: client}
}

func (h *Handler) tripmap(ctx context.Context, req connect.AnyRequest) context.Context {
	return upstream.WithTripmapHeaders(ctx, req.Header())
}

func (h *Handler) ListSnapshots(ctx context.Context, req *connect.Request[bfftripmapv1.ListSnapshotsRequest]) (*connect.Response[bfftripmapv1.ListSnapshotsResponse], error) {
	out, err := h.client.ListSnapshots(h.tripmap(ctx, req), &tripmapv1.ListSnapshotsRequest{})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	resp := &bfftripmapv1.ListSnapshotsResponse{}
	for _, s := range out.GetSnapshots() {
		resp.Snapshots = append(resp.Snapshots, mapInfo(s))
	}
	return connect.NewResponse(resp), nil
}

func (h *Handler) CreateSnapshot(ctx context.Context, req *connect.Request[bfftripmapv1.CreateSnapshotRequest]) (*connect.Response[bfftripmapv1.CreateSnapshotResponse], error) {
	out, err := h.client.CreateSnapshot(h.tripmap(ctx, req), &tripmapv1.CreateSnapshotRequest{Reason: req.Msg.GetReason()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.CreateSnapshotResponse{
		Snapshot: mapInfo(out.GetSnapshot()), Created: out.GetCreated(),
	}), nil
}

func (h *Handler) GetSnapshot(ctx context.Context, req *connect.Request[bfftripmapv1.GetSnapshotRequest]) (*connect.Response[bfftripmapv1.GetSnapshotResponse], error) {
	out, err := h.client.GetSnapshot(h.tripmap(ctx, req), &tripmapv1.GetSnapshotRequest{Id: req.Msg.GetId()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.GetSnapshotResponse{
		Snapshot: mapInfo(out.GetSnapshot()), Data: mapData(out.GetData()),
	}), nil
}

func (h *Handler) RestoreSnapshot(ctx context.Context, req *connect.Request[bfftripmapv1.RestoreSnapshotRequest]) (*connect.Response[bfftripmapv1.RestoreSnapshotResponse], error) {
	in := req.Msg
	out, err := h.client.RestoreSnapshot(h.tripmap(ctx, req), &tripmapv1.RestoreSnapshotRequest{
		SnapshotId: in.GetSnapshotId(), BaseRevision: in.GetBaseRevision(), MutationId: in.GetMutationId(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.RestoreSnapshotResponse{
		Revision: out.GetRevision(), ChangedCount: out.GetChangedCount(),
		UndoSnapshot: mapInfo(out.GetUndoSnapshot()), Replayed: out.GetReplayed(),
	}), nil
}
