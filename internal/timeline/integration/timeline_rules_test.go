package integration

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/timeline/application"
	"github.com/kongken/mono/internal/timeline/ent"
	"github.com/kongken/mono/internal/timeline/ent/category"
	"github.com/kongken/mono/internal/timeline/ent/mediaobject"
	"github.com/kongken/mono/internal/timeline/media"
	timelinev1 "github.com/kongken/mono/pkg/proto/timeline/v1"
)

// newEntOnMigrationDB builds Ent on the throwaway schema from newMigrationDB so
// the application rules run against the real PostgreSQL dialect and indexes.
func newEntOnMigrationDB(t *testing.T, m *migrationDB) *ent.Client {
	t.Helper()
	db := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, m.DB)))
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Schema.Create(m.ctx); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

// TestConcurrentFirstCategoryInitCreatesDefaultsOnce is the regression test for
// the idempotent per-user default-category initialization: the first concurrent
// requests must all succeed and leave exactly one default set.
func TestConcurrentFirstCategoryInitCreatesDefaultsOnce(t *testing.T) {
	m := newMigrationDB(t)
	db := newEntOnMigrationDB(t, m)
	svc := application.NewTimelineService(db)
	user := "timeline-category-race-" + uuid.NewString()[:8]
	ctx := rpcauth.WithCall(m.ctx, rpcauth.Call{UserID: user})

	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.ListCategories(ctx, &timelinev1.ListCategoriesRequest{}); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent first init failed: %v", err)
	}

	rows, err := db.Category.Query().Where(category.UserIDEQ(user)).All(m.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 22 {
		t.Fatalf("categories=%d, want 22", len(rows))
	}
	others := 0
	for _, row := range rows {
		if row.IsOther {
			others++
		}
	}
	if others != 1 {
		t.Fatalf(`"其他" rows=%d, want 1`, others)
	}
}

type downloadSignerBlobs struct{ signed int }

func (downloadSignerBlobs) Put(context.Context, string, io.ReadSeeker, int64, string) error {
	return nil
}
func (downloadSignerBlobs) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("bytes")), nil
}
func (downloadSignerBlobs) Delete(context.Context, string) error { return nil }
func (b *downloadSignerBlobs) SignDownload(_ context.Context, key string, ttl time.Duration) (string, error) {
	b.signed++
	return "https://objects.example.com/" + key + "?ttl=" + ttl.String(), nil
}

type downloadNoSignerBlobs struct{}

func (downloadNoSignerBlobs) Put(context.Context, string, io.ReadSeeker, int64, string) error {
	return nil
}
func (downloadNoSignerBlobs) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("bytes")), nil
}
func (downloadNoSignerBlobs) Delete(context.Context, string) error { return nil }

// TestAttachmentDownloadAddressIsOwnerScopedAndStopsAfterTrash covers the
// explicit download-address contract: a signed, short-lived address for the
// owner, refused for another account and after the record is trashed, and never
// issued by an object store without a download signer.
func TestAttachmentDownloadAddressIsOwnerScopedAndStopsAfterTrash(t *testing.T) {
	m := newMigrationDB(t)
	db := newEntOnMigrationDB(t, m)
	ctx := m.ctx
	owner := "timeline-download-owner"

	e, err := db.Entry.Create().
		SetPublicRef(entryRef).SetUserID(owner).SetTitle("x").
		SetOccurredDate("2024-01-15").SetTimezone("UTC").SetOccurredAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.MediaObject.Create().
		SetPublicRef(mediaRef).SetUserID(owner).SetEntryID(e.ID).SetEntryPublicRef(e.PublicRef).
		SetObjectKey("users/timeline/1.jpg").SetState(mediaobject.StateReady).
		SetFilename("photo.jpg").SetContentType("image/jpeg").SetSizeBytes(5).
		Save(ctx); err != nil {
		t.Fatal(err)
	}
	blobs := &downloadSignerBlobs{}
	store := &media.Store{DB: db, Blobs: blobs}

	got, err := media.DownloadURL(ctx, store, owner, mediaRef, 0)
	if err != nil {
		t.Fatalf("download address: %v", err)
	}
	if blobs.signed != 1 || !strings.Contains(got.URL, "users/timeline/1.jpg") {
		t.Fatalf("unexpected signed address %q (signed=%d)", got.URL, blobs.signed)
	}
	if remaining := time.Until(got.ExpiresAt); remaining <= 0 || remaining > media.DownloadURLTTL+time.Minute {
		t.Fatalf("expiry %s is outside the short-lived window", remaining)
	}
	// The caller cannot extend the server-side window.
	if got, err := media.DownloadURL(ctx, store, owner, mediaRef, time.Hour); err != nil {
		t.Fatal(err)
	} else if remaining := time.Until(got.ExpiresAt); remaining > media.DownloadURLTTL+time.Minute {
		t.Fatalf("caller extended the download window to %s", remaining)
	}
	if _, err := media.DownloadURL(ctx, store, "someone-else", mediaRef, 0); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("another account's attachment: err=%v, want not found", err)
	}
	if _, err := media.DownloadURL(ctx, store, owner, "med_bad", 0); !errors.Is(err, media.ErrInvalid) {
		t.Fatalf("malformed ref: err=%v, want invalid", err)
	}
	if _, err := media.DownloadURL(ctx, &media.Store{DB: db, Blobs: downloadNoSignerBlobs{}}, owner, mediaRef, 0); err == nil {
		t.Fatal("object store without a download signer issued an address")
	}

	if _, err := db.Entry.UpdateOneID(e.ID).SetDeletedAt(time.Now().UTC()).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := media.DownloadURL(ctx, store, owner, mediaRef, 0); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("trashed entry still issued an address: %v", err)
	}
}
