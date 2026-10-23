// Package admin adapts the browser-facing bff.metro.v1.AdminService to the
// internal metro.v1.AdminService.
//
// The two contracts are separate files and separate Go packages, but the
// administration messages are deliberately identical in shape (the admin pages and
// MCP tools see exactly the error details and revisions metro produces). Instead
// of forty hand-written field copies, each call converts through the protobuf
// wire format; TestContractsStayWireCompatible fails the build when the two
// definitions drift apart, so a field added to one side cannot be silently lost.
package admin

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/upstream"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/protoadapt"
)

// Service is the upstream name used in errors and logs.
const Service = "metro"

type Handler struct{ client metrov1.AdminServiceClient }

func New(client metrov1.AdminServiceClient) *Handler { return &Handler{client: client} }

// convert copies src into dst through the wire format. The messages are
// structurally identical, so nothing is lost.
func convert(src, dst proto.Message) error {
	b, err := proto.Marshal(src)
	if err != nil {
		return err
	}
	return proto.Unmarshal(b, dst)
}

// relay forwards one call: browser request -> internal request, the call, internal
// response -> browser response.
func relay(ctx context.Context, browserReq proto.Message, internalReq proto.Message,
	call func(context.Context) (proto.Message, error), browserRes proto.Message) error {
	if err := convert(browserReq, internalReq); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("malformed request"))
	}
	res, err := call(ctx)
	if err != nil {
		return mapError(err)
	}
	if err = convert(res, browserRes); err != nil {
		return connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return nil
}

// mapError turns a metro status into a Connect error. Domain failures keep their
// message and typed details (field errors, the current revision, what still
// references a record) so a client can act on them; everything else goes through
// the shared mapping that hides internals.
func mapError(err error) error {
	st := status.Convert(err)
	switch st.Code() {
	case codes.InvalidArgument, codes.AlreadyExists, codes.FailedPrecondition, codes.NotFound, codes.PermissionDenied:
	default:
		return upstream.ToConnect(Service, err)
	}
	out := connect.NewError(connect.Code(st.Code()), errors.New(st.Message()))
	for _, d := range st.Details() {
		var src proto.Message
		switch m := d.(type) {
		case protoadapt.MessageV1:
			src = protoadapt.MessageV2Of(m)
		case proto.Message:
			src = m
		default:
			continue
		}
		var dst proto.Message
		switch src.(type) {
		case *metrov1.ValidationDetail:
			dst = &bffmetrov1.ValidationDetail{}
		case *metrov1.RevisionConflictDetail:
			dst = &bffmetrov1.RevisionConflictDetail{}
		case *metrov1.ReferencedDetail:
			dst = &bffmetrov1.ReferencedDetail{}
		default:
			continue
		}
		if convert(src, dst) != nil {
			continue
		}
		if detail, derr := connect.NewErrorDetail(dst); derr == nil {
			out.AddDetail(detail)
		}
	}
	return out
}

func (h *Handler) CreateCity(ctx context.Context, req *connect.Request[bffmetrov1.CreateCityRequest]) (*connect.Response[bffmetrov1.CreateCityResponse], error) {
	in, res := &metrov1.CreateCityRequest{}, &bffmetrov1.CreateCityResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.CreateCity(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) UpdateCity(ctx context.Context, req *connect.Request[bffmetrov1.UpdateCityRequest]) (*connect.Response[bffmetrov1.UpdateCityResponse], error) {
	in, res := &metrov1.UpdateCityRequest{}, &bffmetrov1.UpdateCityResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.UpdateCity(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) DeleteCity(ctx context.Context, req *connect.Request[bffmetrov1.DeleteCityRequest]) (*connect.Response[bffmetrov1.DeleteCityResponse], error) {
	in, res := &metrov1.DeleteCityRequest{}, &bffmetrov1.DeleteCityResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.DeleteCity(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) ListAdminCities(ctx context.Context, req *connect.Request[bffmetrov1.ListAdminCitiesRequest]) (*connect.Response[bffmetrov1.ListAdminCitiesResponse], error) {
	in, res := &metrov1.ListAdminCitiesRequest{}, &bffmetrov1.ListAdminCitiesResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.ListAdminCities(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) CreateRollingStock(ctx context.Context, req *connect.Request[bffmetrov1.CreateRollingStockRequest]) (*connect.Response[bffmetrov1.CreateRollingStockResponse], error) {
	in, res := &metrov1.CreateRollingStockRequest{}, &bffmetrov1.CreateRollingStockResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.CreateRollingStock(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) UpdateRollingStock(ctx context.Context, req *connect.Request[bffmetrov1.UpdateRollingStockRequest]) (*connect.Response[bffmetrov1.UpdateRollingStockResponse], error) {
	in, res := &metrov1.UpdateRollingStockRequest{}, &bffmetrov1.UpdateRollingStockResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.UpdateRollingStock(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) DeleteRollingStock(ctx context.Context, req *connect.Request[bffmetrov1.DeleteRollingStockRequest]) (*connect.Response[bffmetrov1.DeleteRollingStockResponse], error) {
	in, res := &metrov1.DeleteRollingStockRequest{}, &bffmetrov1.DeleteRollingStockResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.DeleteRollingStock(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) ListAdminRollingStocks(ctx context.Context, req *connect.Request[bffmetrov1.ListAdminRollingStocksRequest]) (*connect.Response[bffmetrov1.ListAdminRollingStocksResponse], error) {
	in, res := &metrov1.ListAdminRollingStocksRequest{}, &bffmetrov1.ListAdminRollingStocksResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.ListAdminRollingStocks(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) CreateLine(ctx context.Context, req *connect.Request[bffmetrov1.CreateLineRequest]) (*connect.Response[bffmetrov1.CreateLineResponse], error) {
	in, res := &metrov1.CreateLineRequest{}, &bffmetrov1.CreateLineResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.CreateLine(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) UpdateLine(ctx context.Context, req *connect.Request[bffmetrov1.UpdateLineRequest]) (*connect.Response[bffmetrov1.UpdateLineResponse], error) {
	in, res := &metrov1.UpdateLineRequest{}, &bffmetrov1.UpdateLineResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.UpdateLine(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) DeleteLine(ctx context.Context, req *connect.Request[bffmetrov1.DeleteLineRequest]) (*connect.Response[bffmetrov1.DeleteLineResponse], error) {
	in, res := &metrov1.DeleteLineRequest{}, &bffmetrov1.DeleteLineResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.DeleteLine(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) ListAdminLines(ctx context.Context, req *connect.Request[bffmetrov1.ListAdminLinesRequest]) (*connect.Response[bffmetrov1.ListAdminLinesResponse], error) {
	in, res := &metrov1.ListAdminLinesRequest{}, &bffmetrov1.ListAdminLinesResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.ListAdminLines(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) CreateRollingStockTopic(ctx context.Context, req *connect.Request[bffmetrov1.CreateRollingStockTopicRequest]) (*connect.Response[bffmetrov1.CreateRollingStockTopicResponse], error) {
	in, res := &metrov1.CreateRollingStockTopicRequest{}, &bffmetrov1.CreateRollingStockTopicResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.CreateRollingStockTopic(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) UpdateRollingStockTopic(ctx context.Context, req *connect.Request[bffmetrov1.UpdateRollingStockTopicRequest]) (*connect.Response[bffmetrov1.UpdateRollingStockTopicResponse], error) {
	in, res := &metrov1.UpdateRollingStockTopicRequest{}, &bffmetrov1.UpdateRollingStockTopicResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.UpdateRollingStockTopic(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) DeleteRollingStockTopic(ctx context.Context, req *connect.Request[bffmetrov1.DeleteRollingStockTopicRequest]) (*connect.Response[bffmetrov1.DeleteRollingStockTopicResponse], error) {
	in, res := &metrov1.DeleteRollingStockTopicRequest{}, &bffmetrov1.DeleteRollingStockTopicResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.DeleteRollingStockTopic(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) ListAdminRollingStockTopics(ctx context.Context, req *connect.Request[bffmetrov1.ListAdminRollingStockTopicsRequest]) (*connect.Response[bffmetrov1.ListAdminRollingStockTopicsResponse], error) {
	in, res := &metrov1.ListAdminRollingStockTopicsRequest{}, &bffmetrov1.ListAdminRollingStockTopicsResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.ListAdminRollingStockTopics(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) ImportCatalog(ctx context.Context, req *connect.Request[bffmetrov1.ImportCatalogRequest]) (*connect.Response[bffmetrov1.ImportCatalogResponse], error) {
	in, res := &metrov1.ImportCatalogRequest{}, &bffmetrov1.ImportCatalogResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.ImportCatalog(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) ExportCatalog(ctx context.Context, req *connect.Request[bffmetrov1.ExportCatalogRequest]) (*connect.Response[bffmetrov1.ExportCatalogResponse], error) {
	in, res := &metrov1.ExportCatalogRequest{}, &bffmetrov1.ExportCatalogResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.ExportCatalog(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) ListAuditLogs(ctx context.Context, req *connect.Request[bffmetrov1.ListAuditLogsRequest]) (*connect.Response[bffmetrov1.ListAuditLogsResponse], error) {
	in, res := &metrov1.ListAuditLogsRequest{}, &bffmetrov1.ListAuditLogsResponse{}
	if err := relay(ctx, req.Msg, in, func(ctx context.Context) (proto.Message, error) { return h.client.ListAuditLogs(ctx, in) }, res); err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}
