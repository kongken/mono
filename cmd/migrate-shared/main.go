// Command migrate-shared installs or upgrades the schema that Timeline and Tripmap share in one
// PostgreSQL database. It is a deliberate, operator-run step (production keeps auto_migrate=false);
// docs/backend.md ("Timeline 与 Tripmap 共享迁移") has the runbook.
//
//	migrate-shared -check ...        inspect only: print what each phase would do, write nothing
//	migrate-shared ...               apply in one transaction (nothing is written when the check refuses)
//	migrate-shared -timeline-source-dsn <dsn> ...
//	                                 additionally copy a standalone Timeline database into this one
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/kongken/mono/internal/platform/migrations"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("SHARED_MIGRATION_DSN"), "PostgreSQL DSN of the shared (Tripmap) database")
	root := flag.String("root", ".", "repository root")
	links := flag.String("link-import", "", "SQL file containing the one-time media_entry_links import (required when Tripmap media rows exist)")
	check := flag.Bool("check", false, "inspect the database and print the plan; never writes")
	source := flag.String("timeline-source-dsn", os.Getenv("SHARED_MIGRATION_TIMELINE_SOURCE_DSN"), "DSN of a standalone Timeline database to copy into the shared one (cross-database to same-database move; the source is only read)")
	acceptRetire := flag.Bool("accept-retire-legacy-sessions", false, "allow retire_tripmap_sessions to drop legacy session tables that still hold rows")
	flag.Parse()
	if *dsn == "" {
		log.Fatal("-dsn or SHARED_MIGRATION_DSN is required")
	}
	plan := migrations.DiscoverPlan(filepath.Clean(*root))
	plan.LinkImport = *links
	plan.AcceptRetireLegacySessions = *acceptRetire
	ctx := context.Background()
	db, err := sql.Open("pgx", *dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	var reports []migrations.PhaseReport
	switch {
	case *check && *source != "":
		log.Fatal("-check and -timeline-source-dsn cannot be combined: run -check against a restored copy of the target, then the move")
	case *check:
		reports, err = plan.Check(ctx, db)
	case *source != "":
		src, openErr := sql.Open("pgx", *source)
		if openErr != nil {
			log.Fatal(openErr)
		}
		defer src.Close()
		reports, err = plan.ApplyFromTimelineDB(ctx, db, src)
	default:
		reports, err = plan.ApplyReport(ctx, db)
	}
	printReports(reports)
	var refused *migrations.RefusedError
	switch {
	case errors.As(err, &refused):
		fmt.Fprintln(os.Stderr, refused)
		os.Exit(2)
	case err != nil:
		log.Fatal(err)
	case *check:
		log.Println("check passed: nothing was written")
	default:
		log.Println("shared Timeline -> Tripmap migration applied")
	}
}

func printReports(reports []migrations.PhaseReport) {
	for _, r := range reports {
		detail := ""
		if r.Detail != "" {
			detail = "  (" + r.Detail + ")"
		}
		fmt.Printf("%-52s %-9s %s%s\n", r.Name, r.Action, r.File, detail)
	}
}
