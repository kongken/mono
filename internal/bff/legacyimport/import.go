// Package legacyimport copies tripmap users.legacy credential columns into the BFF auth database.
package legacyimport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Row is one legacy tripmap user row eligible for import.
type Row struct {
	ID                  string
	Username            sql.NullString
	DisplayName         string
	AvatarURL           string
	Email               sql.NullString
	Provider            string
	ExternalID          string
	PasswordHash        string
	Disabled            bool
	DeletionRequestedAt sql.NullTime
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// Report summarizes an import run.
type Report struct {
	Scanned       int
	Inserted      int
	Updated       int
	Unchanged     int
	SkippedNoCred int
	Errors        []string
}

func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "scanned=%d inserted=%d updated=%d unchanged=%d skipped_no_cred=%d errors=%d",
		r.Scanned, r.Inserted, r.Updated, r.Unchanged, r.SkippedNoCred, len(r.Errors))
	for _, e := range r.Errors {
		fmt.Fprintf(&b, "\n  - %s", e)
	}
	return b.String()
}

// Import copies legacy credential fields from tripmap users into auth_users.
func Import(ctx context.Context, tripmapDSN, bffDSN string, dryRun bool) (Report, error) {
	if strings.TrimSpace(tripmapDSN) == "" || strings.TrimSpace(bffDSN) == "" {
		return Report{}, errors.New("tripmap and bff DSN required")
	}
	trip, err := sql.Open("pgx", tripmapDSN)
	if err != nil {
		return Report{}, err
	}
	defer trip.Close()
	bff, err := sql.Open("pgx", bffDSN)
	if err != nil {
		return Report{}, err
	}
	defer bff.Close()
	if err := trip.PingContext(ctx); err != nil {
		return Report{}, fmt.Errorf("tripmap db: %w", err)
	}
	if err := bff.PingContext(ctx); err != nil {
		return Report{}, fmt.Errorf("bff db: %w", err)
	}

	rows, err := trip.QueryContext(ctx, `
SELECT id, username, display_name, avatar_url, email, provider, external_id, password_hash,
       disabled, deletion_requested_at, created_at, updated_at
FROM users
ORDER BY id`)
	if err != nil {
		return Report{}, err
	}
	defer rows.Close()

	var rep Report
	for rows.Next() {
		var row Row
		if err := rows.Scan(&row.ID, &row.Username, &row.DisplayName, &row.AvatarURL, &row.Email,
			&row.Provider, &row.ExternalID, &row.PasswordHash, &row.Disabled, &row.DeletionRequestedAt,
			&row.CreatedAt, &row.UpdatedAt); err != nil {
			return rep, err
		}
		rep.Scanned++
		if !hasCredential(row) {
			rep.SkippedNoCred++
			continue
		}
		username := row.Username
		if !username.Valid && row.Email.Valid && row.Email.String != "" {
			username = sql.NullString{String: row.Email.String, Valid: true}
		}
		action, err := upsert(ctx, bff, dryRun, row, username)
		if err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", row.ID, err))
			continue
		}
		switch action {
		case "insert":
			rep.Inserted++
		case "update":
			rep.Updated++
		default:
			rep.Unchanged++
		}
	}
	return rep, rows.Err()
}

func hasCredential(row Row) bool {
	if row.PasswordHash != "" {
		return true
	}
	if row.Provider != "" && row.ExternalID != "" {
		return true
	}
	if row.Disabled || row.DeletionRequestedAt.Valid {
		return true
	}
	return false
}

func upsert(ctx context.Context, bff *sql.DB, dryRun bool, row Row, username sql.NullString) (string, error) {
	var existing struct {
		username, display, avatar, provider, external, hash string
		disabled                                            bool
		del                                                 sql.NullTime
	}
	err := bff.QueryRowContext(ctx, `
SELECT COALESCE(username,''), display_name, avatar_url, provider, external_id, password_hash, disabled, deletion_requested_at
FROM auth_users WHERE id=$1`, row.ID).Scan(
		&existing.username, &existing.display, &existing.avatar, &existing.provider, &existing.external,
		&existing.hash, &existing.disabled, &existing.del)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	wantUser := usernameString(username)
	wantDisplay := row.DisplayName
	wantAvatar := row.AvatarURL
	wantProvider := row.Provider
	wantExternal := row.ExternalID
	wantHash := row.PasswordHash
	wantDisabled := row.Disabled
	wantDel := row.DeletionRequestedAt

	if err == nil && same(existing.username, wantUser) && existing.display == wantDisplay &&
		existing.avatar == wantAvatar && existing.provider == wantProvider && existing.external == wantExternal &&
		existing.hash == wantHash && existing.disabled == wantDisabled && nullTimeEq(existing.del, wantDel) {
		return "unchanged", nil
	}
	if dryRun {
		if errors.Is(err, sql.ErrNoRows) {
			return "insert", nil
		}
		return "update", nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		_, err = bff.ExecContext(ctx, `
INSERT INTO auth_users (id, username, display_name, avatar_url, provider, external_id, password_hash,
  disabled, deletion_requested_at, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			row.ID, nullString(username), wantDisplay, wantAvatar, wantProvider, wantExternal, wantHash,
			wantDisabled, wantDel, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return "", err
		}
		return "insert", nil
	}
	_, err = bff.ExecContext(ctx, `
UPDATE auth_users SET username=$2, display_name=$3, avatar_url=$4, provider=$5, external_id=$6,
  password_hash=$7, disabled=$8, deletion_requested_at=$9, updated_at=$10
WHERE id=$1`,
		row.ID, nullString(username), wantDisplay, wantAvatar, wantProvider, wantExternal, wantHash,
		wantDisabled, wantDel, row.UpdatedAt)
	if err != nil {
		return "", err
	}
	return "update", nil
}

func usernameString(u sql.NullString) string {
	if u.Valid {
		return u.String
	}
	return ""
}

func nullString(u sql.NullString) any {
	if u.Valid {
		return u.String
	}
	return nil
}

func nullTimeEq(a sql.NullTime, b sql.NullTime) bool {
	if !a.Valid && !b.Valid {
		return true
	}
	if a.Valid != b.Valid {
		return false
	}
	return a.Time.Equal(b.Time)
}

func same(a, b string) bool { return a == b }
