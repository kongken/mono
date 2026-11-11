package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"path/filepath"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/kongken/mono/internal/platform/migrations"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("SHARED_MIGRATION_DSN"), "PostgreSQL DSN")
	root := flag.String("root", ".", "repository root")
	links := flag.String("link-import", "", "SQL file containing the one-time media_entry_links import")
	flag.Parse()
	if *dsn == "" {
		log.Fatal("-dsn or SHARED_MIGRATION_DSN is required")
	}
	plan := migrations.DiscoverPlan(filepath.Clean(*root))
	plan.LinkImport = *links
	db, err := sql.Open("pgx", *dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := plan.Apply(context.Background(), db); err != nil {
		log.Fatal(err)
	}
	log.Println("shared Timeline -> Tripmap migration applied")
}
