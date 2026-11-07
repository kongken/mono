package timeline

import (
	"connectrpc.com/connect"
	"context"
	"github.com/kongken/mono/internal/bff/upstream"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/timeline/v1"
	v1 "github.com/kongken/mono/pkg/proto/timeline/v1"
	"google.golang.org/grpc"
)

type Handler struct{ client v1.TimelineServiceClient }

func NewHandler(conn grpc.ClientConnInterface) *Handler {
	return &Handler{client: v1.NewTimelineServiceClient(conn)}
}
func in(e *bffv1.Entry) *v1.Entry {
	if e == nil {
		return nil
	}
	return &v1.Entry{Id: e.Id, Title: e.Title, Date: e.Date, Time: e.Time, Timezone: e.Timezone, Category: e.Category, Location: e.Location, AmountMinor: e.AmountMinor, Currency: e.Currency, Note: e.Note, Url: e.Url, UrlTitle: e.UrlTitle, UrlNote: e.UrlNote, Revision: e.Revision, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt, DeletedAt: e.DeletedAt}
}
func out(e *v1.Entry) *bffv1.Entry {
	if e == nil {
		return nil
	}
	return &bffv1.Entry{Id: e.Id, Title: e.Title, Date: e.Date, Time: e.Time, Timezone: e.Timezone, Category: e.Category, Location: e.Location, AmountMinor: e.AmountMinor, Currency: e.Currency, Note: e.Note, Url: e.Url, UrlTitle: e.UrlTitle, UrlNote: e.UrlNote, Revision: e.Revision, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt, DeletedAt: e.DeletedAt}
}
func (h *Handler) ListEntries(ctx context.Context, r *connect.Request[bffv1.ListEntriesRequest]) (*connect.Response[bffv1.ListEntriesResponse], error) {
	x, err := h.client.ListEntries(ctx, &v1.ListEntriesRequest{Query: r.Msg.Query, Category: r.Msg.Category, FromDate: r.Msg.FromDate, ToDate: r.Msg.ToDate, PageSize: r.Msg.PageSize, PageToken: r.Msg.PageToken, IncludeDeleted: r.Msg.IncludeDeleted})
	if err != nil {
		return nil, upstream.ToConnect("timeline", err)
	}
	o := &bffv1.ListEntriesResponse{NextPageToken: x.NextPageToken}
	for _, e := range x.Entries {
		o.Entries = append(o.Entries, out(e))
	}
	return connect.NewResponse(o), nil
}
func (h *Handler) CreateEntry(ctx context.Context, r *connect.Request[bffv1.CreateEntryRequest]) (*connect.Response[bffv1.EntryResponse], error) {
	x, err := h.client.CreateEntry(ctx, &v1.CreateEntryRequest{Entry: in(r.Msg.Entry), MutationId: r.Msg.MutationId})
	if err != nil {
		return nil, upstream.ToConnect("timeline", err)
	}
	return connect.NewResponse(&bffv1.EntryResponse{Entry: out(x.Entry)}), nil
}
func (h *Handler) UpdateEntry(ctx context.Context, r *connect.Request[bffv1.UpdateEntryRequest]) (*connect.Response[bffv1.EntryResponse], error) {
	x, err := h.client.UpdateEntry(ctx, &v1.UpdateEntryRequest{Entry: in(r.Msg.Entry), ExpectedRevision: r.Msg.ExpectedRevision, MutationId: r.Msg.MutationId})
	if err != nil {
		return nil, upstream.ToConnect("timeline", err)
	}
	return connect.NewResponse(&bffv1.EntryResponse{Entry: out(x.Entry)}), nil
}
func (h *Handler) DeleteEntry(ctx context.Context, r *connect.Request[bffv1.DeleteEntryRequest]) (*connect.Response[bffv1.Empty], error) {
	_, err := h.client.DeleteEntry(ctx, &v1.DeleteEntryRequest{Id: r.Msg.Id, ExpectedRevision: r.Msg.ExpectedRevision, MutationId: r.Msg.MutationId, Permanent: r.Msg.Permanent})
	if err != nil {
		return nil, upstream.ToConnect("timeline", err)
	}
	return connect.NewResponse(&bffv1.Empty{}), nil
}
func (h *Handler) RestoreEntry(ctx context.Context, r *connect.Request[bffv1.RestoreEntryRequest]) (*connect.Response[bffv1.EntryResponse], error) {
	x, err := h.client.RestoreEntry(ctx, &v1.RestoreEntryRequest{Id: r.Msg.Id, ExpectedRevision: r.Msg.ExpectedRevision, MutationId: r.Msg.MutationId})
	if err != nil {
		return nil, upstream.ToConnect("timeline", err)
	}
	return connect.NewResponse(&bffv1.EntryResponse{Entry: out(x.Entry)}), nil
}
func (h *Handler) PurgeEntry(ctx context.Context, r *connect.Request[bffv1.PurgeEntryRequest]) (*connect.Response[bffv1.Empty], error) {
	_, err := h.client.PurgeEntry(ctx, &v1.PurgeEntryRequest{Id: r.Msg.Id, ExpectedRevision: r.Msg.ExpectedRevision, MutationId: r.Msg.MutationId})
	if err != nil {
		return nil, upstream.ToConnect("timeline", err)
	}
	return connect.NewResponse(&bffv1.Empty{}), nil
}
