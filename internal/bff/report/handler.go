package report

import (
	"context"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/upstream"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type Handler struct {
	client tripmapv1.ReportServiceClient
}

func New(client tripmapv1.ReportServiceClient) *Handler {
	return &Handler{client: client}
}

func (h *Handler) GetReport(ctx context.Context, req *connect.Request[bffv1.GetReportRequest]) (*connect.Response[bffv1.GetReportResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.client.GetReport(ctx, &tripmapv1.GetReportRequest{
		Timezone: req.Msg.GetTimezone(),
		Year:     req.Msg.GetYear(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	mapped, err := mapReportResponse(out)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(mapped), nil
}
