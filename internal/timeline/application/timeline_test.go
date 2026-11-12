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

func TestValidateAmountPresenceAndCurrency(t *testing.T) {
	base := func() *timelinev1.Entry {
		return &timelinev1.Entry{Title: "Lunch", Date: "2024-01-15", Timezone: "UTC"}
	}
	amount := base()
	amount.AmountPresent, amount.AmountMinor, amount.Currency = true, 1250, "CNY"
	if err := validate(amount, false); err != nil {
		t.Fatalf("valid amount rejected: %v", err)
	}
	orphan := base()
	orphan.Currency = "CNY"
	if err := validate(orphan, false); err == nil {
		t.Fatal("currency without an amount was accepted")
	}
	missingCurrency := base()
	missingCurrency.AmountPresent, missingCurrency.AmountMinor = true, 100
	if err := validate(missingCurrency, false); err == nil {
		t.Fatal("amount without a currency was accepted")
	}
	malformed := base()
	malformed.AmountPresent, malformed.AmountMinor, malformed.Currency = true, 100, "cny"
	if err := validate(malformed, false); err == nil {
		t.Fatal("malformed currency was accepted")
	}
}

func TestLocalOccurrenceRejectsFutureAndDSTWallTimes(t *testing.T) {
	future := time.Now().In(time.UTC).AddDate(0, 0, 2).Format("2006-01-02")
	if _, err := localOccurrence(&timelinev1.Entry{Date: future, Timezone: "UTC"}); err == nil {
		t.Fatal("future date accepted")
	}
	if _, err := localOccurrence(&timelinev1.Entry{Date: future, Time: "00:00", Timezone: "UTC"}); err == nil {
		t.Fatal("future time accepted")
	}
	// 2026-03-08 02:30 does not exist in New York: the spring-forward gap.
	if _, err := localOccurrence(&timelinev1.Entry{Date: "2026-03-08", Time: "02:30", Timezone: "America/New_York"}); err == nil {
		t.Fatal("DST gap wall time accepted")
	}
	// 2026-11-01 01:30 happens twice in New York: the fall-back repeat.
	if _, err := localOccurrence(&timelinev1.Entry{Date: "2026-11-01", Time: "01:30", Timezone: "America/New_York"}); err == nil {
		t.Fatal("ambiguous DST wall time accepted")
	}
	// A date-only record has no clock value to disambiguate, so it is allowed.
	if _, err := localOccurrence(&timelinev1.Entry{Date: "2026-11-01", Timezone: "America/New_York"}); err != nil {
		t.Fatalf("date-only record on a DST day rejected: %v", err)
	}
	if _, err := localOccurrence(&timelinev1.Entry{Date: "2024-01-15", Time: "09:30", Timezone: "Asia/Shanghai"}); err != nil {
		t.Fatalf("ordinary past local time rejected: %v", err)
	}
}

func TestListCursorIsBoundToItsFilters(t *testing.T) {
	base := &timelinev1.ListEntriesRequest{Query: "dinner", Category: "餐饮"}
	other := &timelinev1.ListEntriesRequest{Query: "dinner", Category: "交通"}
	if listFilter(base) == listFilter(other) {
		t.Fatal("filter fingerprint ignores the category")
	}
	token := encodeListCursor("2026-11-07", "12:30", time.Now().UTC(), "internal-id", listFilter(base))
	cursor, err := decodeListCursor(token)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.Filter != listFilter(base) {
		t.Fatal("cursor lost its filter binding")
	}
	if cursor.Filter == listFilter(other) {
		t.Fatal("cursor filter binding collided across filters")
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
	token := encodeListCursor("2026-11-07", "12:30", at, "internal-uuid")
	got, err := decodeListCursor(token)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CreatedAt.Equal(at) || got.OccurredDate != "2026-11-07" || got.OccurredTime != "12:30" || got.ID != "internal-uuid" {
		t.Fatalf("cursor changed: %#v", got)
	}
	for _, invalid := range []string{"!", "", encodeListCursor("", "", time.Time{}, "id")} {
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
