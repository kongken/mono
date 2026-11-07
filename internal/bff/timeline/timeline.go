package timeline

import (
	"connectrpc.com/connect"
	"context"
	timelinemcp "github.com/kongken/mono/internal/bff/timeline/mcp"
	"github.com/kongken/mono/internal/bff/upstream"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/timeline/v1"
	v1 "github.com/kongken/mono/pkg/proto/timeline/v1"
	"google.golang.org/grpc"
	"net/http"
)

type Handler struct{ client v1.TimelineServiceClient }

func NewMCP(conn *grpc.ClientConn, tokens timelinemcp.Authenticator, accounts timelinemcp.Accounts, limiter timelinemcp.Limiter, ratePerMinute int, perimeter timelinemcp.Perimeter) http.Handler {
	return timelinemcp.New(timelinemcp.Config{Admin: v1.NewTimelineServiceClient(conn), Tokens: tokens, Accounts: accounts, Limiter: limiter, RatePerMinute: ratePerMinute, Perimeter: perimeter})
}

func NewHandler(c grpc.ClientConnInterface) *Handler {
	return &Handler{client: v1.NewTimelineServiceClient(c)}
}
func in(e *bffv1.Entry) *v1.Entry {
	if e == nil {
		return nil
	}
	return &v1.Entry{Id: e.Id, EntryRef: e.EntryRef, Title: e.Title, Date: e.Date, Time: e.Time, Timezone: e.Timezone, Category: e.Category, Location: e.Location, AmountMinor: e.AmountMinor, Currency: e.Currency, Note: e.Note, Url: e.Url, UrlTitle: e.UrlTitle, UrlNote: e.UrlNote, Revision: e.Revision, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt, DeletedAt: e.DeletedAt}
}
func out(e *v1.Entry) *bffv1.Entry {
	if e == nil {
		return nil
	}
	return &bffv1.Entry{Id: e.Id, EntryRef: e.EntryRef, Title: e.Title, Date: e.Date, Time: e.Time, Timezone: e.Timezone, Category: e.Category, Location: e.Location, AmountMinor: e.AmountMinor, Currency: e.Currency, Note: e.Note, Url: e.Url, UrlTitle: e.UrlTitle, UrlNote: e.UrlNote, Revision: e.Revision, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt, DeletedAt: e.DeletedAt}
}
func receipt(r *v1.MutationReceipt) *bffv1.MutationReceipt {
	if r == nil {
		return nil
	}
	return &bffv1.MutationReceipt{MutationId: r.MutationId, Operation: r.Operation, ResultRevision: r.ResultRevision, Replayed: r.Replayed}
}
func (h *Handler) ListEntries(c context.Context, r *connect.Request[bffv1.ListEntriesRequest]) (*connect.Response[bffv1.ListEntriesResponse], error) {
	x, e := h.client.ListEntries(c, &v1.ListEntriesRequest{Query: r.Msg.Query, Category: r.Msg.Category, FromDate: r.Msg.FromDate, ToDate: r.Msg.ToDate, PageSize: r.Msg.PageSize, PageToken: r.Msg.PageToken, IncludeDeleted: r.Msg.IncludeDeleted})
	if e != nil {
		return nil, upstream.ToConnect("timeline", e)
	}
	o := &bffv1.ListEntriesResponse{NextPageToken: x.NextPageToken}
	for _, v := range x.Entries {
		o.Entries = append(o.Entries, out(v))
	}
	return connect.NewResponse(o), nil
}
func (h *Handler) CreateEntry(c context.Context, r *connect.Request[bffv1.CreateEntryRequest]) (*connect.Response[bffv1.EntryResponse], error) {
	x, e := h.client.CreateEntry(c, &v1.CreateEntryRequest{Entry: in(r.Msg.Entry), MutationId: r.Msg.MutationId})
	if e != nil {
		return nil, upstream.ToConnect("timeline", e)
	}
	return connect.NewResponse(&bffv1.EntryResponse{Entry: out(x.Entry), Receipt: receipt(x.Receipt)}), nil
}
func (h *Handler) UpdateEntry(c context.Context, r *connect.Request[bffv1.UpdateEntryRequest]) (*connect.Response[bffv1.EntryResponse], error) {
	x, e := h.client.UpdateEntry(c, &v1.UpdateEntryRequest{Entry: in(r.Msg.Entry), EntryRef: r.Msg.EntryRef, ExpectedRevision: r.Msg.ExpectedRevision, MutationId: r.Msg.MutationId})
	if e != nil {
		return nil, upstream.ToConnect("timeline", e)
	}
	return connect.NewResponse(&bffv1.EntryResponse{Entry: out(x.Entry), Receipt: receipt(x.Receipt)}), nil
}
func (h *Handler) DeleteEntry(c context.Context, r *connect.Request[bffv1.DeleteEntryRequest]) (*connect.Response[bffv1.DeleteEntryResponse], error) {
	x, e := h.client.DeleteEntry(c, &v1.DeleteEntryRequest{Id: r.Msg.Id, EntryRef: r.Msg.EntryRef, ExpectedRevision: r.Msg.ExpectedRevision, MutationId: r.Msg.MutationId, Permanent: r.Msg.Permanent})
	if e != nil {
		return nil, upstream.ToConnect("timeline", e)
	}
	return connect.NewResponse(&bffv1.DeleteEntryResponse{Entry: out(x.Entry), Receipt: receipt(x.Receipt)}), nil
}
func (h *Handler) RestoreEntry(c context.Context, r *connect.Request[bffv1.RestoreEntryRequest]) (*connect.Response[bffv1.EntryResponse], error) {
	x, e := h.client.RestoreEntry(c, &v1.RestoreEntryRequest{Id: r.Msg.Id, EntryRef: r.Msg.EntryRef, ExpectedRevision: r.Msg.ExpectedRevision, MutationId: r.Msg.MutationId})
	if e != nil {
		return nil, upstream.ToConnect("timeline", e)
	}
	return connect.NewResponse(&bffv1.EntryResponse{Entry: out(x.Entry), Receipt: receipt(x.Receipt)}), nil
}
func (h *Handler) PurgeEntry(c context.Context, r *connect.Request[bffv1.PurgeEntryRequest]) (*connect.Response[bffv1.PurgeEntryResponse], error) {
	x, e := h.client.PurgeEntry(c, &v1.PurgeEntryRequest{Id: r.Msg.Id, EntryRef: r.Msg.EntryRef, ExpectedRevision: r.Msg.ExpectedRevision, MutationId: r.Msg.MutationId})
	if e != nil {
		return nil, upstream.ToConnect("timeline", e)
	}
	return connect.NewResponse(&bffv1.PurgeEntryResponse{Receipt: receipt(x.Receipt)}), nil
}
