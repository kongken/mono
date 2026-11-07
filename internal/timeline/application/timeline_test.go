package application

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/timeline/ent"
	"github.com/kongken/mono/internal/timeline/ent/enttest"
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

func TestConcurrentExpectedRevisionAllowsOneMutation(t *testing.T) {
	dsn := os.Getenv("TRIPMAP_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TRIPMAP_TEST_POSTGRES_DSN is not configured")
	}
	db := enttest.Open(t, "pgx", dsn)
	defer db.Close()
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
}
