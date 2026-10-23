package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kongken/mono/internal/bff/accesstoken"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// tool is one MCP tool. Almost every tool decodes its arguments into an internal
// metro.v1 request and calls the AdminService method of the same name, the very
// method the web admin uses, so validation, concurrency control and audit are
// identical on both paths.
type tool struct {
	name        string
	description string
	scope       string
	// readOnly and destructive become MCP tool annotations.
	readOnly    bool
	destructive bool
	// heavy tools get the long deadline.
	heavy bool

	// newReq returns an empty request to decode the arguments into.
	newReq func() proto.Message
	// call performs the RPC.
	call func(ctx context.Context, c metrov1.AdminServiceClient, req proto.Message) (proto.Message, error)

	// requireRevision rejects the call unless expected_revision is a positive
	// number: an AI client must read an entity before it changes or deletes it.
	requireRevision bool
	// dryRunDefaultsTrue makes an omitted dry_run mean true.
	dryRunDefaultsTrue bool

	// custom replaces the generic path (metro_get_entity).
	customSchema map[string]any
	custom       func(ctx context.Context, c metrov1.AdminServiceClient, args json.RawMessage) (any, *toolError)
}

func (t *tool) schema() map[string]any {
	if t.customSchema != nil {
		return t.customSchema
	}
	s := inputSchema(t.newReq().ProtoReflect().Descriptor())
	if t.dryRunDefaultsTrue {
		if props, ok := s["properties"].(map[string]any); ok {
			if p, ok := props["dryRun"].(map[string]any); ok {
				p["default"] = true
				p["description"] = "Defaults to true: validate and preview only. Pass false explicitly to write."
			}
		}
	}
	return s
}

func (t *tool) allowedFor(p accesstoken.Principal) bool { return p.Allows(t.scope) }

const (
	notRecoverable = " Not recoverable. Fails with FAILED_PRECONDITION when something still references it. Pass the expected_revision you read from the matching list or get tool."
	updateNote     = " This replaces every editable field, so send the complete record (read it first), plus the id and expectedRevision you read. A stale revision fails with FAILED_PRECONDITION and the current revision."
	listNote       = " Returns at most 200 per page (default 50); pass nextPageToken as pageToken for the next page."
)

const (
	cityRules  = "City: slug (lowercase letters, digits, hyphens, at most 32 characters), name, headerImages (https URLs, at most 8), mapSvgUrl (https URL of an SVG shown as an image), sortOrder."
	stockRules = "Rolling stock: code (unique, at most 32 characters), optional lengthM, widthM, capacity."
	lineRules  = "Line: code (unique, [a-z0-9][a-z0-9-]{1,31}, cannot be changed after creation), citySlug, name, number (0 = unnumbered, sorts last), status (LINE_STATUS_PLANNED, LINE_STATUS_UNDER_CONSTRUCTION, LINE_STATUS_OPERATING, LINE_STATUS_SUSPENDED, LINE_STATUS_RETIRED), rollingStockCode, cars, carsAddition, stations, lengthKm (at most two decimals), color (#RRGGBB), commencedOn and extendedOn (YYYY-MM-DD or empty), isLoop."
	topicRules = "Topic: slug, name, rollingStockCodes and/or cars; at least one of the two lists must be non-empty. A line matches when its rolling stock is listed and/or its cars or carsAddition is listed."
)

// tools returns the whole catalogue in a stable order.
func tools() []*tool {
	list := []*tool{
		{
			name: "metro_list_cities", scope: accesstoken.ScopeRead, readOnly: true,
			description: "List cities as administrators see them, with id and revision. Optional query matches slug or name." + listNote,
			newReq:      func() proto.Message { return &metrov1.ListAdminCitiesRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.ListAdminCities(ctx, r.(*metrov1.ListAdminCitiesRequest))
			},
		},
		{
			name: "metro_list_lines", scope: accesstoken.ScopeRead, readOnly: true,
			description: "List lines of every status with id and revision. Filter by query (code or name), citySlug and statuses." + listNote,
			newReq:      func() proto.Message { return &metrov1.ListAdminLinesRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.ListAdminLines(ctx, r.(*metrov1.ListAdminLinesRequest))
			},
		},
		{
			name: "metro_list_rolling_stocks", scope: accesstoken.ScopeRead, readOnly: true,
			description: "List rolling stock types with id and revision. Optional query matches the code." + listNote,
			newReq:      func() proto.Message { return &metrov1.ListAdminRollingStocksRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.ListAdminRollingStocks(ctx, r.(*metrov1.ListAdminRollingStocksRequest))
			},
		},
		{
			name: "metro_list_topics", scope: accesstoken.ScopeRead, readOnly: true,
			description: "List rolling stock topics with id and revision. Optional query matches slug or name." + listNote,
			newReq:      func() proto.Message { return &metrov1.ListAdminRollingStockTopicsRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.ListAdminRollingStockTopics(ctx, r.(*metrov1.ListAdminRollingStockTopicsRequest))
			},
		},
		getEntityTool(),
		{
			name: "metro_export_catalog", scope: accesstoken.ScopeRead, readOnly: true, heavy: true,
			description: "Export the whole catalogue as a metro-catalog/1 JSON document (catalogJson), the same format metro_import_catalog accepts. Use it as a backup or to edit many records at once.",
			newReq:      func() proto.Message { return &metrov1.ExportCatalogRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.ExportCatalog(ctx, r.(*metrov1.ExportCatalogRequest))
			},
		},
		{
			name: "metro_list_audit_logs", scope: accesstoken.ScopeRead, readOnly: true,
			description: "List the audit trail of administrative writes, newest first. Filter by entityType (city, rolling_stock, line, topic), entityId, actorUserId, channel (web or mcp) and a from/to time range. Each entry has before/after JSON snapshots." + listNote,
			newReq:      func() proto.Message { return &metrov1.ListAuditLogsRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.ListAuditLogs(ctx, r.(*metrov1.ListAuditLogsRequest))
			},
		},
		{
			name: "metro_import_catalog", scope: accesstoken.ScopeWrite, dryRunDefaultsTrue: true, heavy: true,
			description: "Import a metro-catalog/1 JSON document (catalogJson, a string) by natural key: it creates and updates records, never deletes, and is all-or-nothing. dryRun defaults to TRUE, which only validates and returns a preview with per-record errors; pass dryRun=false explicitly to write. Always run the dry run first and show the user the counts. Documents are limited to 1 MiB and 2000 records.",
			newReq:      func() proto.Message { return &metrov1.ImportCatalogRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.ImportCatalog(ctx, r.(*metrov1.ImportCatalogRequest))
			},
		},
	}
	list = append(list,
		&tool{
			name: "metro_create_city", scope: accesstoken.ScopeWrite,
			description: "Create a city. " + cityRules,
			newReq:      func() proto.Message { return &metrov1.CreateCityRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.CreateCity(ctx, r.(*metrov1.CreateCityRequest))
			},
		},
		&tool{
			name: "metro_update_city", scope: accesstoken.ScopeWrite, requireRevision: true,
			description: "Update a city." + updateNote + " " + cityRules,
			newReq:      func() proto.Message { return &metrov1.UpdateCityRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.UpdateCity(ctx, r.(*metrov1.UpdateCityRequest))
			},
		},
		&tool{
			name: "metro_delete_city", scope: accesstoken.ScopeWrite, requireRevision: true, destructive: true,
			description: "Delete a city." + notRecoverable,
			newReq:      func() proto.Message { return &metrov1.DeleteCityRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.DeleteCity(ctx, r.(*metrov1.DeleteCityRequest))
			},
		},
		&tool{
			name: "metro_create_rolling_stock", scope: accesstoken.ScopeWrite,
			description: "Create a rolling stock type. " + stockRules,
			newReq:      func() proto.Message { return &metrov1.CreateRollingStockRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.CreateRollingStock(ctx, r.(*metrov1.CreateRollingStockRequest))
			},
		},
		&tool{
			name: "metro_update_rolling_stock", scope: accesstoken.ScopeWrite, requireRevision: true,
			description: "Update a rolling stock type." + updateNote + " " + stockRules,
			newReq:      func() proto.Message { return &metrov1.UpdateRollingStockRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.UpdateRollingStock(ctx, r.(*metrov1.UpdateRollingStockRequest))
			},
		},
		&tool{
			name: "metro_delete_rolling_stock", scope: accesstoken.ScopeWrite, requireRevision: true, destructive: true,
			description: "Delete a rolling stock type." + notRecoverable,
			newReq:      func() proto.Message { return &metrov1.DeleteRollingStockRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.DeleteRollingStock(ctx, r.(*metrov1.DeleteRollingStockRequest))
			},
		},
		&tool{
			name: "metro_create_line", scope: accesstoken.ScopeWrite,
			description: "Create a line. " + lineRules,
			newReq:      func() proto.Message { return &metrov1.CreateLineRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.CreateLine(ctx, r.(*metrov1.CreateLineRequest))
			},
		},
		&tool{
			name: "metro_update_line", scope: accesstoken.ScopeWrite, requireRevision: true,
			description: "Update a line." + updateNote + " " + lineRules,
			newReq:      func() proto.Message { return &metrov1.UpdateLineRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.UpdateLine(ctx, r.(*metrov1.UpdateLineRequest))
			},
		},
		&tool{
			name: "metro_delete_line", scope: accesstoken.ScopeWrite, requireRevision: true, destructive: true,
			description: "Delete a line." + notRecoverable + " A line that riders have recorded cannot be deleted; set its status to LINE_STATUS_RETIRED instead.",
			newReq:      func() proto.Message { return &metrov1.DeleteLineRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.DeleteLine(ctx, r.(*metrov1.DeleteLineRequest))
			},
		},
		&tool{
			name: "metro_create_topic", scope: accesstoken.ScopeWrite,
			description: "Create a rolling stock topic. " + topicRules,
			newReq:      func() proto.Message { return &metrov1.CreateRollingStockTopicRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.CreateRollingStockTopic(ctx, r.(*metrov1.CreateRollingStockTopicRequest))
			},
		},
		&tool{
			name: "metro_update_topic", scope: accesstoken.ScopeWrite, requireRevision: true,
			description: "Update a rolling stock topic." + updateNote + " " + topicRules,
			newReq:      func() proto.Message { return &metrov1.UpdateRollingStockTopicRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.UpdateRollingStockTopic(ctx, r.(*metrov1.UpdateRollingStockTopicRequest))
			},
		},
		&tool{
			name: "metro_delete_topic", scope: accesstoken.ScopeWrite, requireRevision: true, destructive: true,
			description: "Delete a rolling stock topic. Not recoverable. Pass the expected_revision you read from metro_list_topics.",
			newReq:      func() proto.Message { return &metrov1.DeleteRollingStockTopicRequest{} },
			call: func(ctx context.Context, c metrov1.AdminServiceClient, r proto.Message) (proto.Message, error) {
				return c.DeleteRollingStockTopic(ctx, r.(*metrov1.DeleteRollingStockTopicRequest))
			},
		},
	)
	return list
}

// getEntityTool reads one record by its natural key. AdminService has no
// single-record read, so it asks the list method for the key and keeps the exact
// match; the key is a slug or code, which the list's substring query finds.
func getEntityTool() *tool {
	entities := []string{"city", "rolling_stock", "line", "topic"}
	return &tool{
		name: "metro_get_entity", scope: accesstoken.ScopeRead, readOnly: true,
		description: "Read one record by its natural key (city slug, rolling stock code, line code or topic slug), returning its id and current revision. Call it before updating or deleting.",
		customSchema: map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"entityType", "key"},
			"properties": map[string]any{
				"entityType": map[string]any{"type": "string", "enum": entities},
				"key":        map[string]any{"type": "string", "description": "City slug, rolling stock code, line code or topic slug."},
			},
		},
		custom: func(ctx context.Context, c metrov1.AdminServiceClient, args json.RawMessage) (any, *toolError) {
			var in struct {
				EntityType string `json:"entityType"`
				Key        string `json:"key"`
			}
			if err := decodeStrict(args, &in); err != nil {
				return nil, badArguments(err)
			}
			if in.Key == "" {
				return nil, badArguments(fmt.Errorf("key is required"))
			}
			const page = 200
			var found proto.Message
			switch in.EntityType {
			case "city":
				res, err := c.ListAdminCities(ctx, &metrov1.ListAdminCitiesRequest{Query: in.Key, PageSize: page})
				if err != nil {
					return nil, fromRPCError(err)
				}
				for _, e := range res.GetCities() {
					if e.GetCity().GetSlug() == in.Key {
						found = e
					}
				}
			case "rolling_stock":
				res, err := c.ListAdminRollingStocks(ctx, &metrov1.ListAdminRollingStocksRequest{Query: in.Key, PageSize: page})
				if err != nil {
					return nil, fromRPCError(err)
				}
				for _, e := range res.GetRollingStocks() {
					if e.GetRollingStock().GetCode() == in.Key {
						found = e
					}
				}
			case "line":
				res, err := c.ListAdminLines(ctx, &metrov1.ListAdminLinesRequest{Query: in.Key, PageSize: page})
				if err != nil {
					return nil, fromRPCError(err)
				}
				for _, e := range res.GetLines() {
					if e.GetLine().GetCode() == in.Key {
						found = e
					}
				}
			case "topic":
				res, err := c.ListAdminRollingStockTopics(ctx, &metrov1.ListAdminRollingStockTopicsRequest{Query: in.Key, PageSize: page})
				if err != nil {
					return nil, fromRPCError(err)
				}
				for _, e := range res.GetTopics() {
					if e.GetTopic().GetSlug() == in.Key {
						found = e
					}
				}
			default:
				return nil, badArguments(fmt.Errorf("entityType must be one of city, rolling_stock, line, topic"))
			}
			if found == nil {
				return nil, &toolError{Code: "NOT_FOUND", Message: in.EntityType + " " + in.Key + " does not exist"}
			}
			return found, nil
		},
	}
}

// field returns the named field of m, or nil.
func field(m proto.Message, name protoreflect.Name) protoreflect.FieldDescriptor {
	return m.ProtoReflect().Descriptor().Fields().ByName(name)
}
