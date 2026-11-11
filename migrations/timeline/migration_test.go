package timeline_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"ariga.io/atlas/sql/migrate"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
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
func TestReceiptCleanupIndexIsTimelineOwned(t *testing.T) {
	body, err := os.ReadFile("20261107180000_refs_mutations.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `CREATE INDEX "timeline_mutations_expires_at" ON "timeline_mutations" ("expires_at")`) {
		t.Fatal("timeline-owned receipt migration must provide the expires_at cleanup index")
	}
}
func TestRefsMigrationPostgreSQL(t *testing.T) {
	dsn := os.Getenv("TIMELINE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TIMELINE_TEST_POSTGRES_DSN for live PostgreSQL migration checks")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	apply := func(t *testing.T, badOwner bool) {
		t.Helper()
		schema := "timeline_migr_" + uuid.NewString()[:8]
		if _, err := db.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
			t.Fatal(err)
		}
		defer db.ExecContext(ctx, `DROP SCHEMA "`+schema+`" CASCADE`)
		if _, err := db.ExecContext(ctx, `SET search_path TO "`+schema+`"`); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile("20261107170000_baseline.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO timeline_entries (id,user_id,title,occurred_date,timezone,occurred_at,created_at,updated_at) VALUES ('e1','u1','entry','2026-11-08','UTC',now(),now(),now())`); err != nil {
			t.Fatal(err)
		}
		owner := "u1"
		if badOwner {
			owner = "u2"
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO timeline_media_objects (id,user_id,entry_id,object_key,created_at) VALUES ('m1',$1,'e1','key',now())`, owner); err != nil {
			t.Fatal(err)
		}
		body, err = os.ReadFile("20261107180000_refs_mutations.sql")
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, execErr := tx.ExecContext(ctx, string(body))
		if badOwner {
			if execErr == nil {
				t.Fatal("owner mismatch migration unexpectedly succeeded")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			var exists int
			if err := db.QueryRowContext(ctx, `SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name='timeline_entries' AND column_name='public_ref'`, schema).Scan(&exists); err == nil {
				t.Fatal("failed migration left public_ref column")
			}
			return
		}
		if execErr != nil {
			t.Fatal(execErr)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		var ref string
		if err := db.QueryRowContext(ctx, `SELECT public_ref FROM timeline_entries WHERE id='e1'`).Scan(&ref); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(ref, "ent_") || len(ref) != 30 {
			t.Fatalf("invalid entry ref %q", ref)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO timeline_mutations (id,user_id,mutation_id,target_kind,operation,request_hash,entry_ref,result_revision,result_state,result_snapshot,created_at,expires_at) VALUES ('r1','u1','m-unique','entry','update',repeat('a',64),$1,2,'active',decode('7b7d','hex'),now(),now()+interval '30 days')`, ref); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE timeline_mutations SET result_snapshot=decode('7b226368616e676564223a747275657d','hex') WHERE id='r1'`); err == nil {
			t.Fatal("receipt snapshot update unexpectedly succeeded")
		}
	}
	apply(t, false)
	apply(t, true)
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
		"public_ref !~ '^ent_[0-9a-f]{26}$'",
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
