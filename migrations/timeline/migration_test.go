package timeline_test

import (
	"os"
	"strings"
	"testing"

	"ariga.io/atlas/sql/migrate"
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
