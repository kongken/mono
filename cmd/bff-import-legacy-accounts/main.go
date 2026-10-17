package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/kongken/mono/internal/bff/legacyimport"
)

func main() {
	tripmapDSN := flag.String("tripmap-dsn", os.Getenv("TRIPMAP_DSN"), "PostgreSQL DSN for tripmap business DB (search_path / schema as needed)")
	bffDSN := flag.String("bff-dsn", os.Getenv("BFF_DSN"), "PostgreSQL DSN for BFF auth DB")
	dryRun := flag.Bool("dry-run", false, "print actions without writing")
	flag.Parse()
	if *tripmapDSN == "" || *bffDSN == "" {
		fmt.Fprintln(os.Stderr, "usage: bff-import-legacy-accounts -tripmap-dsn ... -bff-dsn ... [--dry-run]")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	rep, err := legacyimport.Import(ctx, *tripmapDSN, *bffDSN, *dryRun)
	fmt.Println(rep.String())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(rep.Errors) > 0 {
		os.Exit(1)
	}
}
