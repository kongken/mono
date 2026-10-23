// Package catalog adapts the browser-facing bff.metro.v1.CatalogService to the
// internal metro.v1.CatalogService. The two contracts are separate; every field
// is mapped here by hand so a change to either shows up in review.
package catalog

import (
	"context"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/upstream"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
)

// Service is the upstream name used in errors and logs.
const Service = "metro"

type Handler struct{ client metrov1.CatalogServiceClient }

func New(client metrov1.CatalogServiceClient) *Handler { return &Handler{client: client} }

func (h *Handler) ListCities(ctx context.Context, req *connect.Request[bffmetrov1.ListCitiesRequest]) (*connect.Response[bffmetrov1.ListCitiesResponse], error) {
	in, err := h.client.ListCities(ctx, &metrov1.ListCitiesRequest{KnownVersion: req.Msg.GetKnownVersion()})
	if err != nil {
		return nil, upstream.ToConnect(Service, err)
	}
	out := &bffmetrov1.ListCitiesResponse{Version: in.GetVersion(), NotModified: in.GetNotModified()}
	for _, c := range in.GetCities() {
		out.Cities = append(out.Cities, mapCity(c))
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) GetCity(ctx context.Context, req *connect.Request[bffmetrov1.GetCityRequest]) (*connect.Response[bffmetrov1.GetCityResponse], error) {
	in, err := h.client.GetCity(ctx, &metrov1.GetCityRequest{Slug: req.Msg.GetSlug(), Statuses: toInternalStatuses(req.Msg.GetStatuses())})
	if err != nil {
		return nil, upstream.ToConnect(Service, err)
	}
	return connect.NewResponse(&bffmetrov1.GetCityResponse{
		Version: in.GetVersion(), City: mapCity(in.GetCity()), Lines: mapLines(in.GetLines()),
		TotalLines: in.GetTotalLines(), TotalLengthKm: in.GetTotalLengthKm(),
	}), nil
}

func (h *Handler) ListLines(ctx context.Context, req *connect.Request[bffmetrov1.ListLinesRequest]) (*connect.Response[bffmetrov1.ListLinesResponse], error) {
	in, err := h.client.ListLines(ctx, &metrov1.ListLinesRequest{CitySlug: req.Msg.GetCitySlug(), Statuses: toInternalStatuses(req.Msg.GetStatuses())})
	if err != nil {
		return nil, upstream.ToConnect(Service, err)
	}
	return connect.NewResponse(&bffmetrov1.ListLinesResponse{Version: in.GetVersion(), Lines: mapLines(in.GetLines())}), nil
}

func (h *Handler) ListRollingStockTopics(ctx context.Context, _ *connect.Request[bffmetrov1.ListRollingStockTopicsRequest]) (*connect.Response[bffmetrov1.ListRollingStockTopicsResponse], error) {
	in, err := h.client.ListRollingStockTopics(ctx, &metrov1.ListRollingStockTopicsRequest{})
	if err != nil {
		return nil, upstream.ToConnect(Service, err)
	}
	out := &bffmetrov1.ListRollingStockTopicsResponse{Version: in.GetVersion()}
	for _, t := range in.GetTopics() {
		out.Topics = append(out.Topics, mapTopic(t))
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) GetRollingStockTopic(ctx context.Context, req *connect.Request[bffmetrov1.GetRollingStockTopicRequest]) (*connect.Response[bffmetrov1.GetRollingStockTopicResponse], error) {
	in, err := h.client.GetRollingStockTopic(ctx, &metrov1.GetRollingStockTopicRequest{Slug: req.Msg.GetSlug()})
	if err != nil {
		return nil, upstream.ToConnect(Service, err)
	}
	out := &bffmetrov1.GetRollingStockTopicResponse{
		Version: in.GetVersion(), Topic: mapTopic(in.GetTopic()), Lines: mapLines(in.GetLines()),
		TotalLines: in.GetTotalLines(), TotalLengthKm: in.GetTotalLengthKm(),
	}
	for _, c := range in.GetCities() {
		out.Cities = append(out.Cities, &bffmetrov1.TopicCity{
			CitySlug: c.GetCitySlug(), CityName: c.GetCityName(), LineCount: c.GetLineCount(), TotalLengthKm: c.GetTotalLengthKm(),
		})
	}
	return connect.NewResponse(out), nil
}

// toInternalStatuses maps the browser enum onto the internal one. The numbers
// are not assumed to match; an unknown value stays unknown and metro refuses it.
func toInternalStatuses(in []bffmetrov1.LineStatus) []metrov1.LineStatus {
	out := make([]metrov1.LineStatus, 0, len(in))
	for _, s := range in {
		out = append(out, toInternalStatus(s))
	}
	return out
}

var internalStatus = map[bffmetrov1.LineStatus]metrov1.LineStatus{
	bffmetrov1.LineStatus_LINE_STATUS_UNSPECIFIED:        metrov1.LineStatus_LINE_STATUS_UNSPECIFIED,
	bffmetrov1.LineStatus_LINE_STATUS_PLANNED:            metrov1.LineStatus_LINE_STATUS_PLANNED,
	bffmetrov1.LineStatus_LINE_STATUS_UNDER_CONSTRUCTION: metrov1.LineStatus_LINE_STATUS_UNDER_CONSTRUCTION,
	bffmetrov1.LineStatus_LINE_STATUS_OPERATING:          metrov1.LineStatus_LINE_STATUS_OPERATING,
	bffmetrov1.LineStatus_LINE_STATUS_SUSPENDED:          metrov1.LineStatus_LINE_STATUS_SUSPENDED,
	bffmetrov1.LineStatus_LINE_STATUS_RETIRED:            metrov1.LineStatus_LINE_STATUS_RETIRED,
}

func toInternalStatus(s bffmetrov1.LineStatus) metrov1.LineStatus {
	if v, ok := internalStatus[s]; ok {
		return v
	}
	return metrov1.LineStatus(-1)
}

func toBrowserStatus(s metrov1.LineStatus) bffmetrov1.LineStatus {
	for k, v := range internalStatus {
		if v == s {
			return k
		}
	}
	return bffmetrov1.LineStatus_LINE_STATUS_UNSPECIFIED
}

func mapCity(c *metrov1.City) *bffmetrov1.City {
	if c == nil {
		return nil
	}
	return &bffmetrov1.City{
		Slug: c.GetSlug(), Name: c.GetName(), HeaderImages: c.GetHeaderImages(), MapSvgUrl: c.GetMapSvgUrl(),
		SortOrder: c.GetSortOrder(), LineCount: c.GetLineCount(), TotalLengthKm: c.GetTotalLengthKm(),
	}
}

func mapLines(in []*metrov1.Line) []*bffmetrov1.Line {
	out := make([]*bffmetrov1.Line, 0, len(in))
	for _, l := range in {
		out = append(out, &bffmetrov1.Line{
			Code: l.GetCode(), CitySlug: l.GetCitySlug(), CityName: l.GetCityName(), Name: l.GetName(), Number: l.GetNumber(),
			Status: toBrowserStatus(l.GetStatus()), RollingStockCode: l.GetRollingStockCode(),
			Cars: l.GetCars(), CarsAddition: l.GetCarsAddition(), Stations: l.GetStations(), LengthKm: l.GetLengthKm(),
			Color: l.GetColor(), CommencedOn: l.GetCommencedOn(), ExtendedOn: l.GetExtendedOn(), IsLoop: l.GetIsLoop(),
		})
	}
	return out
}

func mapTopic(t *metrov1.RollingStockTopic) *bffmetrov1.RollingStockTopic {
	if t == nil {
		return nil
	}
	return &bffmetrov1.RollingStockTopic{
		Slug: t.GetSlug(), Name: t.GetName(), RollingStockCodes: t.GetRollingStockCodes(), Cars: t.GetCars(),
	}
}
