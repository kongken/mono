package region

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/bff/upstream"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type Handler struct {
	client         tripmapv1.RegionServiceClient
	allowedOrigins []string
}

func New(client tripmapv1.RegionServiceClient, allowedOrigins []string) *Handler {
	return &Handler{client: client, allowedOrigins: allowedOrigins}
}

func (h *Handler) checkWrite(header http.Header) error {
	if err := session.CheckWrite(header, h.allowedOrigins, header.Get("Host")); err != nil {
		return connect.NewError(connect.CodePermissionDenied, errors.New("request rejected"))
	}
	return nil
}

func (h *Handler) GetRegionIndex(ctx context.Context, req *connect.Request[bffv1.GetRegionIndexRequest]) (*connect.Response[bffv1.GetRegionIndexResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.client.GetRegionIndex(ctx, &tripmapv1.GetRegionIndexRequest{KnownVersion: req.Msg.GetKnownVersion()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(mapIndex(out)), nil
}

func (h *Handler) PreviewRegionUpgrade(ctx context.Context, req *connect.Request[bffv1.PreviewRegionUpgradeRequest]) (*connect.Response[bffv1.PreviewRegionUpgradeResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.client.PreviewRegionUpgrade(ctx, &tripmapv1.PreviewRegionUpgradeRequest{})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(mapPreview(out)), nil
}

func (h *Handler) ApplyRegionUpgrade(ctx context.Context, req *connect.Request[bffv1.ApplyRegionUpgradeRequest]) (*connect.Response[bffv1.ApplyRegionUpgradeResponse], error) {
	if err := h.checkWrite(req.Header()); err != nil {
		return nil, err
	}
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	in := req.Msg
	out, err := h.client.ApplyRegionUpgrade(ctx, &tripmapv1.ApplyRegionUpgradeRequest{
		Version: in.GetVersion(), BaseRevision: in.GetBaseRevision(), MutationId: in.GetMutationId(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(mapApply(out)), nil
}

func mapIndex(in *tripmapv1.GetRegionIndexResponse) *bffv1.GetRegionIndexResponse {
	out := &bffv1.GetRegionIndexResponse{
		Version: in.GetVersion(), NotModified: in.GetNotModified(),
		IndexJson: in.GetIndexJson(), SupportedVersions: in.GetSupportedVersions(),
	}
	for _, m := range in.GetMappings() {
		out.Mappings = append(out.Mappings, &bffv1.RegionMapping{
			FromCode: m.GetFromCode(), FromName: m.GetFromName(), FromProvince: m.GetFromProvince(), ToCode: m.GetToCode(),
		})
	}
	return out
}

func mapPreview(in *tripmapv1.PreviewRegionUpgradeResponse) *bffv1.PreviewRegionUpgradeResponse {
	out := &bffv1.PreviewRegionUpgradeResponse{
		Version: in.GetVersion(), Revision: in.GetRevision(), Blockers: in.GetBlockers(),
	}
	for _, c := range in.GetChanges() {
		out.Changes = append(out.Changes, &bffv1.RegionUpgradeChange{
			FromCode: c.GetFromCode(), ToCode: c.GetToCode(), FromName: c.GetFromName(), ToName: c.GetToName(),
			FromProvince: c.GetFromProvince(), ToProvince: c.GetToProvince(), MediaOnly: c.GetMediaOnly(),
		})
	}
	return out
}

func mapApply(in *tripmapv1.ApplyRegionUpgradeResponse) *bffv1.ApplyRegionUpgradeResponse {
	return &bffv1.ApplyRegionUpgradeResponse{
		Version: in.GetVersion(), Revision: in.GetRevision(), ChangedCount: in.GetChangedCount(),
		Replayed: in.GetReplayed(), SnapshotId: in.GetSnapshotId(),
	}
}
