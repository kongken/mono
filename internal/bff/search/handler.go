package search

import (
	"context"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/upstream"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type Handler struct {
	client tripmapv1.SearchServiceClient
}

func New(client tripmapv1.SearchServiceClient) *Handler {
	return &Handler{client: client}
}

func (h *Handler) SearchNotes(ctx context.Context, req *connect.Request[bffv1.SearchNotesRequest]) (*connect.Response[bffv1.SearchNotesResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	in := &tripmapv1.SearchNotesRequest{
		Query: req.Msg.GetQuery(), ProvinceCode: req.Msg.GetProvinceCode(),
		Limit: req.Msg.GetLimit(), Offset: req.Msg.GetOffset(),
	}
	if req.Msg.ExpectedRevision != nil {
		rev := req.Msg.GetExpectedRevision()
		in.ExpectedRevision = &rev
	}
	out, err := h.client.SearchNotes(ctx, in)
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(mapSearchResponse(out)), nil
}
