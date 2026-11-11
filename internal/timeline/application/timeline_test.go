package application

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/timeline/ent"
	timelinev1 "github.com/kongken/mono/pkg/proto/timeline/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestValidateEntry(t *testing.T) {
	base := &timelinev1.Entry{Title: "Dinner", Date: "2026-11-07", Timezone: "Asia/Shanghai"}
	if err := validate(base, false); err != nil {
		t.Fatalf("valid entry rejected: %v", err)
	}
	for name, in := range map[string]*timelinev1.Entry{
		"missing title": {Date: base.Date, Timezone: base.Timezone},
		"bad date":      {Title: base.Title, Date: "2026-02-30", Timezone: base.Timezone},
		"bad timezone":  {Title: base.Title, Date: base.Date, Timezone: "Mars/Base"},
		"bad scheme":    {Title: base.Title, Date: base.Date, Timezone: base.Timezone, Url: "javascript:alert(1)"},
		"private host":  {Title: base.Title, Date: base.Date, Timezone: base.Timezone, Url: "http://127.0.0.1/admin"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validate(in, false); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestMutationContracts(t *testing.T) {
	for _, id := range []string{"short", "中文123456", "bad value", string(make([]byte, 129))} {
		if validateMutationID(id) == nil {
			t.Errorf("mutation id %q should be rejected", id)
		}
	}
	if validateMutationID("retry:2026-11-07") != nil {
		t.Fatal("valid mutation id rejected")
	}
	if requireRevision(0) == nil || requireRevision(-1) == nil {
		t.Fatal("zero and negative revisions must be rejected")
	}
	if requireRevision(1) != nil {
		t.Fatal("positive revision rejected")
	}
}

func TestPublicResponsesAndSnapshotsUseOpaqueRef(t *testing.T) {
	row := &ent.Entry{ID: "internal-uuid", PublicRef: "ent_0123456789abcdef0123456789", Title: "Dinner", OccurredDate: "2026-11-07", Timezone: "UTC"}
	out := mapEntry(row)
	if out.Id != "" || out.EntryRef != row.PublicRef {
		t.Fatalf("response leaked database id or lost ref: %#v", out)
	}
	snapshot, err := snapshotEntry(row)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot) == 0 || strings.Contains(string(snapshot), "internal-uuid") {
		t.Fatal("snapshot contains internal database id")
	}
}

func TestListCursorRoundTripAndValidation(t *testing.T) {
	at := time.Date(2026, 11, 7, 4, 30, 0, 123000000, time.UTC)
	token := encodeListCursor(at, "internal-uuid")
	got, err := decodeListCursor(token)
	if err != nil {
		t.Fatal(err)
	}
	if !got.OccurredAt.Equal(at) || got.ID != "internal-uuid" {
		t.Fatalf("cursor changed: %#v", got)
	}
	for _, invalid := range []string{"!", "", encodeListCursor(time.Time{}, "id")} {
		if _, err := decodeListCursor(invalid); err == nil {
			t.Fatalf("cursor %q unexpectedly accepted", invalid)
		}
	}
}

func TestConcurrentExpectedRevisionAllowsOneMutation(t *testing.T) {
	dsn := os.Getenv("TRIPMAP_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TRIPMAP_TEST_POSTGRES_DSN is not configured")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "timeline_concurrency_" + uuid.NewString()[:8]
	if _, err := admin.ExecContext(context.Background(), `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`) })
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	scoped := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = scoped.Close() })
	db := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, scoped)))
	if err := db.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewTimelineService(db)
	ctx := rpcauth.WithCall(context.Background(), rpcauth.Call{UserID: "timeline-concurrency-test"})
	created, err := svc.CreateEntry(ctx, &timelinev1.CreateEntryRequest{MutationId: "create-concurrency", Entry: &timelinev1.Entry{Title: "race", Date: "2026-11-07", Timezone: "UTC"}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, callErr := svc.UpdateEntry(ctx, &timelinev1.UpdateEntryRequest{MutationId: fmt.Sprintf("update-race-%d", i), ExpectedRevision: created.Entry.Revision, Entry: &timelinev1.Entry{EntryRef: created.Entry.EntryRef, Title: fmt.Sprintf("winner-%d", i), Date: "2026-11-07", Timezone: "UTC"}})
			results <- callErr
		}(i)
	}
	wg.Wait()
	close(results)
	var successes, conflicts int
	for callErr := range results {
		if callErr == nil {
			successes++
		} else if status.Code(callErr) == codes.Aborted {
			conflicts++
		} else {
			t.Errorf("unexpected concurrent update error: %v", callErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected one success and one conflict, got successes=%d conflicts=%d", successes, conflicts)
	}

	var createWG sync.WaitGroup
	createResults := make(chan *timelinev1.EntryResponse, 2)
	createErrors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		createWG.Add(1)
		go func() {
			defer createWG.Done()
			response, callErr := svc.CreateEntry(ctx, &timelinev1.CreateEntryRequest{MutationId: "create-replay-race", Entry: &timelinev1.Entry{Title: "same request", Date: "2026-11-07", Timezone: "UTC"}})
			createResults <- response
			createErrors <- callErr
		}()
	}
	createWG.Wait()
	close(createResults)
	close(createErrors)
	var createSuccesses, createReplays int
	for callErr := range createErrors {
		if callErr != nil {
			t.Errorf("concurrent create replay failed: %v", callErr)
		}
	}
	for response := range createResults {
		if response == nil || response.Entry == nil || response.Receipt == nil {
			t.Fatal("concurrent create returned incomplete response")
		}
		if response.Receipt.Replayed {
			createReplays++
		} else {
			createSuccesses++
		}
	}
	if createSuccesses != 1 || createReplays != 1 {
		t.Fatalf("expected one create and one replay, got creates=%d replays=%d", createSuccesses, createReplays)
	}
}
