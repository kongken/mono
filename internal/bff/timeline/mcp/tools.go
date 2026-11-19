package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/kongken/mono/internal/bff/accesstoken"
	timelinev1 "github.com/kongken/mono/pkg/proto/timeline/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"strings"
)

type tool struct {
	name, description, scope               string
	readOnly, destructive, requireRevision bool
	newReq                                 func() proto.Message
	call                                   func(context.Context, timelinev1.TimelineServiceClient, proto.Message) (proto.Message, error)
	mediaCall                              func(context.Context, timelinev1.MediaServiceClient, proto.Message) (proto.Message, error)
}

func (t *tool) schema() map[string]any                  { return inputSchema(t.newReq().ProtoReflect().Descriptor()) }
func (t *tool) allowedFor(p accesstoken.Principal) bool { return p.Allows(t.scope) }
func tools() []*tool {
	return []*tool{
		{"timeline_list", "List the authenticated user's timeline entries.", accesstoken.ScopeTimelineRead, true, false, false, func() proto.Message { return &timelinev1.ListEntriesRequest{} }, func(c context.Context, s timelinev1.TimelineServiceClient, r proto.Message) (proto.Message, error) {
			return s.ListEntries(c, r.(*timelinev1.ListEntriesRequest))
		}, nil},
		{"timeline_search", "Search the authenticated user's timeline entries.", accesstoken.ScopeTimelineRead, true, false, false, func() proto.Message { return &timelinev1.ListEntriesRequest{} }, func(c context.Context, s timelinev1.TimelineServiceClient, r proto.Message) (proto.Message, error) {
			return s.ListEntries(c, r.(*timelinev1.ListEntriesRequest))
		}, nil},
		{"timeline_create", "Create an entry; mutation_id makes retries idempotent.", accesstoken.ScopeTimelineWrite, false, false, false, func() proto.Message { return &timelinev1.CreateEntryRequest{} }, func(c context.Context, s timelinev1.TimelineServiceClient, r proto.Message) (proto.Message, error) {
			return s.CreateEntry(c, r.(*timelinev1.CreateEntryRequest))
		}, nil},
		{"timeline_update", "Update an entry with entry_ref, mutation_id and expected_revision.", accesstoken.ScopeTimelineWrite, false, false, true, func() proto.Message { return &timelinev1.UpdateEntryRequest{} }, func(c context.Context, s timelinev1.TimelineServiceClient, r proto.Message) (proto.Message, error) {
			return s.UpdateEntry(c, r.(*timelinev1.UpdateEntryRequest))
		}, nil},
		{"timeline_trash", "Move an entry to trash with expected_revision.", accesstoken.ScopeTimelineWrite, false, true, true, func() proto.Message { return &timelinev1.DeleteEntryRequest{} }, func(c context.Context, s timelinev1.TimelineServiceClient, r proto.Message) (proto.Message, error) {
			return s.DeleteEntry(c, r.(*timelinev1.DeleteEntryRequest))
		}, nil},
		{"timeline_restore", "Restore an entry with expected_revision.", accesstoken.ScopeTimelineWrite, false, false, true, func() proto.Message { return &timelinev1.RestoreEntryRequest{} }, func(c context.Context, s timelinev1.TimelineServiceClient, r proto.Message) (proto.Message, error) {
			return s.RestoreEntry(c, r.(*timelinev1.RestoreEntryRequest))
		}, nil},
		{"timeline_list_trashed_life_logs", "List the authenticated user's entries currently in trash.", accesstoken.ScopeTimelineRead, true, false, false, func() proto.Message { return &timelinev1.ListEntriesRequest{IncludeDeleted: true} }, func(c context.Context, s timelinev1.TimelineServiceClient, r proto.Message) (proto.Message, error) {
			req := r.(*timelinev1.ListEntriesRequest)
			req.IncludeDeleted = true
			return s.ListEntries(c, req)
		}, nil},
		{"timeline_list_categories", "List the authenticated user's active and inactive categories.", accesstoken.ScopeTimelineRead, true, false, false, func() proto.Message { return &timelinev1.ListCategoriesRequest{} }, func(c context.Context, s timelinev1.TimelineServiceClient, r proto.Message) (proto.Message, error) {
			return s.ListCategories(c, r.(*timelinev1.ListCategoriesRequest))
		}, nil},
		{"timeline_create_category", "Create a custom category with an idempotent mutation_id.", accesstoken.ScopeTimelineWrite, false, false, false, func() proto.Message { return &timelinev1.CreateCategoryRequest{} }, func(c context.Context, s timelinev1.TimelineServiceClient, r proto.Message) (proto.Message, error) {
			return s.CreateCategory(c, r.(*timelinev1.CreateCategoryRequest))
		}, nil},
		{"timeline_update_category", "Rename or enable/disable a category with expected_revision.", accesstoken.ScopeTimelineWrite, false, false, true, func() proto.Message { return &timelinev1.UpdateCategoryRequest{} }, func(c context.Context, s timelinev1.TimelineServiceClient, r proto.Message) (proto.Message, error) {
			return s.UpdateCategory(c, r.(*timelinev1.UpdateCategoryRequest))
		}, nil},
		{"timeline_list_attachments", "List attachment metadata for one entry; trashed entries return no media.", accesstoken.ScopeTimelineRead, true, false, false, func() proto.Message { return &timelinev1.ListMediaRequest{} }, nil, func(c context.Context, s timelinev1.MediaServiceClient, r proto.Message) (proto.Message, error) {
			return s.ListMedia(c, r.(*timelinev1.ListMediaRequest))
		}},
		{"timeline_get_attachment_download", "Issue a short-lived private download address for one attachment after listing it. The address expires within minutes, is not a permanent public URL, and is refused once the entry is trashed.", accesstoken.ScopeTimelineRead, true, false, false, func() proto.Message { return &timelinev1.GetMediaDownloadUrlRequest{} }, nil, func(c context.Context, s timelinev1.MediaServiceClient, r proto.Message) (proto.Message, error) {
			return s.GetMediaDownloadUrl(c, r.(*timelinev1.GetMediaDownloadUrlRequest))
		}},
	}
}
func (s *Server) run(ctx context.Context, t *tool, args json.RawMessage) (string, *toolError) {
	raw := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(args))) > 0 {
		if err := json.Unmarshal(args, &raw); err != nil {
			return "", badArguments(err)
		}
	}
	b, _ := json.Marshal(raw)
	req := t.newReq()
	if err := (protojson.UnmarshalOptions{}).Unmarshal(b, req); err != nil {
		return "", badArguments(err)
	}
	if t.requireRevision {
		f := req.ProtoReflect().Descriptor().Fields().ByName("expected_revision")
		if f == nil || req.ProtoReflect().Get(f).Int() <= 0 {
			return "", &toolError{Code: "INVALID_ARGUMENT", Message: "expectedRevision is required"}
		}
	}
	var out proto.Message
	var err error
	if t.mediaCall != nil {
		if s.cfg.Media == nil {
			return "", &toolError{Code: "UNAVAILABLE", Message: "media service is unavailable"}
		}
		out, err = t.mediaCall(ctx, s.cfg.Media, req)
	} else {
		out, err = t.call(ctx, s.cfg.Admin, req)
	}
	if err != nil {
		return "", fromRPCError(err)
	}
	data, e := protojson.Marshal(out)
	if e != nil {
		return "", &toolError{Code: "INTERNAL", Message: "could not encode result"}
	}
	return string(data), nil
}

var _ = errors.New
