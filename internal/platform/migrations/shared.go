package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SharedPlan is the checked-in cross-service migration path for the single
// PostgreSQL database that Timeline and Tripmap share (owner/ref foreign keys
// and the mutation-receipt table cross the service boundary, so they cannot live
// in separate databases; see docs/backend.md "Timeline 与 Tripmap 共享迁移").
//
// A clean database gets every phase in dependency order. An existing database
// is inspected first: phases whose schema is already present are adopted
// (recorded, never replayed), the rest are applied, and anything that is
// partially present, out of order, or would destroy data is refused before the
// first write. Everything runs in one transaction.
type SharedPlan struct {
	TimelineBaseline   string
	TripmapBaseline    string
	TripmapIncrements  []string
	TimelineRefs       string
	LinkStaging        string
	TripmapMedia       string
	LinkImport         string
	TimelineIncrements []string
	// AcceptRetireLegacySessions allows the retire_tripmap_sessions increment
	// to drop legacy session/deletion-receipt tables that still contain rows.
	AcceptRetireLegacySessions bool
}

const (
	chainTimeline = "timeline"
	chainTripmap  = "tripmap"
)

type phase struct {
	name, path string
	chain      string
	probes     []probe
	// idempotent phases may be replayed without harm; they are never reported as
	// partial or out of order, only adopted when their target state already holds.
	idempotent bool
}

func (p SharedPlan) phases() []phase {
	mk := func(name, path, chain string) phase {
		ph := phase{name: name, path: path, chain: chain}
		ph.probes, ph.idempotent = probesFor(filepath.Base(path))
		return ph
	}
	phases := []phase{
		mk("timeline-baseline", p.TimelineBaseline, chainTimeline),
		mk("tripmap-baseline", p.TripmapBaseline, chainTripmap),
		mk("timeline-refs", p.TimelineRefs, chainTimeline),
	}
	for i, path := range p.TripmapIncrements {
		phases = append(phases, mk(fmt.Sprintf("tripmap-increment-%02d", i), path, chainTripmap))
	}
	phases = append(phases,
		mk("media-entry-links", p.LinkStaging, chainTripmap),
		phase{name: "media-entry-link-import", path: p.LinkImport, chain: chainTripmap},
		mk("tripmap-media", p.TripmapMedia, chainTripmap),
	)
	for _, path := range p.TimelineIncrements {
		phases = append(phases, mk("timeline-"+strings.TrimSuffix(filepath.Base(path), ".sql"), path, chainTimeline))
	}
	return phases
}

// Action says what a run does with one phase.
type Action string

const (
	ActionRecorded Action = "recorded" // already in shared_migration_history
	ActionAdopt    Action = "adopt"    // schema already present: record, do not replay
	ActionApply    Action = "apply"    // run the SQL file
	ActionSkip     Action = "skip"     // nothing to do (no import file / import not needed)
)

// PhaseReport is one line of a plan check.
type PhaseReport struct {
	Name   string
	File   string
	Action Action
	Detail string
}

// RefusedError is returned before any write when the database is in a state the
// plan does not support. Problems lists every finding, Remediation the next steps.
type RefusedError struct {
	Problems []string
}

func (e *RefusedError) Error() string {
	var b strings.Builder
	b.WriteString("shared migration refused before any change was made:")
	for _, problem := range e.Problems {
		b.WriteString("\n  - " + problem)
	}
	b.WriteString("\nRemediation: see docs/backend.md \"Timeline 与 Tripmap 共享迁移\"; restore the database from the pre-upgrade backup or bring the schema to a supported state, then re-run (use -check to preview).")
	return b.String()
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type probe struct{ kind, a, b string }

func tbl(name string) probe  { return probe{kind: "table", a: name} }
func col(t, c string) probe  { return probe{kind: "column", a: t, b: c} }
func idx(name string) probe  { return probe{kind: "index", a: name} }
func gone(name string) probe { return probe{kind: "gone", a: name} }
func (pr probe) String() string {
	switch pr.kind {
	case "column":
		return "column " + pr.a + "." + pr.b
	case "gone":
		return "absence of table " + pr.a
	default:
		return pr.kind + " " + pr.a
	}
}

func (pr probe) holds(ctx context.Context, q queryer) (bool, error) {
	var ok bool
	var err error
	switch pr.kind {
	case "table":
		err = q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1)`, pr.a).Scan(&ok)
	case "gone":
		err = q.QueryRowContext(ctx, `SELECT NOT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1)`, pr.a).Scan(&ok)
	case "column":
		err = q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2)`, pr.a, pr.b).Scan(&ok)
	case "index":
		err = q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema() AND indexname = $1)`, pr.a).Scan(&ok)
	default:
		err = fmt.Errorf("unknown probe %q", pr.kind)
	}
	return ok, err
}

// probesFor returns the schema fingerprint of a checked-in migration file. A file
// without a fingerprint can only be applied, never adopted.
func probesFor(base string) ([]probe, bool) {
	switch {
	case strings.HasSuffix(base, "_baseline.sql") && strings.HasPrefix(base, "20261107"):
		return []probe{tbl("timeline_entries"), tbl("timeline_media_objects"), tbl("timeline_cleanup_objects")}, false
	case strings.HasSuffix(base, "_refs_mutations.sql"):
		return []probe{col("timeline_entries", "public_ref"), col("timeline_media_objects", "public_ref"), tbl("timeline_mutations"), idx("timeline_entries_public_ref_key")}, false
	case strings.HasSuffix(base, "_media_upload_mutations.sql"):
		return []probe{col("timeline_media_objects", "mutation_id"), idx("timeline_media_objects_user_id_mutation_id_key")}, false
	case strings.HasSuffix(base, "_amount_presence.sql"):
		return []probe{col("timeline_entries", "amount_present")}, false
	case strings.HasSuffix(base, "_categories.sql"):
		return []probe{tbl("timeline_categories"), col("timeline_categories", "public_ref"), col("timeline_categories", "user_id"), col("timeline_categories", "is_default"), col("timeline_categories", "is_other"), col("timeline_categories", "revision")}, false
	case strings.HasSuffix(base, "_baseline.sql"):
		return []probe{tbl("users"), tbl("city_visits"), tbl("footprint_changes"), tbl("media_objects"), tbl("province_marks"), tbl("snapshots"), tbl("sync_receipts"), tbl("media_cleanups")}, false
	case strings.HasSuffix(base, "_f5_multiple_trips.sql"):
		return []probe{col("media_objects", "trip_id"), tbl("city_trips"), col("users", "schema_version")}, false
	case strings.HasSuffix(base, "_f6_shares.sql"):
		return []probe{tbl("shares")}, false
	case strings.HasSuffix(base, "_f7_collaboration.sql"):
		return []probe{tbl("friendships"), tbl("shared_trips"), tbl("trip_members"), col("users", "friend_code")}, false
	case strings.HasSuffix(base, "_f11_itineraries.sql"):
		return []probe{tbl("itineraries")}, false
	case strings.HasSuffix(base, "_f12_notifications.sql"):
		return []probe{tbl("notification_deliveries"), tbl("notification_profiles"), tbl("notification_settings"), tbl("push_subscriptions")}, false
	case strings.HasSuffix(base, "_retire_tripmap_sessions.sql"):
		return []probe{gone("auth_sessions"), gone("account_deletion_receipts")}, true
	case strings.HasSuffix(base, "_align_legacy_user_indexes.sql"):
		return []probe{idx("user_email")}, true
	case strings.HasSuffix(base, "_world_documents.sql"):
		return []probe{tbl("world_documents"), tbl("world_receipts")}, false
	case strings.HasSuffix(base, "_media_entry_links.sql"):
		return []probe{tbl("media_entry_links")}, false
	case strings.HasSuffix(base, "_media_delete_receipts.sql"):
		return []probe{col("media_objects", "public_ref"), col("media_objects", "entry_public_ref"), col("media_objects", "revision"), idx("media_objects_public_ref_key")}, false
	}
	return nil, false
}

type inspection struct {
	reports  []PhaseReport
	problems []string
	before   map[string]int64
}

func fileChecksum(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return "", errors.New("file is empty")
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func loadHistory(ctx context.Context, q queryer) (map[string]string, error) {
	history := map[string]string{}
	var present bool
	if err := q.QueryRowContext(ctx, `SELECT to_regclass('shared_migration_history') IS NOT NULL`).Scan(&present); err != nil {
		return nil, err
	}
	if !present {
		return history, nil
	}
	var hasChecksum bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'shared_migration_history' AND column_name = 'checksum')`).Scan(&hasChecksum); err != nil {
		return nil, err
	}
	query := `SELECT name, '' FROM shared_migration_history`
	if hasChecksum {
		query = `SELECT name, checksum FROM shared_migration_history`
	}
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, sum string
		if err = rows.Scan(&name, &sum); err != nil {
			return nil, err
		}
		history[name] = sum
	}
	return history, rows.Err()
}

func countRows(ctx context.Context, q queryer, table string) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM "`+strings.ReplaceAll(table, `"`, `""`)+`"`).Scan(&n)
	return n, err
}

func tableExists(ctx context.Context, q queryer, table string) (bool, error) {
	return tbl(table).holds(ctx, q)
}

// inspect decides, without writing, what a run would do with every phase and
// collects every reason to refuse.
func (p SharedPlan) inspect(ctx context.Context, q queryer) (*inspection, error) {
	insp := &inspection{before: map[string]int64{}}
	history, err := loadHistory(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read shared migration history: %w", err)
	}
	phases := p.phases()
	known := map[string]bool{}
	for _, ph := range phases {
		known[ph.name] = true
	}
	var unknown []string
	for name := range history {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		insp.problems = append(insp.problems, fmt.Sprintf("shared_migration_history lists phases this build does not know (%s); the database was migrated by a newer version, run the matching or newer migrate-shared", strings.Join(unknown, ", ")))
	}

	pending := map[string]bool{} // chain -> an earlier non-idempotent phase will be applied
	actions := map[string]Action{}
	for _, ph := range phases {
		report := PhaseReport{Name: ph.name, File: filepath.Base(ph.path)}
		if ph.name == "media-entry-link-import" {
			// Decided after tripmap-media, below.
			insp.reports = append(insp.reports, report)
			continue
		}
		if ph.path == "" {
			insp.problems = append(insp.problems, fmt.Sprintf("%s has no migration file", ph.name))
			insp.reports = append(insp.reports, report)
			continue
		}
		sum, sumErr := fileChecksum(ph.path)
		if sumErr != nil {
			insp.problems = append(insp.problems, fmt.Sprintf("%s: %v", ph.name, sumErr))
			insp.reports = append(insp.reports, report)
			continue
		}
		if recorded, ok := history[ph.name]; ok {
			report.Action = ActionRecorded
			if recorded == "" {
				report.Detail = "recorded by an earlier migrate-shared; checksum was not stored, content cannot be verified"
			} else if recorded != sum {
				insp.problems = append(insp.problems, fmt.Sprintf("%s: %s changed after it was applied (recorded checksum %.12s, file %.12s); applied migrations are immutable, add a new migration instead", ph.name, report.File, recorded, sum))
			}
			actions[ph.name] = ActionRecorded
			insp.reports = append(insp.reports, report)
			continue
		}
		var holds, missing []string
		for _, pr := range ph.probes {
			ok, probeErr := pr.holds(ctx, q)
			if probeErr != nil {
				return nil, probeErr
			}
			if ok {
				holds = append(holds, pr.String())
			} else {
				missing = append(missing, pr.String())
			}
		}
		switch {
		case len(ph.probes) == 0 || len(holds) == 0:
			report.Action = ActionApply
		case len(missing) == 0 && !pending[ph.chain]:
			report.Action = ActionAdopt
			report.Detail = "schema already present; recorded without replaying the file"
		case len(missing) == 0 && ph.idempotent:
			report.Action = ActionApply
		case len(missing) == 0:
			insp.problems = append(insp.problems, fmt.Sprintf("%s: its schema exists although an earlier %s migration is missing (out-of-order or hand-edited database)", ph.name, ph.chain))
			report.Action = ActionApply
		case ph.idempotent:
			report.Action = ActionApply
		default:
			insp.problems = append(insp.problems, fmt.Sprintf("%s (%s) is only partially present: found [%s], missing [%s]; refusing to replay or skip it (a same-named object may belong to something else, or an earlier upgrade was stopped halfway)", ph.name, report.File, strings.Join(holds, "; "), strings.Join(missing, "; ")))
			report.Action = ActionApply
		}
		if report.Action == ActionApply && !ph.idempotent {
			pending[ph.chain] = true
		}
		actions[ph.name] = report.Action
		insp.reports = append(insp.reports, report)
	}
	// The one-time link import only matters while the media migration still runs.
	_, recordedImport := history["media-entry-link-import"]
	for i := range insp.reports {
		r := &insp.reports[i]
		if r.Name != "media-entry-link-import" {
			continue
		}
		if p.LinkImport != "" {
			r.File = filepath.Base(p.LinkImport)
		}
		switch {
		case recordedImport:
			r.Action = ActionRecorded
		case p.LinkImport == "":
			r.Action, r.Detail = ActionSkip, "no -link-import file given"
		case actions["tripmap-media"] != ActionApply:
			r.Action, r.Detail = ActionSkip, "media links already exist; import not needed"
		default:
			if _, sumErr := fileChecksum(p.LinkImport); sumErr != nil {
				insp.problems = append(insp.problems, fmt.Sprintf("media-entry-link-import: %v", sumErr))
			}
			r.Action = ActionApply
		}
	}

	if err = p.checkData(ctx, q, insp, actions); err != nil {
		return nil, err
	}
	// Row counts of every table that exists now; verified again before commit.
	rows, err := q.QueryContext(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' AND table_name <> 'shared_migration_history'`)
	if err != nil {
		return nil, err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		tables = append(tables, name)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, name := range tables {
		n, countErr := countRows(ctx, q, name)
		if countErr != nil {
			return nil, countErr
		}
		insp.before[name] = n
	}
	return insp, nil
}

// checkData holds the refusals that depend on existing rows rather than on schema shape.
func (p SharedPlan) checkData(ctx context.Context, q queryer, insp *inspection, actions map[string]Action) error {
	if actions["timeline-refs"] == ActionApply {
		var available bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pgcrypto') OR EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'pgcrypto')`).Scan(&available); err != nil {
			return err
		}
		if !available {
			insp.problems = append(insp.problems, "timeline-refs needs the pgcrypto extension, which this server does not provide (install postgresql-contrib or ask the DBA to CREATE EXTENSION pgcrypto)")
		}
	}
	if actions["tripmap-media"] == ActionApply && p.LinkImport == "" {
		has, err := tableExists(ctx, q, "media_objects")
		if err != nil {
			return err
		}
		if has {
			var unlinked int64
			links, err := tableExists(ctx, q, "media_entry_links")
			if err != nil {
				return err
			}
			query := `SELECT count(*) FROM media_objects`
			if links {
				query = `SELECT count(*) FROM media_objects m LEFT JOIN media_entry_links l ON l.media_id = m.id AND l.user_id = m.user_id WHERE l.media_id IS NULL`
			}
			if err = q.QueryRowContext(ctx, query).Scan(&unlinked); err != nil {
				return err
			}
			if unlinked > 0 {
				insp.problems = append(insp.problems, fmt.Sprintf("%d existing Tripmap media row(s) have no Timeline entry link; generate the one-time link import (deploy/sql/media_entry_links.sql template) and pass it with -link-import", unlinked))
			}
		}
	}
	if !p.AcceptRetireLegacySessions {
		retire := false
		for _, ph := range p.phases() {
			if strings.HasSuffix(ph.path, "_retire_tripmap_sessions.sql") && actions[ph.name] == ActionApply {
				retire = true
			}
		}
		if retire {
			for _, table := range []string{"auth_sessions", "account_deletion_receipts"} {
				has, err := tableExists(ctx, q, table)
				if err != nil {
					return err
				}
				if !has {
					continue
				}
				n, err := countRows(ctx, q, table)
				if err != nil {
					return err
				}
				if n > 0 {
					insp.problems = append(insp.problems, fmt.Sprintf("the retire_tripmap_sessions increment would drop %s, which holds %d row(s); import legacy accounts first and pass -accept-retire-legacy-sessions to confirm the drop", table, n))
				}
			}
		}
	}
	return nil
}

// Check reports what Apply would do, in a read-only transaction. It never writes.
func (p SharedPlan) Check(ctx context.Context, db *sql.DB) ([]PhaseReport, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	insp, err := p.inspect(ctx, tx)
	if err != nil {
		return nil, err
	}
	if len(insp.problems) > 0 {
		return insp.reports, &RefusedError{Problems: insp.problems}
	}
	return insp.reports, nil
}

func (p SharedPlan) complete() error {
	if p.TimelineBaseline == "" || p.TripmapBaseline == "" || p.TimelineRefs == "" || p.LinkStaging == "" || p.TripmapMedia == "" {
		return fmt.Errorf("shared migration plan is incomplete")
	}
	return nil
}

// Apply upgrades or installs the shared schema in one transaction. Nothing is
// written when the inspection refuses; a failed phase, import, or verification
// rolls back every earlier phase. A second run finds every phase recorded and
// changes nothing.
func (p SharedPlan) Apply(ctx context.Context, db *sql.DB) error {
	_, err := p.apply(ctx, db, nil)
	return err
}

// ApplyReport is Apply that also returns what happened to each phase.
func (p SharedPlan) ApplyReport(ctx context.Context, db *sql.DB) ([]PhaseReport, error) {
	return p.apply(ctx, db, nil)
}

// ApplyFromTimelineDB is the cross-database to same-database path. db is the
// shared (Tripmap) database that has no Timeline schema yet; src is the
// standalone Timeline database at the current Timeline version. The Timeline
// rows are copied inside the same transaction and verified table by table with
// row counts and content digests. src is only read.
func (p SharedPlan) ApplyFromTimelineDB(ctx context.Context, db, src *sql.DB) ([]PhaseReport, error) {
	if src == nil {
		return nil, errors.New("source Timeline database is required")
	}
	return p.apply(ctx, db, src)
}

func (p SharedPlan) apply(ctx context.Context, db, src *sql.DB) ([]PhaseReport, error) {
	if err := p.complete(); err != nil {
		return nil, err
	}
	var srcTx *sql.Tx
	if src != nil {
		var err error
		srcTx, err = src.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			return nil, err
		}
		defer srcTx.Rollback()
		if err = p.checkTimelineSource(ctx, srcTx); err != nil {
			return nil, err
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// One migrator at a time; the inspection below runs under the lock.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('kongken.shared_migration'))`); err != nil {
		return nil, fmt.Errorf("lock shared migration: %w", err)
	}
	insp, err := p.inspect(ctx, tx)
	if err != nil {
		return nil, err
	}
	if src != nil {
		for _, ph := range p.phases() {
			if ph.chain != chainTimeline {
				continue
			}
			for _, r := range insp.reports {
				if r.Name == ph.name && r.Action != ActionApply {
					insp.problems = append(insp.problems, fmt.Sprintf("the target database already has the Timeline schema (%s is %s); merging a second Timeline database into it is not supported, use an empty Timeline side or restore from backup", r.Name, r.Action))
				}
			}
		}
		for table := range insp.before {
			if strings.HasPrefix(table, "timeline_") {
				insp.problems = append(insp.problems, fmt.Sprintf("the target database already contains %s", table))
			}
		}
	}
	if len(insp.problems) > 0 {
		return insp.reports, &RefusedError{Problems: insp.problems}
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS shared_migration_history (name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return nil, fmt.Errorf("create shared migration history: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `ALTER TABLE shared_migration_history ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT 'applied', ADD COLUMN IF NOT EXISTS checksum text NOT NULL DEFAULT ''`); err != nil {
		return nil, fmt.Errorf("extend shared migration history: %w", err)
	}
	paths := map[string]string{}
	for _, ph := range p.phases() {
		paths[ph.name] = ph.path
	}
	for _, r := range insp.reports {
		if r.Action != ActionAdopt && r.Action != ActionApply {
			continue
		}
		path := paths[r.Name]
		sum, err := fileChecksum(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", r.Name, err)
		}
		source := "adopted"
		if r.Action == ActionApply {
			body, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", r.Name, err)
			}
			if _, err = tx.ExecContext(ctx, string(body)); err != nil {
				return nil, fmt.Errorf("apply %s: %w", r.Name, err)
			}
			source = "applied"
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO shared_migration_history(name, source, checksum) VALUES ($1, $2, $3)`, r.Name, source, sum); err != nil {
			return nil, fmt.Errorf("record %s: %w", r.Name, err)
		}
	}
	if srcTx != nil {
		if err = copyTimelineData(ctx, srcTx, tx); err != nil {
			return nil, err
		}
	}
	if err = p.verify(ctx, tx, insp); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit shared migration: %w", err)
	}
	return insp.reports, nil
}

// verify runs inside the transaction; a failure rolls the whole upgrade back.
func (p SharedPlan) verify(ctx context.Context, tx *sql.Tx, insp *inspection) error {
	retired := map[string]bool{}
	for _, ph := range p.phases() {
		if strings.HasSuffix(ph.path, "_retire_tripmap_sessions.sql") {
			retired["auth_sessions"], retired["account_deletion_receipts"] = true, true
		}
	}
	for table, before := range insp.before {
		if retired[table] {
			continue
		}
		after, err := countRows(ctx, tx, table)
		if err != nil {
			return fmt.Errorf("verify %s: %w", table, err)
		}
		// The one-time link import may add Timeline entries; nothing else may change.
		if after < before || (after != before && table != "timeline_entries") {
			return fmt.Errorf("verify: %s had %d rows before the upgrade and %d after; rolled back", table, before, after)
		}
	}
	both, err := tbl("timeline_media_objects").holds(ctx, tx)
	if err != nil {
		return err
	}
	if both {
		var bad int64
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM timeline_media_objects m JOIN timeline_entries e ON e.id = m.entry_id WHERE m.user_id <> e.user_id`).Scan(&bad); err != nil {
			return fmt.Errorf("verify media owners: %w", err)
		}
		if bad > 0 {
			return fmt.Errorf("verify: %d Timeline media row(s) belong to a different account than their entry; rolled back", bad)
		}
	}
	return nil
}

func (p SharedPlan) checkTimelineSource(ctx context.Context, q queryer) error {
	var problems []string
	for _, ph := range p.phases() {
		if ph.chain != chainTimeline {
			continue
		}
		for _, pr := range ph.probes {
			ok, err := pr.holds(ctx, q)
			if err != nil {
				return err
			}
			if !ok {
				problems = append(problems, fmt.Sprintf("source Timeline database is not at the current version: %s needs %s; apply migrations/timeline up to date on the source first", ph.name, pr))
			}
		}
	}
	rows, err := q.QueryContext(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' AND table_name LIKE 'timeline\_%'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	known := map[string]bool{}
	for _, t := range timelineTables {
		known[t] = true
	}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return err
		}
		if !known[name] {
			problems = append(problems, fmt.Sprintf("source Timeline database has table %s that the copy does not know; it would be left behind", name))
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(problems) > 0 {
		return &RefusedError{Problems: problems}
	}
	return nil
}

// timelineTables is the copy order: parents before children.
var timelineTables = []string{"timeline_entries", "timeline_media_objects", "timeline_cleanup_objects", "timeline_mutations", "timeline_categories"}

func tableColumns(ctx context.Context, q queryer, table string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1 ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err = rows.Scan(&c); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

func quoteList(cols []string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = `"` + strings.ReplaceAll(c, `"`, `""`) + `"`
	}
	return strings.Join(quoted, ", ")
}

func tableDigest(ctx context.Context, q queryer, table string, cols []string) (int64, string, error) {
	var n int64
	var digest string
	err := q.QueryRowContext(ctx, `SELECT count(*), coalesce(md5(string_agg(row(`+quoteList(cols)+`)::text, '|' ORDER BY "id")), '') FROM "`+table+`"`).Scan(&n, &digest)
	return n, digest, err
}

// copyTimelineData copies every Timeline table from src to dst and proves the
// copy with row counts and content digests over the source's columns.
func copyTimelineData(ctx context.Context, src, dst *sql.Tx) error {
	for _, table := range timelineTables {
		srcCols, err := tableColumns(ctx, src, table)
		if err != nil {
			return err
		}
		dstCols, err := tableColumns(ctx, dst, table)
		if err != nil {
			return err
		}
		have := map[string]bool{}
		for _, c := range dstCols {
			have[c] = true
		}
		for _, c := range srcCols {
			if !have[c] {
				return fmt.Errorf("copy %s: column %s exists in the source but not in the shared schema; rolled back", table, c)
			}
		}
		placeholders := make([]string, len(srcCols))
		for i := range srcCols {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
		}
		stmt, err := dst.PrepareContext(ctx, `INSERT INTO "`+table+`" (`+quoteList(srcCols)+`) VALUES (`+strings.Join(placeholders, ", ")+`)`)
		if err != nil {
			return fmt.Errorf("copy %s: %w", table, err)
		}
		rows, err := src.QueryContext(ctx, `SELECT `+quoteList(srcCols)+` FROM "`+table+`" ORDER BY "id"`)
		if err != nil {
			stmt.Close()
			return fmt.Errorf("copy %s: %w", table, err)
		}
		for rows.Next() {
			values := make([]any, len(srcCols))
			ptrs := make([]any, len(srcCols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err = rows.Scan(ptrs...); err == nil {
				_, err = stmt.ExecContext(ctx, values...)
			}
			if err != nil {
				rows.Close()
				stmt.Close()
				return fmt.Errorf("copy %s: %w", table, err)
			}
		}
		err = rows.Err()
		rows.Close()
		stmt.Close()
		if err != nil {
			return fmt.Errorf("copy %s: %w", table, err)
		}
		wantN, wantDigest, err := tableDigest(ctx, src, table, srcCols)
		if err != nil {
			return err
		}
		gotN, gotDigest, err := tableDigest(ctx, dst, table, srcCols)
		if err != nil {
			return err
		}
		if wantN != gotN || wantDigest != gotDigest {
			return fmt.Errorf("copy %s: source has %d rows (digest %s), target %d rows (digest %s); rolled back", table, wantN, wantDigest, gotN, gotDigest)
		}
	}
	return nil
}

func DiscoverPlan(root string) SharedPlan {
	tripmapDir := filepath.Join(root, "migrations", "tripmap")
	timelineDir := filepath.Join(root, "migrations", "timeline")
	allTripmap, _ := SQLFiles(tripmapDir)
	var increments []string
	for _, path := range allTripmap {
		base := filepath.Base(path)
		if base > "20261014011442_baseline.sql" && base < "20261107900000_media_entry_links.sql" {
			increments = append(increments, path)
		}
	}
	allTimeline, _ := SQLFiles(timelineDir)
	var timelineIncrements []string
	for _, path := range allTimeline {
		if filepath.Base(path) > "20261107180000_refs_mutations.sql" {
			timelineIncrements = append(timelineIncrements, path)
		}
	}
	return SharedPlan{
		TimelineBaseline:   filepath.Join(timelineDir, "20261107170000_baseline.sql"),
		TripmapBaseline:    filepath.Join(tripmapDir, "20261014011442_baseline.sql"),
		TripmapIncrements:  increments,
		TimelineRefs:       filepath.Join(timelineDir, "20261107180000_refs_mutations.sql"),
		LinkStaging:        filepath.Join(tripmapDir, "20261107900000_media_entry_links.sql"),
		TripmapMedia:       filepath.Join(tripmapDir, "20261108000000_media_delete_receipts.sql"),
		TimelineIncrements: timelineIncrements,
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
