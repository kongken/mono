// Package admin adapts the browser-facing bff.metro.v1.AdminService to the
// internal metro.v1.AdminService; see package relay for how calls are carried.
package admin

import (
	"context"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/metro/relay"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/protobuf/proto"
)

type Handler struct{ client metrov1.AdminServiceClient }

func New(client metrov1.AdminServiceClient) *Handler { return &Handler{client: client} }

func (h *Handler) CreateCity(ctx context.Context, req *connect.Request[bffmetrov1.CreateCityRequest]) (*connect.Response[bffmetrov1.CreateCityResponse], error) {
	in, res := &metrov1.CreateCityRequest{}, &bffmetrov1.CreateCityResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.CreateCity(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) UpdateCity(ctx context.Context, req *connect.Request[bffmetrov1.UpdateCityRequest]) (*connect.Response[bffmetrov1.UpdateCityResponse], error) {
	in, res := &metrov1.UpdateCityRequest{}, &bffmetrov1.UpdateCityResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.UpdateCity(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) DeleteCity(ctx context.Context, req *connect.Request[bffmetrov1.DeleteCityRequest]) (*connect.Response[bffmetrov1.DeleteCityResponse], error) {
	in, res := &metrov1.DeleteCityRequest{}, &bffmetrov1.DeleteCityResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.DeleteCity(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) ListAdminCities(ctx context.Context, req *connect.Request[bffmetrov1.ListAdminCitiesRequest]) (*connect.Response[bffmetrov1.ListAdminCitiesResponse], error) {
	in, res := &metrov1.ListAdminCitiesRequest{}, &bffmetrov1.ListAdminCitiesResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.ListAdminCities(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) CreateRollingStock(ctx context.Context, req *connect.Request[bffmetrov1.CreateRollingStockRequest]) (*connect.Response[bffmetrov1.CreateRollingStockResponse], error) {
	in, res := &metrov1.CreateRollingStockRequest{}, &bffmetrov1.CreateRollingStockResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.CreateRollingStock(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) UpdateRollingStock(ctx context.Context, req *connect.Request[bffmetrov1.UpdateRollingStockRequest]) (*connect.Response[bffmetrov1.UpdateRollingStockResponse], error) {
	in, res := &metrov1.UpdateRollingStockRequest{}, &bffmetrov1.UpdateRollingStockResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.UpdateRollingStock(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) DeleteRollingStock(ctx context.Context, req *connect.Request[bffmetrov1.DeleteRollingStockRequest]) (*connect.Response[bffmetrov1.DeleteRollingStockResponse], error) {
	in, res := &metrov1.DeleteRollingStockRequest{}, &bffmetrov1.DeleteRollingStockResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.DeleteRollingStock(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) ListAdminRollingStocks(ctx context.Context, req *connect.Request[bffmetrov1.ListAdminRollingStocksRequest]) (*connect.Response[bffmetrov1.ListAdminRollingStocksResponse], error) {
	in, res := &metrov1.ListAdminRollingStocksRequest{}, &bffmetrov1.ListAdminRollingStocksResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.ListAdminRollingStocks(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) CreateLine(ctx context.Context, req *connect.Request[bffmetrov1.CreateLineRequest]) (*connect.Response[bffmetrov1.CreateLineResponse], error) {
	in, res := &metrov1.CreateLineRequest{}, &bffmetrov1.CreateLineResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.CreateLine(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) UpdateLine(ctx context.Context, req *connect.Request[bffmetrov1.UpdateLineRequest]) (*connect.Response[bffmetrov1.UpdateLineResponse], error) {
	in, res := &metrov1.UpdateLineRequest{}, &bffmetrov1.UpdateLineResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.UpdateLine(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) DeleteLine(ctx context.Context, req *connect.Request[bffmetrov1.DeleteLineRequest]) (*connect.Response[bffmetrov1.DeleteLineResponse], error) {
	in, res := &metrov1.DeleteLineRequest{}, &bffmetrov1.DeleteLineResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.DeleteLine(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) ListAdminLines(ctx context.Context, req *connect.Request[bffmetrov1.ListAdminLinesRequest]) (*connect.Response[bffmetrov1.ListAdminLinesResponse], error) {
	in, res := &metrov1.ListAdminLinesRequest{}, &bffmetrov1.ListAdminLinesResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.ListAdminLines(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) CreateRollingStockTopic(ctx context.Context, req *connect.Request[bffmetrov1.CreateRollingStockTopicRequest]) (*connect.Response[bffmetrov1.CreateRollingStockTopicResponse], error) {
	in, res := &metrov1.CreateRollingStockTopicRequest{}, &bffmetrov1.CreateRollingStockTopicResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.CreateRollingStockTopic(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) UpdateRollingStockTopic(ctx context.Context, req *connect.Request[bffmetrov1.UpdateRollingStockTopicRequest]) (*connect.Response[bffmetrov1.UpdateRollingStockTopicResponse], error) {
	in, res := &metrov1.UpdateRollingStockTopicRequest{}, &bffmetrov1.UpdateRollingStockTopicResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.UpdateRollingStockTopic(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) DeleteRollingStockTopic(ctx context.Context, req *connect.Request[bffmetrov1.DeleteRollingStockTopicRequest]) (*connect.Response[bffmetrov1.DeleteRollingStockTopicResponse], error) {
	in, res := &metrov1.DeleteRollingStockTopicRequest{}, &bffmetrov1.DeleteRollingStockTopicResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.DeleteRollingStockTopic(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) ListAdminRollingStockTopics(ctx context.Context, req *connect.Request[bffmetrov1.ListAdminRollingStockTopicsRequest]) (*connect.Response[bffmetrov1.ListAdminRollingStockTopicsResponse], error) {
	in, res := &metrov1.ListAdminRollingStockTopicsRequest{}, &bffmetrov1.ListAdminRollingStockTopicsResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.ListAdminRollingStockTopics(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) ImportCatalog(ctx context.Context, req *connect.Request[bffmetrov1.ImportCatalogRequest]) (*connect.Response[bffmetrov1.ImportCatalogResponse], error) {
	in, res := &metrov1.ImportCatalogRequest{}, &bffmetrov1.ImportCatalogResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.ImportCatalog(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) ExportCatalog(ctx context.Context, req *connect.Request[bffmetrov1.ExportCatalogRequest]) (*connect.Response[bffmetrov1.ExportCatalogResponse], error) {
	in, res := &metrov1.ExportCatalogRequest{}, &bffmetrov1.ExportCatalogResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.ExportCatalog(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) ListAuditLogs(ctx context.Context, req *connect.Request[bffmetrov1.ListAuditLogsRequest]) (*connect.Response[bffmetrov1.ListAuditLogsResponse], error) {
	in, res := &metrov1.ListAuditLogsRequest{}, &bffmetrov1.ListAuditLogsResponse{}
	if err := relay.Call(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.ListAuditLogs(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}
