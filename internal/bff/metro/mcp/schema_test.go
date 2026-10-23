package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kongken/mono/internal/bff/accesstoken"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
)

func TestSchemaFollowsTheRequestMessage(t *testing.T) {
	s := inputSchema((&metrov1.UpdateLineRequest{}).ProtoReflect().Descriptor())
	b, _ := json.Marshal(s)
	got := string(b)
	for _, want := range []string{
		`"expectedRevision":{"type":"integer"}`,
		`"citySlug":{"type":"string"}`,
		`"isLoop":{"type":"boolean"}`,
		`"lengthKm":{"type":"number"}`,
		`"LINE_STATUS_OPERATING"`,
		`"additionalProperties":false`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("schema lacks %s:\n%s", want, got)
		}
	}
	audit := inputSchema((&metrov1.ListAuditLogsRequest{}).ProtoReflect().Descriptor())
	props := audit["properties"].(map[string]any)
	if props["from"].(map[string]any)["format"] != "date-time" {
		t.Errorf("timestamp field: %v", props["from"])
	}
}

func TestToolCatalogue(t *testing.T) {
	seen := map[string]bool{}
	var read, write int
	for _, tl := range tools() {
		if seen[tl.name] || !strings.HasPrefix(tl.name, "metro_") || tl.description == "" {
			t.Errorf("tool %q is duplicated, misnamed or undocumented", tl.name)
		}
		seen[tl.name] = true
		if tl.schema()["type"] != "object" {
			t.Errorf("%s schema", tl.name)
		}
		switch tl.scope {
		case accesstoken.ScopeRead:
			read++
			if !tl.readOnly || tl.destructive {
				t.Errorf("%s: a read tool must be read-only", tl.name)
			}
		case accesstoken.ScopeWrite:
			write++
			if tl.readOnly {
				t.Errorf("%s: a write tool is marked read-only", tl.name)
			}
			if strings.HasPrefix(tl.name, "metro_update_") || strings.HasPrefix(tl.name, "metro_delete_") {
				if !tl.requireRevision {
					t.Errorf("%s must require expectedRevision", tl.name)
				}
			}
			if strings.HasPrefix(tl.name, "metro_delete_") && !tl.destructive {
				t.Errorf("%s must be marked destructive", tl.name)
			}
		default:
			t.Errorf("%s has no scope", tl.name)
		}
	}
	if read != 7 || write != 13 {
		t.Fatalf("read %d write %d", read, write)
	}
	// The dry-run default is part of the published schema.
	for _, tl := range tools() {
		if tl.name == "metro_import_catalog" {
			p := tl.schema()["properties"].(map[string]any)["dryRun"].(map[string]any)
			if p["default"] != true {
				t.Errorf("dryRun default not published: %v", p)
			}
		}
	}
}
