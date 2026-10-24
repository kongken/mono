// Command migrate-gen writes the next versioned PostgreSQL migration from the
// Ent schema (internal/tripmap/ent/schema, internal/bff/ent/schema, internal/metro/ent/schema or internal/usersrv/ent/schema, chosen by -dir), for deployments that run with
// auto_migrate: false. It replays the existing files in -dir on an empty scratch
// schema of the -dev database, diffs the result against the Ent schema and writes
// the difference plus atlas.sum. The -dev DSN only needs CREATEDB: a scratch
// database is created and dropped, nothing else in the server is touched.
//
//	make migration NAME=add_something DEV_DSN='postgres://user:pass@127.0.0.1:15432/tripmap?sslmode=disable'
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"ariga.io/atlas/sql/migrate"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	bffmigrate "github.com/kongken/mono/internal/bff/ent/migrate"
	metromigrate "github.com/kongken/mono/internal/metro/ent/migrate"
	entmigrate "github.com/kongken/mono/internal/tripmap/ent/migrate"
	usersrvmigrate "github.com/kongken/mono/internal/usersrv/ent/migrate"
)

func main() {
	dir := flag.String("dir", "migrations/tripmap", "directory holding the versioned migrations")
	name := flag.String("name", "", "short snake_case name of the change")
	dev := flag.String("dev", os.Getenv("DEV_DSN"), "DSN of a PostgreSQL server where a scratch database may be created")
	flag.Parse()
	if *name == "" || *dev == "" {
		log.Fatal("usage: migrate-gen -name <change> -dev <dsn> [-dir migrations/tripmap|migrations/bff|migrations/metro|migrations/usersrv]")
	}
	tables := entmigrate.Tables
	switch {
	case strings.HasSuffix(*dir, "/bff"):
		tables = bffmigrate.Tables
	case strings.HasSuffix(*dir, "/metro"):
		tables = metromigrate.Tables
	case strings.HasSuffix(*dir, "/usersrv"):
		tables = usersrvmigrate.Tables
	}
	if err := run(context.Background(), *dir, *name, *dev, tables); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, dir, name, dsn string, tables []*schema.Table) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	local, err := migrate.NewLocalDir(dir)
	if err != nil {
		return err
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer admin.Close()
	// Atlas only replays into a database that is completely clean, so the scratch
	// space is a database of its own, created here and dropped at the end.
	scratch := "migrate_gen_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err = admin.ExecContext(ctx, `CREATE DATABASE "`+scratch+`"`); err != nil {
		return fmt.Errorf("creating the scratch database (the -dev user needs CREATEDB): %w", err)
	}
	defer func() { _, _ = admin.ExecContext(context.Background(), `DROP DATABASE "`+scratch+`" WITH (FORCE)`) }()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return err
	}
	cfg.Database = scratch
	conn := stdlib.OpenDB(*cfg)
	defer conn.Close()
	m, err := schema.NewMigrate(entsql.OpenDB(dialect.Postgres, conn),
		schema.WithDir(local),
		// One forward-only file per change: down scripts that drop data are not generated.
		schema.WithFormatter(migrate.DefaultFormatter),
		schema.WithMigrationMode(schema.ModeReplay),
		schema.WithDialect(dialect.Postgres),
		schema.WithDropColumn(false),
		schema.WithDropIndex(false),
	)
	if err != nil {
		return err
	}
	before, _ := os.ReadDir(dir)
	if err = m.NamedDiff(ctx, name, tables...); err != nil {
		return fmt.Errorf("diff: %w", err)
	}
	after, _ := os.ReadDir(dir)
	if len(after) == len(before) {
		fmt.Println("the migrations already match the Ent schema; nothing written")
		return nil
	}
	fmt.Printf("written to %s (%s)\n", dir, time.Now().Format(time.RFC3339))
	return nil
}
