package integration

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"ariga.io/atlas/sql/migrate"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/kongken/mono/internal/tripmap/ent"
)

const migrationsDir = "../../../migrations/tripmap"

func TestMigrationDirectoryChecksum(t *testing.T) {
	dir, err := migrate.NewLocalDir(migrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate.Validate(dir); err != nil {
		t.Fatalf("atlas.sum does not match the files; regenerate with make migration: %v", err)
	}
}

func migrationFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	if err != nil || len(files) < 2 {
		t.Fatalf("migrations missing: %v %v", files, err)
	}
	sort.Strings(files)
	return files
}

// Existing deployments were created by auto_migrate from the pre-F5 schema,
// which the baseline file reproduces. The test starts from that state with
// real rows, applies the remaining files the way the deployment notes say
// (one transaction per file) and checks that old rows keep their data, that the
// result is exactly what Ent expects, and that no further change is pending.
func TestVersionedMigrationsUpgradeExistingData(t *testing.T) {
	dsn := os.Getenv("TRIPMAP_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TRIPMAP_TEST_POSTGRES_DSN for live PostgreSQL checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "tripmap_migr_" + uuid.NewString()[:8]
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.ExecContext(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`) }()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	scoped := stdlib.OpenDB(*cfg)
	defer scoped.Close()
	apply := func(file string) {
		t.Helper()
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := scoped.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		// No arguments: pgx runs the whole file with the simple protocol.
		if _, err = tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			t.Fatalf("%s: %v", filepath.Base(file), err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	files := migrationFiles(t)
	apply(files[0])
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := scoped.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO users (id, display_name, created_at, updated_at, revision) VALUES ('u1','旧用户',now(),now(),7)`)
	exec(`INSERT INTO city_visits (id, region_code, region_name, province_code, color, travel_date, note, created_at, updated_at, user_id)
		VALUES ('v1','330100','杭州市','330000','#4b937d','2026-10-12','西湖',now(),now(),'u1')`)
	exec(`INSERT INTO snapshots (id, reason, revision, visit_count, mark_count, payload, created_at, user_id)
		VALUES ('s1','manual',7,1,0,'\x7b7d',now(),'u1')`)
	exec(`INSERT INTO media_objects (id, object_key, content_type, size_bytes, status, region_code, created_at, updated_at, user_id)
		VALUES ('m1','k','image/jpeg',10,'ready','330100',now(),now(),'u1')`)
	for _, f := range files[1:] {
		apply(f)
	}

	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, scoped)))
	user, err := client.User.Get(ctx, "u1")
	if err != nil || user.SchemaVersion != 1 || user.Revision != 7 || user.DisplayName != "旧用户" || !user.FriendShowCities || user.FriendShowDates || user.FriendShowNotes || user.FriendCode != nil {
		t.Fatalf("user after migration: %+v %v", user, err)
	}
	visit, err := client.CityVisit.Get(ctx, "v1")
	if err != nil || visit.TravelDate != "2026-10-12" || visit.Note != "西湖" {
		t.Fatalf("visit changed: %+v %v", visit, err)
	}
	snap, err := client.Snapshot.Get(ctx, "s1")
	if err != nil || snap.SchemaVersion != 1 || snap.TripCount != 0 || string(snap.Payload) != "{}" {
		t.Fatalf("snapshot after migration: %+v %v", snap, err)
	}
	media, err := client.MediaObject.Get(ctx, "m1")
	if err != nil || media.TripID != "" || media.RegionCode != "330100" {
		t.Fatalf("media after migration: %+v %v", media, err)
	}
	if n, err := client.Trip.Query().Count(ctx); err != nil || n != 0 {
		t.Fatalf("new table should start empty: %d %v", n, err)
	}
	if n, err := client.Friendship.Query().Count(ctx); err != nil || n != 0 {
		t.Fatalf("friendships table should start empty: %d %v", n, err)
	}
	if n, err := client.SharedTrip.Query().Count(ctx); err != nil || n != 0 {
		t.Fatalf("shared_trips table should start empty: %d %v", n, err)
	}
	if n, err := client.TripMember.Query().Count(ctx); err != nil || n != 0 {
		t.Fatalf("trip_members table should start empty: %d %v", n, err)
	}
	if n, err := client.Itinerary.Query().Count(ctx); err != nil || n != 0 {
		t.Fatalf("itineraries table should start empty: %d %v", n, err)
	}
	// A share row keeps the user's data apart from the public token and goes away with the user.
	exec(`INSERT INTO shares (id, visibility, token, show_cities, show_dates, show_notes, show_photos, photo_ids, version, published_revision, payload, created_at, updated_at, user_id)
		VALUES ('sh1','link','tok',false,false,false,false,'[]',1,7,'\x7b7d',now(),now(),'u1')`)
	if _, err := scoped.ExecContext(ctx, `INSERT INTO shares (id, visibility, token, photo_ids, created_at, updated_at, user_id) VALUES ('sh2','link','tok2','[]',now(),now(),'u1')`); err == nil {
		t.Fatal("a user can own two share rows")
	}
	exec(`INSERT INTO users (id, display_name, created_at, updated_at, revision) VALUES ('u2','另一个',now(),now(),0)`)
	if _, err := scoped.ExecContext(ctx, `INSERT INTO shares (id, visibility, token, photo_ids, created_at, updated_at, user_id) VALUES ('sh3','link','tok','[]',now(),now(),'u2')`); err == nil {
		t.Fatal("two shares share one token")
	}
	exec(`INSERT INTO shares (id, visibility, token, photo_ids, created_at, updated_at, user_id) VALUES ('sh4','link','tok4','[]',now(),now(),'u2')`)
	exec(`INSERT INTO friendships (id, user_low_id, user_high_id, status, created_at, updated_at, requester_id, addressee_id) VALUES ('fr1','u1','u2','accepted',now(),now(),'u1','u2')`)
	if _, err := scoped.ExecContext(ctx, `INSERT INTO friendships (id, user_low_id, user_high_id, status, created_at, updated_at, requester_id, addressee_id) VALUES ('fr2','u1','u2','pending',now(),now(),'u2','u1')`); err == nil {
		t.Fatal("duplicate friendship pair allowed")
	}
	exec(`INSERT INTO shared_trips (id, region_code, trip_id, travel_date, note, created_at, updated_at, owner_id) VALUES ('st1','330100','t1','2026-10-12','共游',now(),now(),'u1')`)
	exec(`INSERT INTO trip_members (id, member_trip_id, role, status, invited_by, created_at, updated_at, shared_trip_id, user_id) VALUES ('tm1','t1','editor','active','u1',now(),now(),'st1','u2')`)
	exec(`INSERT INTO itineraries (id, itinerary_id, title, note, color, stops, version, created_at, updated_at, user_id) VALUES ('it1','itin-1','江南行','','\#4b937d','[]',1,now(),now(),'u2')`)
	if _, err := scoped.ExecContext(ctx, `INSERT INTO itineraries (id, itinerary_id, title, note, color, stops, version, created_at, updated_at, user_id) VALUES ('it2','itin-1','重复','','\#4b937d','[]',1,now(),now(),'u2')`); err == nil {
		t.Fatal("duplicate itinerary_id per user allowed")
	}
	exec(`INSERT INTO notification_profiles (id, timezone, send_hour, show_places, email, pending_email, code_hash, code_attempts, created_at, updated_at, user_id) VALUES ('np1','Asia/Shanghai',9,false,'','','',0,now(),now(),'u2')`)
	if _, err := scoped.ExecContext(ctx, `INSERT INTO notification_profiles (id, created_at, updated_at, user_id) VALUES ('np2',now(),now(),'u2')`); err == nil {
		t.Fatal("a user can own two notification profiles")
	}
	exec(`INSERT INTO notification_settings (id, kind, channel, enabled, created_at, updated_at, user_id) VALUES ('ns1','memory','email',true,now(),now(),'u2')`)
	if _, err := scoped.ExecContext(ctx, `INSERT INTO notification_settings (id, kind, channel, enabled, created_at, updated_at, user_id) VALUES ('ns2','memory','email',false,now(),now(),'u2')`); err == nil {
		t.Fatal("duplicate notification setting per kind and channel allowed")
	}
	exec(`INSERT INTO push_subscriptions (id, endpoint_hash, endpoint, p256dh, auth, failures, created_at, updated_at, user_id) VALUES ('ps1','h1','https://push.example/1','k','a',0,now(),now(),'u2')`)
	if _, err := scoped.ExecContext(ctx, `INSERT INTO push_subscriptions (id, endpoint_hash, endpoint, p256dh, auth, failures, created_at, updated_at, user_id) VALUES ('ps2','h1','https://push.example/1','k','a',0,now(),now(),'u1')`); err == nil {
		t.Fatal("one push endpoint can belong to two rows")
	}
	exec(`INSERT INTO notification_deliveries (id, kind, channel, period, status, attempts, created_at, updated_at, user_id) VALUES ('nd1','memory','email','2026-10-15','sent',1,now(),now(),'u2')`)
	if _, err := scoped.ExecContext(ctx, `INSERT INTO notification_deliveries (id, kind, channel, period, status, attempts, created_at, updated_at, user_id) VALUES ('nd2','memory','email','2026-10-15','pending',1,now(),now(),'u2')`); err == nil {
		t.Fatal("a notification can be claimed twice for one period")
	}
	exec(`DELETE FROM users WHERE id='u2'`)
	for _, table := range []string{"notification_profiles", "notification_settings", "push_subscriptions", "notification_deliveries"} {
		var rows int
		if err := scoped.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE user_id='u2'`).Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("deleting a user left %d %s rows: %v", rows, table, err)
		}
	}
	var left int
	if err := scoped.QueryRowContext(ctx, `SELECT count(*) FROM shares WHERE user_id='u2'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("deleting a user left %d share rows: %v", left, err)
	}
	if err := scoped.QueryRowContext(ctx, `SELECT count(*) FROM friendships WHERE requester_id='u2' OR addressee_id='u2'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("deleting a user left %d friendship rows: %v", left, err)
	}
	if err := scoped.QueryRowContext(ctx, `SELECT count(*) FROM trip_members WHERE user_id='u2'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("deleting a user left %d trip_member rows: %v", left, err)
	}
	if err := scoped.QueryRowContext(ctx, `SELECT count(*) FROM itineraries WHERE user_id='u2'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("deleting a user left %d itinerary rows: %v", left, err)
	}
	// Ent keeps proposing to re-create its two partial unique indexes because it
	// compares the predicate text with PostgreSQL's normalised form. That is also
	// what a database created by Schema.Create reports, so the migrated database
	// must report exactly the same and nothing more.
	reference := freshEntSchema(t, ctx, admin, cfg)
	if got, want := pendingChanges(t, ctx, client), pendingChanges(t, ctx, reference); got != want {
		t.Fatalf("migrations differ from the Ent schema.\nmigrated database wants:\n%s\nEnt-created database wants:\n%s", got, want)
	}
	// Logto-only clients never select or write legacy credential columns.
	if _, err := client.User.Create().SetID("u-new").SetUsername("fresh_user").SetDisplayName("新用户").Save(ctx); err != nil {
		t.Fatalf("ent create profile-only user: %v", err)
	}
	cleanup, err := os.ReadFile("../../../deploy/sql/tripmap/post_import_drop_legacy_auth_columns.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := scoped.ExecContext(ctx, string(cleanup)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.User.Get(ctx, "u-new"); err != nil {
		t.Fatalf("new client requires retired columns: %v", err)
	}
	if _, err := client.User.Create().SetID("u-same-name").SetUsername("fresh_user").Save(ctx); err != nil {
		t.Fatalf("cached usernames must not be unique after cleanup: %v", err)
	}
}

func TestMigrationDirectoryListsPostImportCleanupSeparately(t *testing.T) {
	t.Helper()
	body, err := os.ReadFile("../../../deploy/sql/tripmap/post_import_drop_legacy_auth_columns.sql")
	if err != nil || !strings.Contains(string(body), "DROP COLUMN") {
		t.Fatal("post-import cleanup script missing or empty")
	}
	files, _ := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	for _, f := range files {
		if strings.Contains(filepath.Base(f), "drop_legacy") {
			t.Fatal("destructive cleanup must not live in atlas migrations")
		}
	}
}

func pendingChanges(t *testing.T, ctx context.Context, c *ent.Client) string {
	t.Helper()
	var b bytes.Buffer
	if err := c.Schema.WriteTo(ctx, &b); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(b.String())
}

func freshEntSchema(t *testing.T, ctx context.Context, admin *sql.DB, base *pgx.ConnConfig) *ent.Client {
	t.Helper()
	name := "tripmap_ref_" + uuid.NewString()[:8]
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA "`+name+`"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), `DROP SCHEMA "`+name+`" CASCADE`) })
	cfg := base.Copy()
	cfg.RuntimeParams["search_path"] = name
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = db.Close() })
	c := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	if err := c.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	return c
}
