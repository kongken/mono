package integration

// Upgrade scenarios of the shared Timeline + Tripmap schema (cmd/migrate-shared) on a real
// PostgreSQL. Every scenario starts from a database that already holds user data, because an
// empty-database test cannot show that an upgrade keeps anything. Each test works on throwaway
// schemas (see newMigrationDB) and never touches a real environment.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kongken/mono/internal/platform/migrations"
)

const repoRoot = "../../.."

const importedEntryRef = "ent_00000000000000000000000001"

func sharedPlan() migrations.SharedPlan { return migrations.DiscoverPlan(repoRoot) }

// applyFiles applies migration files exactly the way an operator following the old, per-directory
// notes would: one transaction per file, no history table.
func (m *migrationDB) applyFiles(t *testing.T, files ...string) {
	t.Helper()
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := m.BeginTx(m.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(m.ctx, string(body)); err != nil {
			_ = tx.Rollback()
			t.Fatalf("%s: %v", filepath.Base(file), err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

func (m *migrationDB) queryInt(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := m.QueryRowContext(m.ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func (m *migrationDB) tripmapPre(t *testing.T) []string {
	t.Helper()
	plan := sharedPlan()
	return append([]string{plan.TripmapBaseline}, plan.TripmapIncrements...)
}

// seedTripmap is an installation that ran Tripmap alone up to the last pre-receipt increment.
func (m *migrationDB) seedTripmap(t *testing.T, withMedia bool) {
	t.Helper()
	m.applyFiles(t, m.tripmapPre(t)...)
	// The legacy session tables only exist before retire_tripmap_sessions; dropping them here
	// would be that migration, so the seed leaves the tables as that increment left them (absent).
	m.exec(t, `INSERT INTO users (id, display_name, created_at, updated_at, revision) VALUES ('u1','旧用户',now(),now(),7),('u2','另一个',now(),now(),3)`)
	m.exec(t, `INSERT INTO city_visits (id, region_code, region_name, province_code, color, travel_date, note, created_at, updated_at, user_id)
		VALUES ('v1','330100','杭州市','330000','#4b937d','2026-10-12','西湖',now(),now(),'u1')`)
	m.exec(t, `INSERT INTO snapshots (id, reason, revision, visit_count, mark_count, payload, created_at, user_id)
		VALUES ('s1','manual',7,1,0,'\x7b7d',now(),'u1')`)
	m.exec(t, `INSERT INTO shares (id, visibility, token, show_cities, show_dates, show_notes, show_photos, photo_ids, version, published_revision, payload, created_at, updated_at, user_id)
		VALUES ('sh1','link','tok',false,false,false,false,'[]',1,7,'\x7b7d',now(),now(),'u1')`)
	m.exec(t, `INSERT INTO friendships (id, user_low_id, user_high_id, status, created_at, updated_at, requester_id, addressee_id) VALUES ('fr1','u1','u2','accepted',now(),now(),'u1','u2')`)
	m.exec(t, `INSERT INTO world_documents (id, revision, payload, place_count, trip_count, created_at, updated_at, user_id) VALUES ('wd1',1,'\x00',0,0,now(),now(),'u2')`)
	if withMedia {
		m.exec(t, `INSERT INTO media_objects (id, object_key, content_type, size_bytes, status, region_code, created_at, updated_at, user_id)
			VALUES ('m1','media/files/m1','image/jpeg',10,'ready','330100',now(),now(),'u1')`)
	}
}

// seedTimeline writes rows through the standalone Timeline migrations, `through` of the five
// files applied (2 = the state of the first Timeline release, 5 = current).
func (m *migrationDB) seedTimeline(t *testing.T, through int) {
	t.Helper()
	files := migrationFiles(t)
	m.applyFiles(t, files[:2]...)
	m.exec(t, `INSERT INTO timeline_entries (id, user_id, public_ref, title, occurred_date, timezone, occurred_at, created_at, updated_at, amount_minor, currency)
		VALUES ('e1','u1','ent_0123456789abcdefghjkmnpqrs','旧记录','2026-11-01','Asia/Shanghai','2026-11-01T09:00:00Z','2026-11-01T09:00:00Z','2026-11-01T09:00:00Z',1250,'CNY'),
		       ('e2','u2','ent_1123456789abcdefghjkmnpqrs','别人的记录','2026-11-02','Asia/Shanghai','2026-11-02T09:00:00Z','2026-11-02T09:00:00Z','2026-11-02T09:00:00Z',0,'')`)
	m.exec(t, `INSERT INTO timeline_media_objects (id, user_id, entry_id, object_key, created_at, public_ref, entry_public_ref, state, filename)
		VALUES ('m1','u1','e1','timeline/u1/m1','2026-11-01T09:00:00Z','med_0123456789abcdefghjkmnpqrs','ent_0123456789abcdefghjkmnpqrs','ready','a.jpg'),
		       ('m2','u2','e2','timeline/u2/m2','2026-11-02T09:00:00Z','med_1123456789abcdefghjkmnpqrs','ent_1123456789abcdefghjkmnpqrs','ready','b.jpg')`)
	m.exec(t, `INSERT INTO timeline_cleanup_objects (id, object_key, status, attempts, created_at, updated_at)
		VALUES ('c1','timeline/u1/gone','pending',2,'2026-11-03T00:00:00Z','2026-11-03T00:00:00Z')`)
	m.exec(t, `INSERT INTO timeline_mutations (id, user_id, mutation_id, target_kind, operation, request_hash, entry_ref, result_revision, result_state, result_snapshot, snapshot_version, created_at, expires_at)
		VALUES ('r1','u1','mut-1','entry','create',$1,'ent_0123456789abcdefghjkmnpqrs',1,'active','\x0a00',1,'2026-11-01T09:00:00Z','2026-12-01T09:00:00Z')`, strings.Repeat("a", 64))
	if through > 2 {
		m.applyFiles(t, files[2:through]...)
	}
	if through >= 5 {
		m.exec(t, `INSERT INTO timeline_categories (id, public_ref, user_id, name, created_at, updated_at) VALUES ('k1','cat_0123456789abcdefghjkmnpqrs','u1','餐饮','2026-11-12T00:00:00Z','2026-11-12T00:00:00Z')`)
	}
}

// shape is every table of the schema with its row count, plus whether the history table exists:
// what "nothing was written" means.
func (m *migrationDB) shape(t *testing.T) string {
	t.Helper()
	rows, err := m.QueryContext(m.ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err = rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	rows.Close()
	var parts []string
	for _, n := range names {
		parts = append(parts, fmt.Sprintf("%s=%d", n, m.queryInt(t, `SELECT count(*) FROM "`+n+`"`)))
	}
	cols := m.queryInt(t, `SELECT count(*) FROM information_schema.columns WHERE table_schema = current_schema()`)
	idxs := m.queryInt(t, `SELECT count(*) FROM pg_indexes WHERE schemaname = current_schema()`)
	return strings.Join(parts, ",") + fmt.Sprintf(" columns=%d indexes=%d", cols, idxs)
}

func (m *migrationDB) history(t *testing.T) map[string]string {
	t.Helper()
	rows, err := m.QueryContext(m.ctx, `SELECT name, source || '|' || checksum FROM shared_migration_history`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var n, v string
		if err = rows.Scan(&n, &v); err != nil {
			t.Fatal(err)
		}
		out[n] = v
	}
	return out
}

func fileSum(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (m *migrationDB) digest(t *testing.T, table, cols string) string {
	t.Helper()
	return m.queryString(t, `SELECT count(*)::text || ':' || coalesce(md5(string_agg(row(`+cols+`)::text, '|' ORDER BY id)), '') FROM `+table)
}

func mustRefuse(t *testing.T, err error, contains ...string) *migrations.RefusedError {
	t.Helper()
	var refused *migrations.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("want a RefusedError before any write, got %v", err)
	}
	for _, want := range contains {
		if !strings.Contains(refused.Error(), want) {
			t.Fatalf("refusal does not mention %q:\n%v", want, refused)
		}
	}
	if !strings.Contains(refused.Error(), "Remediation") {
		t.Fatalf("refusal carries no remediation:\n%v", refused)
	}
	return refused
}

// TestSharedInstallOnAnEmptyDatabaseAppliesEveryMigrationOnce is the clean-install path: all
// sixteen recorded phases (the optional link import is absent) in dependency order, each recorded with the checksum of the file in the
// directory, and a second run that changes nothing.
func TestSharedInstallOnAnEmptyDatabaseAppliesEveryMigrationOnce(t *testing.T) {
	m := newMigrationDB(t)
	plan := sharedPlan()
	if err := plan.Apply(m.ctx, m.DB); err != nil {
		t.Fatal(err)
	}
	history := m.history(t)
	files := append(append([]string{}, mustGlob(t, "../../../migrations/timeline")...), mustGlob(t, "../../../migrations/tripmap")...)
	recorded := map[string]bool{}
	for _, file := range files {
		found := false
		for name, v := range history {
			if strings.HasSuffix(v, "|"+fileSum(t, file)) && strings.HasPrefix(v, "applied|") {
				recorded[name], found = true, true
			}
		}
		if !found {
			t.Errorf("%s is not recorded with its directory checksum", filepath.Base(file))
		}
	}
	if len(history) != len(files)+0 && len(history) != len(files) {
		t.Errorf("history has %d phases for %d files: %v", len(history), len(files), history)
	}
	if m.queryInt(t, `SELECT count(*) FROM information_schema.columns WHERE table_name='timeline_entries' AND column_name='amount_present' AND table_schema=current_schema()`) != 1 ||
		m.queryInt(t, `SELECT count(*) FROM information_schema.tables WHERE table_name='timeline_categories' AND table_schema=current_schema()`) != 1 ||
		m.queryInt(t, `SELECT count(*) FROM information_schema.columns WHERE table_name='timeline_media_objects' AND column_name='mutation_id' AND table_schema=current_schema()`) != 1 {
		t.Fatal("a new Timeline migration did not run on a clean install")
	}
	before := m.shape(t)
	stamp := m.queryString(t, `SELECT max(applied_at)::text FROM shared_migration_history`)
	if err := plan.Apply(m.ctx, m.DB); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if m.shape(t) != before || m.queryString(t, `SELECT max(applied_at)::text FROM shared_migration_history`) != stamp {
		t.Fatal("a repeated run changed the database")
	}
	reports, err := plan.Check(m.ctx, m.DB)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reports {
		if r.Action != migrations.ActionRecorded && r.Action != migrations.ActionSkip {
			t.Errorf("after install %s would still %s", r.Name, r.Action)
		}
	}
}

func mustGlob(t *testing.T, dir string) []string {
	t.Helper()
	files, err := migrations.SQLFiles(dir)
	if err != nil || len(files) == 0 {
		t.Fatalf("%s: %v %v", dir, files, err)
	}
	return files
}

// TestSharedUpgradeOfAnExistingTripmapDatabaseKeepsEveryRow runs the tool against a database that
// Tripmap created and filled before Timeline existed. The baseline and the eight increments
// must not run again; rows, owner relations and the media link survive.
func TestSharedUpgradeOfAnExistingTripmapDatabaseKeepsEveryRow(t *testing.T) {
	m := newMigrationDB(t)
	m.seedTripmap(t, true)
	plan := sharedPlan()

	// Without the one-time link import the tool refuses before the first write.
	before := m.shape(t)
	_, err := plan.Check(m.ctx, m.DB)
	mustRefuse(t, err, "1 existing Tripmap media row(s)", "-link-import")
	mustRefuse(t, plan.Apply(m.ctx, m.DB), "-link-import")
	if m.shape(t) != before {
		t.Fatal("a refused run changed the database")
	}

	linkImport := filepath.Join(t.TempDir(), "link-import.sql")
	if err := os.WriteFile(linkImport, []byte(`INSERT INTO timeline_entries (id, user_id, public_ref, title, occurred_date, timezone, occurred_at, created_at, updated_at)
VALUES ('imp-m1','u1','`+importedEntryRef+`','Tripmap 照片','2026-10-12','UTC',now(),now(),now());
INSERT INTO media_entry_links (media_id, user_id, entry_ref) VALUES ('m1','u1','`+importedEntryRef+`');
`), 0o600); err != nil {
		t.Fatal(err)
	}
	plan.LinkImport = linkImport
	tripmapDigests := map[string]string{
		"users":        m.digest(t, "users", "id, display_name, revision"),
		"city_visits":  m.digest(t, "city_visits", "id, region_code, region_name, note, user_id"),
		"snapshots":    m.digest(t, "snapshots", "id, revision, payload, user_id"),
		"shares":       m.digest(t, "shares", "id, token, user_id"),
		"friendships":  m.digest(t, "friendships", "id, user_low_id, user_high_id, status"),
		"world":        m.digest(t, "world_documents", "id, revision, user_id"),
		"media_object": m.digest(t, "media_objects", "id, object_key, user_id, size_bytes"),
	}
	reports, err := plan.Check(m.ctx, m.DB)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reports {
		switch {
		case r.Name == "tripmap-baseline" || strings.HasPrefix(r.Name, "tripmap-increment-"):
			if r.Action != migrations.ActionAdopt {
				t.Errorf("%s would %s on an existing Tripmap database, want adopt (never replay)", r.Name, r.Action)
			}
		case strings.HasPrefix(r.Name, "timeline-"), r.Name == "media-entry-links", r.Name == "tripmap-media", r.Name == "media-entry-link-import":
			if r.Action != migrations.ActionApply {
				t.Errorf("%s would %s, want apply", r.Name, r.Action)
			}
		}
	}
	if err = plan.Apply(m.ctx, m.DB); err != nil {
		t.Fatal(err)
	}
	for table, cols := range map[string]string{"users": "id, display_name, revision", "city_visits": "id, region_code, region_name, note, user_id", "snapshots": "id, revision, payload, user_id", "shares": "id, token, user_id", "friendships": "id, user_low_id, user_high_id, status", "world_documents": "id, revision, user_id", "media_objects": "id, object_key, user_id, size_bytes"} {
		key := map[string]string{"world_documents": "world", "media_objects": "media_object"}[table]
		if key == "" {
			key = table
		}
		if got := m.digest(t, table, cols); got != tripmapDigests[key] {
			t.Errorf("%s changed during the upgrade: %s -> %s", table, tripmapDigests[key], got)
		}
	}
	// Owner/ref relations: the media row carries the entry ref of the same account.
	if got := m.queryString(t, `SELECT m.entry_public_ref || '|' || e.user_id || '|' || m.user_id FROM media_objects m JOIN timeline_entries e ON e.public_ref = m.entry_public_ref AND e.user_id = m.user_id WHERE m.id = 'm1'`); got != importedEntryRef+"|u1|u1" {
		t.Fatalf("media/entry owner relation: %q", got)
	}
	if !strings.HasPrefix(m.queryString(t, `SELECT public_ref FROM media_objects WHERE id = 'm1'`), "med_") {
		t.Fatal("existing media row did not get an opaque ref")
	}
	history := m.history(t)
	if !strings.HasPrefix(history["tripmap-baseline"], "adopted|") || !strings.HasPrefix(history["tripmap-media"], "applied|") {
		t.Fatalf("history does not distinguish adopted from applied phases: %v", history)
	}
	// Cross-account isolation stays enforced by the owner foreign keys after the upgrade.
	if _, err = m.ExecContext(m.ctx, `INSERT INTO media_entry_links (media_id, user_id, entry_ref) VALUES ('m1','u2',$1)`, importedEntryRef); err == nil {
		t.Fatal("a media link can cross accounts after the upgrade")
	}
	// Repeat run: nothing pending, nothing changes.
	after := m.shape(t)
	if err = plan.Apply(m.ctx, m.DB); err != nil {
		t.Fatalf("repeat run: %v", err)
	}
	if m.shape(t) != after {
		t.Fatal("repeat run changed the database")
	}
}

// TestSharedUpgradeAdoptsAStandaloneTimelineDatabase covers a database Timeline created alone:
// at the first release's schema (baseline + refs) and at the current one. Existing Timeline
// rows survive byte for byte, the new migrations only add, and Tripmap's schema is added next to it.
func TestSharedUpgradeAdoptsAStandaloneTimelineDatabase(t *testing.T) {
	for _, through := range []int{2, 5} {
		through := through
		t.Run(fmt.Sprintf("timeline-files-%d", through), func(t *testing.T) {
			m := newMigrationDB(t)
			m.seedTimeline(t, through)
			cols := "id, user_id, public_ref, title, occurred_date, amount_minor, currency"
			want := map[string]string{
				"entries":   m.digest(t, "timeline_entries", cols),
				"media":     m.digest(t, "timeline_media_objects", "id, user_id, entry_id, public_ref, entry_public_ref, object_key"),
				"cleanup":   m.digest(t, "timeline_cleanup_objects", "id, object_key, status, attempts"),
				"mutations": m.digest(t, "timeline_mutations", "id, user_id, mutation_id, entry_ref, result_snapshot, request_hash"),
			}
			plan := sharedPlan()
			reports, err := plan.Check(m.ctx, m.DB)
			if err != nil {
				t.Fatal(err)
			}
			byName := map[string]migrations.Action{}
			for _, r := range reports {
				byName[r.Name] = r.Action
			}
			if byName["timeline-baseline"] != migrations.ActionAdopt || byName["timeline-refs"] != migrations.ActionAdopt || byName["tripmap-baseline"] != migrations.ActionApply {
				t.Fatalf("plan for a standalone Timeline database: %v", byName)
			}
			if err = plan.Apply(m.ctx, m.DB); err != nil {
				t.Fatal(err)
			}
			got := map[string]string{
				"entries":   m.digest(t, "timeline_entries", cols),
				"media":     m.digest(t, "timeline_media_objects", "id, user_id, entry_id, public_ref, entry_public_ref, object_key"),
				"cleanup":   m.digest(t, "timeline_cleanup_objects", "id, object_key, status, attempts"),
				"mutations": m.digest(t, "timeline_mutations", "id, user_id, mutation_id, entry_ref, result_snapshot, request_hash"),
			}
			for k := range want {
				if got[k] != want[k] {
					t.Errorf("Timeline %s changed: %s -> %s", k, want[k], got[k])
				}
			}
			if m.queryInt(t, `SELECT count(*) FROM timeline_entries WHERE amount_present`) != 0 {
				t.Error("amount_present must default to false for existing entries")
			}
			if m.queryInt(t, `SELECT count(*) FROM users`) != 0 || m.queryInt(t, `SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='city_visits'`) != 1 {
				t.Error("the Tripmap schema must be added empty next to the Timeline data")
			}
			history := m.history(t)
			if !strings.HasPrefix(history["timeline-baseline"], "adopted|") || !strings.HasPrefix(history["tripmap-baseline"], "applied|") {
				t.Fatalf("history: %v", history)
			}
			if through == 2 && !strings.HasPrefix(history["timeline-20261112011000_categories"], "applied|") {
				t.Fatalf("the missing Timeline migrations must be applied, not adopted: %v", history)
			}
			if through == 5 && !strings.HasPrefix(history["timeline-20261112011000_categories"], "adopted|") {
				t.Fatalf("the already-applied Timeline migrations must be adopted, not replayed: %v", history)
			}
			if m.queryInt(t, `SELECT count(*) FROM timeline_categories`) != map[int]int64{2: 0, 5: 1}[through] {
				t.Error("category rows were lost or invented")
			}
		})
	}
}

// TestSharedUpgradeFromTheEarlierSharedToolOnlyAddsTheNewTimelineMigrations is the database a
// previous version of migrate-shared produced: a history table with the old shape and fourteen
// phases. The new run adds the three Timeline migrations and keeps everything else.
func TestSharedUpgradeFromTheEarlierSharedToolOnlyAddsTheNewTimelineMigrations(t *testing.T) {
	m := newMigrationDB(t)
	plan := sharedPlan()
	old := plan
	old.TimelineIncrements = nil
	if err := old.Apply(m.ctx, m.DB); err != nil {
		t.Fatal(err)
	}
	// Rewind to the history shape of that tool: name + applied_at only, no checksum column.
	m.exec(t, `ALTER TABLE shared_migration_history DROP COLUMN source, DROP COLUMN checksum`)
	m.exec(t, `INSERT INTO users (id, display_name, created_at, updated_at, revision) VALUES ('u1','旧用户',now(),now(),7)`)
	m.exec(t, `INSERT INTO timeline_entries (id, user_id, public_ref, title, occurred_date, timezone, occurred_at, created_at, updated_at)
		VALUES ('e1','u1','ent_0123456789abcdefghjkmnpqrs','旧记录','2026-11-01','UTC',now(),now(),now())`)
	want := m.digest(t, "timeline_entries", "id, user_id, public_ref, title")
	reports, err := plan.Check(m.ctx, m.DB)
	if err != nil {
		t.Fatal(err)
	}
	applied := 0
	for _, r := range reports {
		if r.Action == migrations.ActionApply {
			applied++
			if !strings.HasPrefix(r.Name, "timeline-2026111") {
				t.Errorf("unexpected pending phase %s", r.Name)
			}
		}
	}
	if applied != 3 {
		t.Fatalf("pending phases = %d, want the three new Timeline migrations", applied)
	}
	if err = plan.Apply(m.ctx, m.DB); err != nil {
		t.Fatal(err)
	}
	if m.digest(t, "timeline_entries", "id, user_id, public_ref, title") != want || m.queryInt(t, `SELECT count(*) FROM users`) != 1 {
		t.Fatal("existing rows changed")
	}
	if len(m.history(t)) != 16 { // the optional link import is not recorded when none was given
		t.Fatalf("history after the upgrade: %v", m.history(t))
	}
}

// TestSharedPlanRefusesUnsupportedStatesBeforeAnyWrite: each case is a database the plan must not
// touch. The proof of "before any write" is the schema shape (every table, row count, column
// and index) being identical after the refused Apply.
func TestSharedPlanRefusesUnsupportedStatesBeforeAnyWrite(t *testing.T) {
	type refusal struct {
		name     string
		prepare  func(t *testing.T, m *migrationDB, plan *migrations.SharedPlan)
		mentions []string
	}
	cases := []refusal{
		{"a same-named column without its index", func(t *testing.T, m *migrationDB, _ *migrations.SharedPlan) {
			m.seedTimeline(t, 2)
			m.exec(t, `ALTER TABLE timeline_media_objects ADD COLUMN mutation_id text`)
		}, []string{"timeline-20261112000000_media_upload_mutations", "partially present", "mutation_id"}},
		{"a same-named table with another shape", func(t *testing.T, m *migrationDB, _ *migrations.SharedPlan) {
			m.seedTimeline(t, 2)
			m.exec(t, `CREATE TABLE timeline_categories (id text PRIMARY KEY, label text)`)
		}, []string{"timeline-20261112011000_categories", "partially present"}},
		{"a Tripmap increment applied without its predecessor", func(t *testing.T, m *migrationDB, plan *migrations.SharedPlan) {
			files := m.tripmapPre(t)
			m.applyFiles(t, files[0]) // baseline
			m.applyFiles(t, files[3]) // f7 collaboration, but f5/f6 missing -> users.friend_code without city_trips
		}, []string{"tripmap-increment-02", "out-of-order"}},
		{"Tripmap tables that are not Tripmap's baseline", func(t *testing.T, m *migrationDB, _ *migrations.SharedPlan) {
			m.exec(t, `CREATE TABLE users (id text PRIMARY KEY, email text)`)
		}, []string{"tripmap-baseline", "partially present"}},
		{"Timeline refs without the Timeline baseline", func(t *testing.T, m *migrationDB, _ *migrations.SharedPlan) {
			m.exec(t, `CREATE TABLE timeline_mutations (id text PRIMARY KEY)`)
		}, []string{"timeline-refs", "partially present"}},
		{"a history written by a newer tool", func(t *testing.T, m *migrationDB, _ *migrations.SharedPlan) {
			m.exec(t, `CREATE TABLE shared_migration_history (name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`)
			m.exec(t, `INSERT INTO shared_migration_history (name) VALUES ('timeline-99999999999999_from_the_future')`)
		}, []string{"timeline-99999999999999_from_the_future", "newer version"}},
		{"an applied migration file edited afterwards", func(t *testing.T, m *migrationDB, plan *migrations.SharedPlan) {
			if err := plan.Apply(m.ctx, m.DB); err != nil {
				t.Fatal(err)
			}
			m.exec(t, `UPDATE shared_migration_history SET checksum = repeat('0', 64) WHERE name = 'timeline-20261112010000_amount_presence'`)
		}, []string{"timeline-20261112010000_amount_presence", "immutable"}},
		{"live legacy sessions that the retire increment would drop", func(t *testing.T, m *migrationDB, _ *migrations.SharedPlan) {
			files := m.tripmapPre(t)
			m.applyFiles(t, files[:6]...) // through f12, before retire_tripmap_sessions
			m.exec(t, `INSERT INTO users (id, display_name, created_at, updated_at, revision) VALUES ('u1','x',now(),now(),1)`)
			m.exec(t, `INSERT INTO auth_sessions (id, token_hash, created_at, expires_at, user_id) VALUES ('s1','h',now(),now() + interval '1 day','u1')`)
		}, []string{"auth_sessions", "-accept-retire-legacy-sessions"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := newMigrationDB(t)
			plan := sharedPlan()
			tc.prepare(t, m, &plan)
			before := m.shape(t)
			err := plan.Apply(m.ctx, m.DB)
			mustRefuse(t, err, tc.mentions...)
			if after := m.shape(t); after != before {
				t.Fatalf("the refused run wrote to the database:\nbefore %s\nafter  %s", before, after)
			}
			_, checkErr := plan.Check(m.ctx, m.DB)
			mustRefuse(t, checkErr, tc.mentions...)
		})
	}
}

// TestSharedFailureInsideThePlanRollsEverythingBack: the link import fails in the middle of the
// plan, after the Timeline schema, the Tripmap link table and the first phases ran. The database
// must be back where it was, with no history table, and a corrected rerun must succeed.
func TestSharedFailureInsideThePlanRollsEverythingBack(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "empty"
		if existing {
			name = "existing-tripmap"
		}
		t.Run(name, func(t *testing.T) {
			m := newMigrationDB(t)
			if existing {
				m.seedTripmap(t, true)
			}
			plan := sharedPlan()
			bad := filepath.Join(t.TempDir(), "bad-import.sql")
			// Valid SQL up to a statement the database rejects; the earlier insert is an
			// effect that must disappear with everything else.
			if err := os.WriteFile(bad, []byte(`INSERT INTO media_entry_links (media_id, user_id, entry_ref) VALUES ('ghost','u1','`+importedEntryRef+`'); SELECT 1/0;`), 0o600); err != nil {
				t.Fatal(err)
			}
			plan.LinkImport = bad
			before := m.shape(t)
			if err := plan.Apply(m.ctx, m.DB); err == nil {
				t.Fatal("a failing import must fail the run")
			}
			if after := m.shape(t); after != before {
				t.Fatalf("failed run left changes behind:\nbefore %s\nafter  %s", before, after)
			}
			if m.queryInt(t, `SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name IN ('shared_migration_history','timeline_entries','media_entry_links')`) != 0 {
				t.Fatal("tables of the plan survived the rollback")
			}
			plan.LinkImport = ""
			if existing {
				good := filepath.Join(t.TempDir(), "import.sql")
				if err := os.WriteFile(good, []byte(`INSERT INTO timeline_entries (id, user_id, public_ref, title, occurred_date, timezone, occurred_at, created_at, updated_at) VALUES ('imp','u1','`+importedEntryRef+`','x','2026-10-12','UTC',now(),now(),now()); INSERT INTO media_entry_links (media_id, user_id, entry_ref) VALUES ('m1','u1','`+importedEntryRef+`');`), 0o600); err != nil {
					t.Fatal(err)
				}
				plan.LinkImport = good
			}
			if err := plan.Apply(m.ctx, m.DB); err != nil {
				t.Fatalf("the corrected rerun failed: %v", err)
			}
			if len(m.history(t)) != 17-map[bool]int{false: 1, true: 0}[existing] {
				t.Fatalf("history after the corrected run: %v", m.history(t))
			}
		})
	}
}

// TestSharedCheckNeverWrites: -check is what an operator runs against a restored copy first.
func TestSharedCheckNeverWrites(t *testing.T) {
	m := newMigrationDB(t)
	m.seedTimeline(t, 5)
	before := m.shape(t)
	reports, err := sharedPlan().Check(m.ctx, m.DB)
	if err != nil || len(reports) == 0 {
		t.Fatalf("%v %v", reports, err)
	}
	if m.shape(t) != before || m.queryInt(t, `SELECT count(*) FROM pg_tables WHERE schemaname=current_schema() AND tablename='shared_migration_history'`) != 0 {
		t.Fatal("Check wrote to the database")
	}
}

// TestSharedCrossDatabaseMoveKeepsTimelineDataAndOwnerRelations is the cross-database to
// same-database path: a standalone Timeline database (its own `timeline` database, as the earlier
// examples suggested) is merged into the Tripmap database. Every Timeline table is copied inside
// the upgrade transaction, proved with counts and digests, and the source is left untouched.
func TestSharedCrossDatabaseMoveKeepsTimelineDataAndOwnerRelations(t *testing.T) {
	src := newMigrationDB(t)
	src.seedTimeline(t, 5) // installs pgcrypto into the source schema, as the refs migration does
	dst := newMigrationDBAlso(t, src)
	dst.seedTripmap(t, false)
	plan := sharedPlan()
	tables := map[string]string{
		"timeline_entries":         "id, user_id, public_ref, title, occurred_date, occurred_time, timezone, category, location, amount_minor, currency, url, occurred_at, deleted_at, mutation_id, revision, created_at, updated_at, amount_present",
		"timeline_media_objects":   "id, user_id, entry_id, object_key, thumbnail_key, upload_key, created_at, public_ref, entry_public_ref, state, revision, filename, content_type, size_bytes, width, height, upload_expires_at, updated_at, mutation_id",
		"timeline_cleanup_objects": "id, object_key, status, attempts, created_at, updated_at",
		"timeline_mutations":       "id, user_id, mutation_id, target_kind, operation, request_hash, entry_ref, media_ref, result_revision, result_state, result_snapshot, snapshot_version, created_at, expires_at",
		"timeline_categories":      "id, public_ref, user_id, name, active, is_default, is_other, revision, created_at, updated_at",
	}
	want := map[string]string{}
	var names []string
	for table, cols := range tables {
		want[table] = src.digest(t, table, cols)
		names = append(names, table)
	}
	sort.Strings(names)
	srcBefore := src.shape(t)
	tripmapUsers := dst.digest(t, "users", "id, display_name, revision")

	// A stale source is refused before anything is written to either side.
	stale := newMigrationDBAlso(t, src)
	stale.seedTimeline(t, 2)
	dstBefore := dst.shape(t)
	_, err := plan.ApplyFromTimelineDB(dst.ctx, dst.DB, stale.DB)
	mustRefuse(t, err, "source Timeline database is not at the current version")
	if dst.shape(t) != dstBefore {
		t.Fatal("a refused move changed the target")
	}

	if _, err = plan.ApplyFromTimelineDB(dst.ctx, dst.DB, src.DB); err != nil {
		t.Fatal(err)
	}
	for _, table := range names {
		if got := dst.digest(t, table, tables[table]); got != want[table] {
			t.Errorf("%s was not copied faithfully: source %s, target %s", table, want[table], got)
		}
	}
	if src.shape(t) != srcBefore {
		t.Fatal("the source database was modified")
	}
	if dst.digest(t, "users", "id, display_name, revision") != tripmapUsers {
		t.Fatal("Tripmap rows changed")
	}
	if dst.queryInt(t, `SELECT count(*) FROM timeline_media_objects m JOIN timeline_entries e ON e.id = m.entry_id WHERE m.user_id <> e.user_id`) != 0 {
		t.Fatal("owner relation broken by the move")
	}
	// The owner foreign key still protects the shared tables in the merged database.
	if _, err = dst.ExecContext(dst.ctx, `INSERT INTO media_entry_links (media_id, user_id, entry_ref) VALUES ('nope','u2','ent_0123456789abcdefghjkmnpqrs')`); err == nil {
		t.Fatal("a link may point at another account's entry after the move")
	}
	// Running it again must not merge a second copy.
	_, err = plan.ApplyFromTimelineDB(dst.ctx, dst.DB, src.DB)
	mustRefuse(t, err, "already")
}
