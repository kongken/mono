package timeline_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ariga.io/atlas/sql/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestMigrationDirectoryChecksum(t *testing.T) {
	dir, err := migrate.NewLocalDir(".")
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Validate(dir); err != nil {
		t.Fatalf("atlas.sum does not match timeline SQL: %v", err)
	}
}
func TestRefsMigrationUsesRandomRefsAndTransactionalChecks(t *testing.T) {
	b, err := os.ReadFile("20261107180000_refs_mutations.sql")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"CREATE EXTENSION IF NOT EXISTS pgcrypto",
		"gen_random_bytes(13)",
		"timeline_entries_public_ref_key",
		"timeline_mutations_target",
		"timeline_mutations_operation",
		"timeline_mutations_state",
		"timeline_mutations_user_id_mutation_id",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("migration missing %q", want)
		}
	}
	for _, forbidden := range []string{"md5('entry:'", "md5('media:'", "encode(id", "translate(substr(md5"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("migration derives opaque ref using %q", forbidden)
		}
	}
	if strings.Index(s, "ALTER TABLE \"timeline_entries\"") > strings.Index(s, "CREATE TABLE \"timeline_mutations\"") {
		t.Fatal("mutation table must be created after entry ref backfill")
	}
}

func TestPostgresTimelineMigrationAndChecks(t *testing.T) {
	dsn := os.Getenv("TRIPMAP_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TRIPMAP_TEST_POSTGRES_DSN for live PostgreSQL migration checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "timeline_migr_" + uuid.NewString()[:8]
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.ExecContext(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`) }()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	apply := func(name string) {
		t.Helper()
		body, readErr := os.ReadFile(filepath.Join(".", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		tx, beginErr := db.BeginTx(ctx, nil)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if _, execErr := tx.ExecContext(ctx, string(body)); execErr != nil {
			_ = tx.Rollback()
			t.Fatal(execErr)
		}
		if commitErr := tx.Commit(); commitErr != nil {
			t.Fatal(commitErr)
		}
	}
	apply("20261107170000_baseline.sql")
	now := time.Now().UTC()
	if _, err = db.ExecContext(ctx, `INSERT INTO timeline_entries (id,user_id,title,occurred_date,timezone,occurred_at,created_at,updated_at) VALUES ($1,$2,'entry','2026-11-07','UTC',$3,$3,$3)`, "entry-1", "user-a", now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO timeline_media_objects (id,user_id,entry_id,object_key,created_at) VALUES ($1,$2,$3,'object',$4)`, "media-1", "user-a", "entry-1", now); err != nil {
		t.Fatal(err)
	}
	apply("20261107180000_refs_mutations.sql")
	var entryRef, mediaRef, entryPublicRef string
	if err = db.QueryRowContext(ctx, `SELECT public_ref FROM timeline_entries WHERE id='entry-1'`).Scan(&entryRef); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT public_ref, entry_public_ref FROM timeline_media_objects WHERE id='media-1'`).Scan(&mediaRef, &entryPublicRef); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(entryRef, "ent_") || len(entryRef) != 30 || !strings.HasPrefix(mediaRef, "med_") || len(mediaRef) != 30 || entryPublicRef != entryRef {
		t.Fatalf("invalid migrated refs: entry=%q media=%q entry_public=%q", entryRef, mediaRef, entryPublicRef)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO timeline_mutations (id,user_id,mutation_id,target_kind,operation,request_hash,entry_ref,result_revision,result_state,result_snapshot,created_at) VALUES ('m1','user-a','mutation-1','entry','create',repeat('a',64),$1,1,'active','{}',$2)`, entryRef, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO timeline_mutations (id,user_id,mutation_id,target_kind,operation,request_hash,entry_ref,media_ref,result_revision,result_state,result_snapshot,created_at) VALUES ('m2','user-a','mutation-2','entry','create',repeat('a',64),$1,$2,1,'active','{}',$3)`, entryRef, mediaRef, now); err == nil {
		t.Fatal("invalid entry mutation CHECK combination was accepted")
	}
}
