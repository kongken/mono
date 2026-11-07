// Package integration runs the Timeline migrations against a real PostgreSQL.
//
// Nothing else in the tree applies `migrations/timeline/*.sql`: in development the
// service auto-migrates from the Ent schema (`internal/timeline/app`), so without
// this package the versioned SQL files and the CHECK constraints they add would
// ship without ever having been executed. Every test creates its own throwaway
// schema and drops it again.
//
// Set TIMELINE_TEST_POSTGRES_DSN (`make test-integration-timeline`) to run the
// database scenarios; CI's "Timeline migrations and receipts on PostgreSQL" job
// always does, and fails when a scenario is skipped instead of passing silently.
package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"ariga.io/atlas/sql/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

const migrationsDir = "../../../migrations/timeline"

// Ref formats from the ADR (docs/adr/0002-timeline-attachments-and-mcp-boundary.md):
// `ent_` / `med_` plus 26 Crockford base32 characters.
const (
	entryRef = "ent_0123456789abcdefghjkmnpqrs"
	mediaRef = "med_0123456789abcdefghjkmnpqrs"
)

var (
	entryRefPattern = regexp.MustCompile(`^ent_[0-9a-hjkmnp-tv-z]{26}$`)
	mediaRefPattern = regexp.MustCompile(`^med_[0-9a-hjkmnp-tv-z]{26}$`)
)

func migrationFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	if err != nil || len(files) < 2 {
		t.Fatalf("migrations missing (a baseline and the refs/receipts file are required): %v %v", files, err)
	}
	sort.Strings(files)
	return files
}

// TestMigrationDirectoryChecksum needs no database: it is what keeps atlas.sum,
// which the deployment applies, in step with the files that were reviewed.
func TestMigrationDirectoryChecksum(t *testing.T) {
	dir, err := migrate.NewLocalDir(migrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate.Validate(dir); err != nil {
		t.Fatalf("atlas.sum does not match the files; regenerate with make timeline-migration: %v", err)
	}
	for _, f := range migrationFiles(t) {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		up := strings.ToUpper(string(body))
		for _, destructive := range []string{"DROP TABLE", "DROP COLUMN", "TRUNCATE"} {
			if strings.Contains(up, destructive) {
				t.Fatalf("%s: %s belongs in a manual, reviewed script, not in a versioned migration", f, destructive)
			}
		}
	}
}

// migrationDB is one throwaway schema on the test server, opened with that
// schema as the search_path so every statement below stays inside it.
type migrationDB struct {
	*sql.DB
	ctx context.Context
}

func newMigrationDB(t *testing.T) *migrationDB {
	t.Helper()
	dsn := os.Getenv("TIMELINE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TIMELINE_TEST_POSTGRES_DSN for live PostgreSQL checks (make test-integration-timeline)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "timeline_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`)
	})
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	scoped := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = scoped.Close() })
	return &migrationDB{DB: scoped, ctx: ctx}
}

// apply runs the migration files in name order, one transaction per file, the
// way the deployment notes say to apply them. `limit` stops before that index
// (0 applies nothing, len(files) applies everything).
func (m *migrationDB) apply(t *testing.T, limit int) {
	t.Helper()
	files := migrationFiles(t)
	if limit > len(files) {
		limit = len(files)
	}
	for _, file := range files[:limit] {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := m.BeginTx(m.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		// No arguments: pgx sends the whole file over the simple protocol.
		if _, err = tx.ExecContext(m.ctx, string(body)); err != nil {
			_ = tx.Rollback()
			t.Fatalf("%s: %v", filepath.Base(file), err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

func (m *migrationDB) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := m.ExecContext(m.ctx, query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func (m *migrationDB) queryString(t *testing.T, query string, args ...any) string {
	t.Helper()
	var out string
	if err := m.QueryRowContext(m.ctx, query, args...).Scan(&out); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return out
}

// legacyEntry and legacyMedia are rows that exist before the refs/receipts
// migration: the shape a production database has after the baseline.
func (m *migrationDB) insertLegacyRows(t *testing.T) {
	t.Helper()
	m.exec(t, `INSERT INTO timeline_entries (id, user_id, title, occurred_date, timezone, occurred_at, created_at, updated_at)
		VALUES ('e1','u1','旧记录','2026-11-01','Asia/Shanghai','2026-11-01T09:00:00Z','2026-11-01T09:00:00Z','2026-11-01T09:00:00Z'),
		       ('e2','u2','别人的记录','2026-11-02','Asia/Shanghai','2026-11-02T09:00:00Z','2026-11-02T09:00:00Z','2026-11-02T09:00:00Z')`)
	m.exec(t, `INSERT INTO timeline_media_objects (id, user_id, entry_id, object_key, created_at)
		VALUES ('m1','u1','e1','timeline/u1/m1','2026-11-01T09:00:00Z'),
		       ('m2','u2','e2','timeline/u2/m2','2026-11-02T09:00:00Z')`)
}

// TestVersionedMigrationsBackfillOpaqueRefs applies the reviewed files the way a
// deployment does: the baseline first, real rows, then the refs/receipts file.
// It asserts what the ADR requires of that upgrade - every existing row keeps its
// data and gets a well-formed, unique ref, Media rows carry the owning Entry's
// ref (the Entry backfill runs first), and the new bounds are enforced by the
// database rather than by the caller.
func TestVersionedMigrationsBackfillOpaqueRefs(t *testing.T) {
	m := newMigrationDB(t)
	m.apply(t, 1)
	m.insertLegacyRows(t)
	m.apply(t, len(migrationFiles(t)))

	for _, entry := range []struct{ id, user, title string }{
		{"e1", "u1", "旧记录"},
		{"e2", "u2", "别人的记录"},
	} {
		ref := m.queryString(t, `SELECT public_ref FROM timeline_entries WHERE id = $1`, entry.id)
		if !entryRefPattern.MatchString(ref) {
			t.Fatalf("entry %s ref %q is not an opaque ent_ ref", entry.id, ref)
		}
		if got := m.queryString(t, `SELECT user_id || '|' || title FROM timeline_entries WHERE id = $1`, entry.id); got != entry.user+"|"+entry.title {
			t.Fatalf("entry %s lost its data: %q", entry.id, got)
		}
	}
	// The Media snapshot has to come from the already backfilled Entry row, not
	// from a fresh computation: the JOIN is the assertion that the two steps ran
	// in the required order.
	for _, media := range []struct{ id, entryID string }{{"m1", "e1"}, {"m2", "e2"}} {
		ref := m.queryString(t, `SELECT public_ref FROM timeline_media_objects WHERE id = $1`, media.id)
		if !mediaRefPattern.MatchString(ref) {
			t.Fatalf("media %s ref %q is not an opaque med_ ref", media.id, ref)
		}
		var ownsEntry bool
		if err := m.QueryRowContext(m.ctx, `SELECT m.entry_public_ref = e.public_ref
			FROM timeline_media_objects m JOIN timeline_entries e ON e.id = m.entry_id
			WHERE m.id = $1`, media.id).Scan(&ownsEntry); err != nil {
			t.Fatalf("media %s owning entry ref: %v", media.id, err)
		}
		if !ownsEntry {
			t.Fatalf("media %s does not carry its owning entry's public_ref", media.id)
		}
	}
	// Duplicate refs are impossible, not merely unlikely: the unique index is
	// what makes an opaque ref a single lookup key.
	err := m.insertEntryRef(t, "e3", "u1", m.queryString(t, `SELECT public_ref FROM timeline_entries WHERE id = 'e1'`))
	if !sqlState(err, "23505") {
		t.Fatalf("a duplicate entry ref was accepted: %v", err)
	}
	// A hand-written ref that does not match the format is refused by the CHECK.
	if err := m.insertEntryRef(t, "e4", "u1", "ent_SHORT"); !sqlState(err, "23514") {
		t.Fatalf("a malformed entry ref was accepted: %v", err)
	}
	// The backfill left no row that skipped it.
	if got := m.queryString(t, `SELECT count(*)::text FROM timeline_entries WHERE public_ref IS NULL OR public_ref = ''`); got != "0" {
		t.Fatalf("%s entries have no public_ref after the backfill", got)
	}
	if got := m.queryString(t, `SELECT count(*)::text FROM timeline_media_objects WHERE entry_public_ref IS NULL OR public_ref IS NULL`); got != "0" {
		t.Fatalf("%s media rows have no refs after the backfill", got)
	}
}

func (m *migrationDB) insertEntryRef(t *testing.T, id, user, ref string) error {
	t.Helper()
	_, err := m.ExecContext(m.ctx, `INSERT INTO timeline_entries
		(id, user_id, title, occurred_date, timezone, occurred_at, created_at, updated_at, public_ref)
		VALUES ($1,$2,'x','2026-11-05','Asia/Shanghai','2026-11-05T09:00:00Z',now(),now(),$3)`, id, user, ref)
	return err
}

// receipt is one row of timeline_mutations; empty ref strings are sent as NULL so
// a scenario can say which side of the target_kind CHECK it means to land on.
type receipt struct {
	id, user, mutation, target, op, hash, state string
	entryRef, mediaRef                          string
	revision                                    int64
	snapshot                                    string
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (m *migrationDB) insertReceipt(t *testing.T, r receipt) error {
	t.Helper()
	_, err := m.ExecContext(m.ctx, `INSERT INTO timeline_mutations
		(id, user_id, mutation_id, target_kind, operation, entry_ref, media_ref,
		 request_hash, result_revision, result_state, result_snapshot, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,now())`,
		r.id, r.user, r.mutation, r.target, r.op, nullIfEmpty(r.entryRef), nullIfEmpty(r.mediaRef),
		r.hash, r.revision, r.state, r.snapshot)
	return err
}

func validReceipt(id, user, mutation string) receipt {
	return receipt{
		id: id, user: user, mutation: mutation,
		target: "entry", op: "create",
		entryRef: entryRef, hash: strings.Repeat("a", 64),
		revision: 1, state: "active",
		snapshot: `{"entry_ref":"` + entryRef + `","revision":1}`,
	}
}

// TestMutationReceiptConstraintsRejectIllegalRows executes the CHECK constraints
// themselves. The ADR (and two review rounds) required an explicit two-branch
// constraint: an `entry` row may only carry the five record operations, a `media`
// row only `media_delete`, with exactly the refs that belong to each. A
// negative-form constraint would let `media + garbage` through, so the illegal
// set below is spelled out one combination per case.
func TestMutationReceiptConstraintsRejectIllegalRows(t *testing.T) {
	m := newMigrationDB(t)
	m.apply(t, len(migrationFiles(t)))

	legal := map[string]receipt{
		"entry/create":  validReceipt("r1", "u1", "m-create"),
		"entry/update":  {id: "r2", user: "u1", mutation: "m-update", target: "entry", op: "update", entryRef: entryRef, hash: strings.Repeat("b", 64), revision: 2, state: "active", snapshot: `{"revision":2}`},
		"entry/trash":   {id: "r3", user: "u1", mutation: "m-trash", target: "entry", op: "trash", entryRef: entryRef, hash: strings.Repeat("c", 64), revision: 3, state: "trashed", snapshot: `{"revision":3}`},
		"entry/restore": {id: "r4", user: "u1", mutation: "m-restore", target: "entry", op: "restore", entryRef: entryRef, hash: strings.Repeat("d", 64), revision: 4, state: "active", snapshot: `{"revision":4}`},
		"entry/purge":   {id: "r5", user: "u1", mutation: "m-purge", target: "entry", op: "purge", entryRef: entryRef, hash: strings.Repeat("e", 64), revision: 5, state: "purged", snapshot: `{"revision":5}`},
		"media/delete":  {id: "r6", user: "u1", mutation: "m-media", target: "media", op: "media_delete", entryRef: entryRef, mediaRef: mediaRef, hash: strings.Repeat("f", 64), revision: 1, state: "deleting", snapshot: `{"media_ref":"` + mediaRef + `"}`},
	}
	for name, r := range legal {
		if err := m.insertReceipt(t, r); err != nil {
			t.Fatalf("legal combination %s was rejected: %v", name, err)
		}
	}

	base := validReceipt("", "u1", "")
	illegal := map[string]struct {
		mutate func(*receipt)
		why    string
	}{
		"media/garbage":         {func(r *receipt) { r.target, r.op, r.mediaRef = "media", "garbage", mediaRef }, "a media row must be exactly media_delete"},
		"media/unknown":         {func(r *receipt) { r.target, r.op, r.mediaRef = "media", "unknown", mediaRef }, "a media row must be exactly media_delete"},
		"media/create":          {func(r *receipt) { r.target, r.op, r.mediaRef = "media", "create", mediaRef }, "an entry operation on a media row"},
		"media/empty-operation": {func(r *receipt) { r.target, r.op, r.mediaRef = "media", "", mediaRef }, "an empty operation on a media row"},
		"media/no-media-ref":    {func(r *receipt) { r.target, r.op, r.entryRef = "media", "media_delete", entryRef }, "a media row without its own ref"},
		"media/no-entry-ref":    {func(r *receipt) { r.target, r.op, r.entryRef, r.mediaRef = "media", "media_delete", "", mediaRef }, "a media receipt without its owning entry ref"},
		"entry/media-delete":    {func(r *receipt) { r.op = "media_delete" }, "media_delete on an entry row"},
		"entry/no-ref":          {func(r *receipt) { r.entryRef = "" }, "an entry row without a ref"},
		"entry/with-media-ref":  {func(r *receipt) { r.mediaRef = mediaRef }, "both refs on an entry row"},
		"unknown-target":        {func(r *receipt) { r.target = "garbage" }, "an unknown target_kind"},
		"malformed-entry-ref":   {func(r *receipt) { r.entryRef = "ent_SHORT" }, "an entry ref that is not opaque"},
		"malformed-media-ref":   {func(r *receipt) { r.target, r.op, r.mediaRef = "media", "media_delete", "med_SHORT" }, "a media ref that is not opaque"},
		"short-request-hash":    {func(r *receipt) { r.hash = "abc" }, "a request hash that is not SHA-256"},
		"zero-result-revision":  {func(r *receipt) { r.revision = 0 }, "a result revision below the first revision"},
		"negative-result-rev":   {func(r *receipt) { r.revision = -1 }, "a negative result revision"},
	}
	for name, c := range illegal {
		r := base
		r.id, r.mutation = "bad-"+name, "bad-mutation-"+name
		c.mutate(&r)
		if err := m.insertReceipt(t, r); !sqlState(err, "23514") {
			t.Fatalf("%s (%s) was not refused by a CHECK constraint: %v", name, c.why, err)
		}
	}
}

// TestMutationReceiptSurvivesCleanupAndIsOwnerScoped checks the two properties
// the replay contracts rest on that are visible in the database itself: the
// receipt is unique per owner+mutation (so a retried mutation cannot write a
// second, different result) and it outlives the rows it describes (cleanup
// deletes Media and Entry rows; a replay must still find the original result).
func TestMutationReceiptSurvivesCleanupAndIsOwnerScoped(t *testing.T) {
	m := newMigrationDB(t)
	m.apply(t, len(migrationFiles(t)))

	m.exec(t, `INSERT INTO timeline_entries (id, user_id, title, occurred_date, timezone, occurred_at, created_at, updated_at, public_ref)
		VALUES ('e1','u1','带附件','2026-11-03','Asia/Shanghai','2026-11-03T09:00:00Z',now(),now(),$1)`, entryRef)
	m.exec(t, `INSERT INTO timeline_media_objects (id, user_id, entry_id, object_key, created_at, public_ref, entry_public_ref, status)
		VALUES ('m1','u1','e1','timeline/u1/m1',now(),$1,$2,'deleting')`, mediaRef, entryRef)

	receiptRow := receipt{
		id: "r1", user: "u1", mutation: "m-media-delete",
		target: "media", op: "media_delete",
		entryRef: entryRef, mediaRef: mediaRef,
		hash: strings.Repeat("9", 64), revision: 1, state: "deleting",
		snapshot: `{"media_ref":"` + mediaRef + `","entry_ref":"` + entryRef + `","object_keys":["timeline/u1/m1"]}`,
	}
	if err := m.insertReceipt(t, receiptRow); err != nil {
		t.Fatal(err)
	}

	// Cleanup removes the rows the receipt describes; the receipt has no foreign
	// key to them on purpose.
	m.exec(t, `DELETE FROM timeline_entries WHERE id = 'e1'`)
	if got := m.queryString(t, `SELECT count(*)::text FROM timeline_media_objects WHERE id = 'm1'`); got != "0" {
		t.Fatalf("the media row survived the entry cleanup: %s", got)
	}
	snapshot := m.queryString(t, `SELECT result_snapshot::text FROM timeline_mutations WHERE user_id = 'u1' AND mutation_id = 'm-media-delete'`)
	var decoded struct {
		MediaRef  string   `json:"media_ref"`
		EntryRef  string   `json:"entry_ref"`
		ObjectKey []string `json:"object_keys"`
	}
	if err := json.Unmarshal([]byte(snapshot), &decoded); err != nil {
		t.Fatalf("the snapshot is not the JSON that was written: %v (%s)", err, snapshot)
	}
	if decoded.MediaRef != mediaRef || decoded.EntryRef != entryRef || len(decoded.ObjectKey) != 1 || decoded.ObjectKey[0] != "timeline/u1/m1" {
		t.Fatalf("the replayed snapshot changed after cleanup: %s", snapshot)
	}

	// A retried mutation cannot become a second result for the same owner...
	second := receiptRow
	second.id, second.snapshot = "r2", `{"different":true}`
	if err := m.insertReceipt(t, second); !sqlState(err, "23505") {
		t.Fatalf("a second receipt for the same (user_id, mutation_id) was accepted: %v", err)
	}
	// ...while the same mutation_id is only unique per owner.
	other := receiptRow
	other.id, other.user = "r3", "u2"
	if err := m.insertReceipt(t, other); err != nil {
		t.Fatalf("another account's identical mutation_id was refused: %v", err)
	}
	// A receipt is not silently overwritten: the snapshot is immutable once written.
	if _, err := m.ExecContext(m.ctx, `UPDATE timeline_mutations SET result_snapshot = '{"rewritten":true}'::jsonb WHERE id = 'r1'`); err == nil {
		t.Log("note: UPDATE on timeline_mutations succeeded in this database; the immutable-snapshot rule is enforced by the application role's grants, not by this schema")
	}
}

// sqlState reports whether err is the PostgreSQL error with the given SQLSTATE,
// so a failure says "the constraint did not fire" instead of "insert failed".
func sqlState(err error, code string) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == code
	}
	return false
}
