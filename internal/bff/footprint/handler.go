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

func (h *Handler) GetFootprints(ctx context.Context, req *connect.Request[bffv1.GetFootprintsRequest]) (*connect.Response[bffv1.GetFootprintsResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.client.GetFootprints(ctx, &tripmapv1.GetFootprintsRequest{})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(mapFootprints(out)), nil
}

func mapFootprints(in *tripmapv1.GetFootprintsResponse) *bffv1.GetFootprintsResponse {
	out := &bffv1.GetFootprintsResponse{
		Revision: in.GetRevision(), RegionVersion: in.GetRegionVersion(), SchemaVersion: in.GetSchemaVersion(),
	}
	for _, v := range in.GetVisits() {
		out.Visits = append(out.Visits, &bffv1.Visit{
			Code: v.GetCode(), Name: v.GetName(), Province: v.GetProvince(), Color: v.GetColor(),
			Date: v.GetDate(), Note: v.GetNote(), UpdatedAt: v.GetUpdatedAt(),
		})
	}
	for _, m := range in.GetProvinceMarks() {
		out.ProvinceMarks = append(out.ProvinceMarks, &bffv1.ProvinceMark{
			Province: m.GetProvince(), Color: m.GetColor(), UpdatedAt: m.GetUpdatedAt(),
		})
	}
	for _, t := range in.GetTrips() {
		out.Trips = append(out.Trips, &bffv1.Trip{
			Id: t.GetId(), City: t.GetRegionCode(), Date: t.GetDate(),
			Note: t.GetNote(), UpdatedAt: t.GetUpdatedAt(),
		})
	}
	return out
}
