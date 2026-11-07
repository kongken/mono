package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SharedPlan is the checked-in cross-service migration path. It bootstraps both
// clean baselines in one database, then applies Timeline refs, link staging,
// external import, and Tripmap's receipt migration.
type SharedPlan struct {
	TimelineBaseline string
	TripmapBaseline  string
	TimelineRefs     string
	LinkStaging      string
	TripmapMedia     string
	LinkImport       string
}

// Apply executes every phase in one database transaction. A failed import or
// constraint check rolls back the preceding phases. The history table makes a
// successful run idempotent on subsequent deployments.
func (p SharedPlan) Apply(ctx context.Context, db *sql.DB) error {
	if p.TimelineBaseline == "" || p.TripmapBaseline == "" || p.TimelineRefs == "" || p.LinkStaging == "" || p.TripmapMedia == "" {
		return fmt.Errorf("shared migration plan is incomplete")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS shared_migration_history (name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("create shared migration history: %w", err)
	}
	phases := []struct{ name, path string }{
		{"timeline-baseline", p.TimelineBaseline},
		{"tripmap-baseline", p.TripmapBaseline},
		{"timeline-refs", p.TimelineRefs},
		{"media-entry-links", p.LinkStaging},
		{"media-entry-link-import", p.LinkImport},
		{"tripmap-media", p.TripmapMedia},
	}
	for _, phase := range phases {
		if phase.path == "" {
			continue
		}
		var applied bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM shared_migration_history WHERE name=$1)`, phase.name).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		body, readErr := os.ReadFile(phase.path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", phase.name, readErr)
		}
		if len(strings.TrimSpace(string(body))) == 0 {
			return fmt.Errorf("%s is empty", phase.name)
		}
		if _, err = tx.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", phase.name, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO shared_migration_history(name) VALUES ($1)`, phase.name); err != nil {
			return fmt.Errorf("record %s: %w", phase.name, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit shared migration: %w", err)
	}
	return nil
}

func DiscoverPlan(root string) SharedPlan {
	return SharedPlan{
		TimelineBaseline: filepath.Join(root, "migrations", "timeline", "20261107170000_baseline.sql"),
		TripmapBaseline:  filepath.Join(root, "migrations", "tripmap", "20261014011442_baseline.sql"),
		TimelineRefs:     filepath.Join(root, "migrations", "timeline", "20261107180000_refs_mutations.sql"),
		LinkStaging:      filepath.Join(root, "migrations", "tripmap", "20261107900000_media_entry_links.sql"),
		TripmapMedia:     filepath.Join(root, "migrations", "tripmap", "20261108000000_media_delete_receipts.sql"),
	}
}

func SQLFiles(dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}
