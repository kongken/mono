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
}

func (t *tool) schema() map[string]any                  { return inputSchema(t.newReq().ProtoReflect().Descriptor()) }
func (t *tool) allowedFor(p accesstoken.Principal) bool { return p.Allows(t.scope) }
func tools() []*tool {
	return []*tool{
		{"timeline_list", "List the authenticated user's timeline entries.", accesstoken.ScopeTimelineRead, true, false, false, func() proto.Message { return &timelinev1.ListEntriesRequest{} }, func(c context.Context, s timelinev1.TimelineServiceClient, r proto.Message) (proto.Message, error) {
			return s.ListEntries(c, r.(*timelinev1.ListEntriesRequest))
		}},
		{"timeline_search", "Search the authenticated user's timeline entries.", accesstoken.ScopeTimelineRead, true, false, false, func() proto.Message { return &timelinev1.ListEntriesRequest{} }, func(c context.Context, s timelinev1.TimelineServiceClient, r proto.Message) (proto.Message, error) {
			return s.ListEntries(c, r.(*timelinev1.ListEntriesRequest))
		}},
		{"timeline_create", "Create an entry; mutation_id makes retries idempotent.", accesstoken.ScopeTimelineWrite, false, false, false, func() proto.Message { return &timelinev1.CreateEntryRequest{} }, func(c context.Context, s timelinev1.TimelineServiceClient, r proto.Message) (proto.Message, error) {
			return s.CreateEntry(c, r.(*timelinev1.CreateEntryRequest))
		}},
		{"timeline_update", "Update an entry with entry_ref, mutation_id and expected_revision.", accesstoken.ScopeTimelineWrite, false, false, true, func() proto.Message { return &timelinev1.UpdateEntryRequest{} }, func(c context.Context, s timelinev1.TimelineServiceClient, r proto.Message) (proto.Message, error) {
			return s.UpdateEntry(c, r.(*timelinev1.UpdateEntryRequest))
		}},
		{"timeline_trash", "Move an entry to trash with expected_revision.", accesstoken.ScopeTimelineWrite, false, true, true, func() proto.Message { return &timelinev1.DeleteEntryRequest{} }, func(c context.Context, s timelinev1.TimelineServiceClient, r proto.Message) (proto.Message, error) {
			return s.DeleteEntry(c, r.(*timelinev1.DeleteEntryRequest))
		}},
		{"timeline_restore", "Restore an entry with expected_revision.", accesstoken.ScopeTimelineWrite, false, false, true, func() proto.Message { return &timelinev1.RestoreEntryRequest{} }, func(c context.Context, s timelinev1.TimelineServiceClient, r proto.Message) (proto.Message, error) {
			return s.RestoreEntry(c, r.(*timelinev1.RestoreEntryRequest))
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
	out, err := t.call(ctx, s.cfg.Admin, req)
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
