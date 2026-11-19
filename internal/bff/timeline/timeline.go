package timeline

import (
	"connectrpc.com/connect"
	"context"
	"github.com/kongken/mono/internal/bff/timeline/mcp"
	"github.com/kongken/mono/internal/bff/upstream"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/timeline/v1"
	v1 "github.com/kongken/mono/pkg/proto/timeline/v1"
	"google.golang.org/grpc"
	"net/http"
)

type Handler struct{ client v1.TimelineServiceClient }

// NewMCP mounts the Timeline Streamable HTTP endpoint with the same internal
// Timeline service used by the browser API. Authentication, account state and
// source perimeter remain owned by the BFF MCP wrapper.
func NewMCP(conn *grpc.ClientConn, tokens mcp.Authenticator, accounts mcp.Accounts, limiter mcp.Limiter, ratePerMinute int, perimeter mcp.Perimeter) http.Handler {
	return mcp.New(mcp.Config{Admin: v1.NewTimelineServiceClient(conn), Media: v1.NewMediaServiceClient(conn), Tokens: tokens, Accounts: accounts, Limiter: limiter, RatePerMinute: ratePerMinute, Perimeter: perimeter})
}

func NewHandler(conn grpc.ClientConnInterface) *Handler {
	return &Handler{client: v1.NewTimelineServiceClient(conn)}
}
func in(e *bffv1.Entry) *v1.Entry {
	if e == nil {
		return nil
	}
	return &v1.Entry{Id: e.Id, EntryRef: e.EntryRef, Title: e.Title, Date: e.Date, Time: e.Time, Timezone: e.Timezone, Category: e.Category, Location: e.Location, AmountMinor: e.AmountMinor, AmountPresent: e.AmountPresent, Currency: e.Currency, Note: e.Note, Url: e.Url, UrlTitle: e.UrlTitle, UrlNote: e.UrlNote, Revision: e.Revision, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt, DeletedAt: e.DeletedAt}
}
func out(e *v1.Entry) *bffv1.Entry {
	if e == nil {
		return nil
	}
	return &bffv1.Entry{EntryRef: e.EntryRef, Title: e.Title, Date: e.Date, Time: e.Time, Timezone: e.Timezone, Category: e.Category, Location: e.Location, AmountMinor: e.AmountMinor, AmountPresent: e.AmountPresent, Currency: e.Currency, Note: e.Note, Url: e.Url, UrlTitle: e.UrlTitle, UrlNote: e.UrlNote, Revision: e.Revision, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt, DeletedAt: e.DeletedAt}
}
func receipt(x *v1.MutationReceipt) *bffv1.MutationReceipt {
	if x == nil {
		return nil
	}
	return &bffv1.MutationReceipt{MutationId: x.MutationId, TargetKind: x.TargetKind, Operation: x.Operation, ResultRevision: x.ResultRevision, ResultState: x.ResultState, Replayed: x.Replayed, RecordedAt: x.RecordedAt, EntryRef: x.EntryRef, MediaRef: x.MediaRef, ResultSnapshot: x.ResultSnapshot}
}
func categoryOut(c *v1.Category) *bffv1.Category {
	if c == nil {
		return nil
	}
	return &bffv1.Category{CategoryRef: c.CategoryRef, Name: c.Name, Active: c.Active, IsDefault: c.IsDefault, IsOther: c.IsOther, Revision: c.Revision, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
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
func (h *Handler) ListCategories(ctx context.Context, r *connect.Request[bffv1.ListCategoriesRequest]) (*connect.Response[bffv1.ListCategoriesResponse], error) {
	x, err := h.client.ListCategories(ctx, &v1.ListCategoriesRequest{})
	if err != nil {
		return nil, upstream.ToConnect("timeline", err)
	}
	out := &bffv1.ListCategoriesResponse{}
	for _, c := range x.Categories {
		out.Categories = append(out.Categories, categoryOut(c))
	}
	return connect.NewResponse(out), nil
}
func (h *Handler) CreateCategory(ctx context.Context, r *connect.Request[bffv1.CreateCategoryRequest]) (*connect.Response[bffv1.CategoryResponse], error) {
	x, err := h.client.CreateCategory(ctx, &v1.CreateCategoryRequest{Name: r.Msg.Name, MutationId: r.Msg.MutationId})
	if err != nil {
		return nil, upstream.ToConnect("timeline", err)
	}
	return connect.NewResponse(&bffv1.CategoryResponse{Category: categoryOut(x.Category), Receipt: receipt(x.Receipt)}), nil
}
func (h *Handler) UpdateCategory(ctx context.Context, r *connect.Request[bffv1.UpdateCategoryRequest]) (*connect.Response[bffv1.CategoryResponse], error) {
	x, err := h.client.UpdateCategory(ctx, &v1.UpdateCategoryRequest{CategoryRef: r.Msg.CategoryRef, Name: r.Msg.Name, Active: r.Msg.Active, ExpectedRevision: r.Msg.ExpectedRevision, MutationId: r.Msg.MutationId})
	if err != nil {
		return nil, upstream.ToConnect("timeline", err)
	}
	return connect.NewResponse(&bffv1.CategoryResponse{Category: categoryOut(x.Category), Receipt: receipt(x.Receipt)}), nil
}
func (h *Handler) CreateEntry(ctx context.Context, r *connect.Request[bffv1.CreateEntryRequest]) (*connect.Response[bffv1.EntryResponse], error) {
	x, err := h.client.CreateEntry(ctx, &v1.CreateEntryRequest{Entry: in(r.Msg.Entry), MutationId: r.Msg.MutationId})
	if err != nil {
		return nil, upstream.ToConnect("timeline", err)
	}
	return connect.NewResponse(&bffv1.EntryResponse{Entry: out(x.Entry), Receipt: receipt(x.Receipt)}), nil
}
func (h *Handler) UpdateEntry(ctx context.Context, r *connect.Request[bffv1.UpdateEntryRequest]) (*connect.Response[bffv1.EntryResponse], error) {
	x, err := h.client.UpdateEntry(ctx, &v1.UpdateEntryRequest{Entry: in(r.Msg.Entry), ExpectedRevision: r.Msg.ExpectedRevision, MutationId: r.Msg.MutationId})
	if err != nil {
		return nil, upstream.ToConnect("timeline", err)
	}
	return connect.NewResponse(&bffv1.EntryResponse{Entry: out(x.Entry), Receipt: receipt(x.Receipt)}), nil
}
func (h *Handler) DeleteEntry(ctx context.Context, r *connect.Request[bffv1.DeleteEntryRequest]) (*connect.Response[bffv1.DeleteEntryResponse], error) {
	x, err := h.client.DeleteEntry(ctx, &v1.DeleteEntryRequest{Id: r.Msg.Id, EntryRef: r.Msg.EntryRef, ExpectedRevision: r.Msg.ExpectedRevision, MutationId: r.Msg.MutationId, Permanent: r.Msg.Permanent})
	if err != nil {
		return nil, upstream.ToConnect("timeline", err)
	}
	return connect.NewResponse(&bffv1.DeleteEntryResponse{Entry: out(x.Entry), Receipt: receipt(x.Receipt)}), nil
}
func (h *Handler) RestoreEntry(ctx context.Context, r *connect.Request[bffv1.RestoreEntryRequest]) (*connect.Response[bffv1.EntryResponse], error) {
	x, err := h.client.RestoreEntry(ctx, &v1.RestoreEntryRequest{Id: r.Msg.Id, EntryRef: r.Msg.EntryRef, ExpectedRevision: r.Msg.ExpectedRevision, MutationId: r.Msg.MutationId})
	if err != nil {
		return nil, upstream.ToConnect("timeline", err)
	}
	return connect.NewResponse(&bffv1.EntryResponse{Entry: out(x.Entry), Receipt: receipt(x.Receipt)}), nil
}
func (h *Handler) PurgeEntry(ctx context.Context, r *connect.Request[bffv1.PurgeEntryRequest]) (*connect.Response[bffv1.PurgeEntryResponse], error) {
	x, err := h.client.PurgeEntry(ctx, &v1.PurgeEntryRequest{Id: r.Msg.Id, EntryRef: r.Msg.EntryRef, ExpectedRevision: r.Msg.ExpectedRevision, MutationId: r.Msg.MutationId})
	if err != nil {
		return nil, upstream.ToConnect("timeline", err)
	}
	return connect.NewResponse(&bffv1.PurgeEntryResponse{Receipt: receipt(x.Receipt)}), nil
}
