package account

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/securityaudit"
	"github.com/kongken/mono/internal/usersrv/identity"
)

// The page sizes of the administrator audit list.
const (
	DefaultAuditPageSize = 50
	MaxAuditPageSize     = 100
)

// AuditRecord is one security_audit row as an administrator may see it: who did what
// to which account, why, and how it ended. It holds no email, password, token, session,
// raw request or product data: only identifiers, fixed classifications, the reason the
// operator gave (shown only if the write path vouched for it, see visibleReason, else the
// ReasonHidden marker) and the status and role names before and after.
type AuditRecord struct {
	ID        uuid.UUID
	CreatedAt time.Time
	// Channel says where the actor came from, see the Channel constants.
	Channel string
	// OperatorUserID is the real account behind a web change or read, and the account
	// holder for a self-service event. It is nil for the CLI and the system.
	OperatorUserID *uuid.UUID
	// OperatorLabel is the controlled operator name of a CLI change ("system" for the
	// system). It is never an identity for a web actor.
	OperatorLabel string
	// UserID is the account the row is about; nil when there is none (an anonymous flow, or
	// a target that did not exist).
	UserID    *uuid.UUID
	Action    string
	Result    string
	Reason    string
	RequestID string
	// MutationID is the idempotency key of a console change. Keys are reusable after their
	// receipt expires and refused attempts may use any key, so it is not a unique event name.
	MutationID                string
	BeforeStatus, AfterStatus string
	BeforeRoles, AfterRoles   []string
	// OriginID is the exact link of a replayed row to the row of the change it answered
	// again, nil if there is none (a replay of a receipt that predates the link). Origin is
	// that row when it is still kept.
	OriginID *uuid.UUID
	Origin   *AuditOrigin
}

// AuditOrigin is the summary of the original change a replay points at. It holds fixed
// classifications only and no free text (no reason, no operator label, no request ID), so
// nothing in it can carry a secret; a test pins that list of fields.
type AuditOrigin struct {
	ID                        uuid.UUID
	CreatedAt                 time.Time
	Action, Result            string
	BeforeStatus, AfterStatus string
	BeforeRoles, AfterRoles   []string
}

// AuditFilter selects audit rows. Every field narrows the result; empty means any.
type AuditFilter struct {
	// UserID is the account the rows are about.
	UserID string
	// OperatorID is the web operator (or self-service holder) account.
	OperatorID string
	// Channel is one of the Channel constants.
	Channel string
	// Action and Result are exact classifications as stored (see the audit docs).
	Action, Result string
	// MutationID is an exact idempotency key.
	MutationID string
	// From (inclusive) and To (exclusive) bound created_at; the zero time is unbounded.
	From, To time.Time
}

// AuditCursor is the position after the last row of a page: created_at then id, both
// descending, a total order, so paging neither repeats nor skips a row while the log
// is not being written behind the position.
type AuditCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// AuditPage is one page of audit rows. Next is nil at the last page.
type AuditPage struct {
	Records []AuditRecord
	Next    *AuditCursor
}

var (
	classPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,63}$`)
	mutationPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

// validate checks the filter and returns the parsed UUIDs.
func (f AuditFilter) validate() (user, operator uuid.UUID, err error) {
	if f.UserID != "" {
		if user, err = uuid.Parse(f.UserID); err != nil || user == uuid.Nil {
			return uuid.Nil, uuid.Nil, &identity.ValidationError{Field: "user_id", Reason: "not a UUID"}
		}
	}
	if f.OperatorID != "" {
		if operator, err = uuid.Parse(f.OperatorID); err != nil || operator == uuid.Nil {
			return uuid.Nil, uuid.Nil, &identity.ValidationError{Field: "operator_user_id", Reason: "not a UUID"}
		}
	}
	switch f.Channel {
	case "", ChannelWeb, ChannelCLI, ChannelSelf, ChannelSystem:
	default:
		return uuid.Nil, uuid.Nil, &identity.ValidationError{Field: "channel", Reason: "not a known channel"}
	}
	for field, v := range map[string]string{"action": f.Action, "result": f.Result} {
		if v != "" && !classPattern.MatchString(v) {
			return uuid.Nil, uuid.Nil, &identity.ValidationError{Field: field, Reason: "not a valid classification"}
		}
	}
	if f.MutationID != "" && !mutationPattern.MatchString(f.MutationID) {
		return uuid.Nil, uuid.Nil, &identity.ValidationError{Field: "mutation_id", Reason: "must be 1-64 characters of A-Z a-z 0-9 . _ -"}
	}
	if !f.From.IsZero() && !f.To.IsZero() && !f.From.Before(f.To) {
		return uuid.Nil, uuid.Nil, &identity.ValidationError{Field: "time_range", Reason: "from must be before to"}
	}
	return user, operator, nil
}

// reason is the redacted description of an audit query for the audit row of the read
// itself: which filters were used, never their values.
func (f AuditFilter) reason(paged bool) string {
	var used []string
	for _, p := range []struct {
		name string
		set  bool
	}{
		{"user", f.UserID != ""}, {"operator", f.OperatorID != ""}, {"channel", f.Channel != ""}, {"action", f.Action != ""},
		{"result", f.Result != ""}, {"mutation", f.MutationID != ""}, {"time", !f.From.IsZero() || !f.To.IsZero()},
	} {
		if p.set {
			used = append(used, p.name)
		}
	}
	text := "filters=none"
	if len(used) > 0 {
		text = "filters=" + strings.Join(used, ",")
	}
	if paged {
		text += " paged"
	}
	return text
}

// ListAudit returns one page of the account audit to the administrator actor, newest
// first. The read and its own audit row (action admin.audit.list, a redacted description
// of the filters) are one transaction: nothing is returned without the record, and a
// caller who no longer holds the platform role gets nothing.
func (s *Service) ListAudit(ctx context.Context, actor uuid.UUID, requestID string, f AuditFilter, size int, after *AuditCursor) (AuditPage, error) {
	user, operator, err := f.validate()
	if err != nil {
		return AuditPage{}, err
	}
	switch {
	case size < 0:
		return AuditPage{}, &identity.ValidationError{Field: "page_size", Reason: "must not be negative"}
	case size == 0:
		size = DefaultAuditPageSize
	case size > MaxAuditPageSize:
		size = MaxAuditPageSize
	}
	var page AuditPage
	var denied bool
	err = s.inTx(ctx, func(tx *ent.Tx) error {
		entry := auditEntry{actor: adminActor(actor), actorUser: &actor, channel: ChannelWeb, action: "admin.audit.list", requestID: requestID, reason: f.reason(after != nil)}
		if err := s.requireAdmin(ctx, tx, actor); err != nil {
			if err != ErrForbidden {
				return err
			}
			denied, entry.result = true, "refused_forbidden"
			return s.audit(ctx, tx, entry)
		}
		q := tx.SecurityAudit.Query()
		if user != uuid.Nil {
			q.Where(securityaudit.UserID(user))
		}
		if operator != uuid.Nil {
			// A web change names its operator in actor_user_id; a self-service row names the
			// holder as the account it is about.
			q.Where(securityaudit.Or(
				securityaudit.ActorUserID(operator),
				securityaudit.And(securityaudit.ChannelEQ(ChannelSelf), securityaudit.UserID(operator)),
			))
		}
		if f.Channel != "" {
			q.Where(securityaudit.ChannelEQ(f.Channel))
		}
		if f.Action != "" {
			q.Where(securityaudit.Action(f.Action))
		}
		if f.Result != "" {
			q.Where(securityaudit.Result(f.Result))
		}
		if f.MutationID != "" {
			q.Where(securityaudit.MutationID(f.MutationID))
		}
		if !f.From.IsZero() {
			q.Where(securityaudit.CreatedAtGTE(f.From))
		}
		if !f.To.IsZero() {
			q.Where(securityaudit.CreatedAtLT(f.To))
		}
		if after != nil {
			q.Where(func(sel *sql.Selector) {
				sel.Where(sql.Or(
					sql.LT(sel.C(securityaudit.FieldCreatedAt), after.CreatedAt),
					sql.And(sql.EQ(sel.C(securityaudit.FieldCreatedAt), after.CreatedAt), sql.LT(sel.C(securityaudit.FieldID), after.ID)),
				))
			})
		}
		rows, err := q.Order(ent.Desc(securityaudit.FieldCreatedAt), ent.Desc(securityaudit.FieldID)).Limit(size + 1).All(ctx)
		if err != nil {
			return err
		}
		if len(rows) > size {
			rows = rows[:size]
			last := rows[len(rows)-1]
			page.Next = &AuditCursor{CreatedAt: last.CreatedAt, ID: last.ID}
		}
		origins, err := s.auditOrigins(ctx, tx, rows)
		if err != nil {
			return err
		}
		page.Records = make([]AuditRecord, 0, len(rows))
		for _, r := range rows {
			page.Records = append(page.Records, auditRecordOf(r, origins))
		}
		entry.result = "ok"
		return s.audit(ctx, tx, entry)
	})
	if err != nil {
		return AuditPage{}, mapStoreError(err)
	}
	if denied {
		return AuditPage{}, ErrForbidden
	}
	return page, nil
}

// auditOrigins loads the rows that replayed rows point at, in one query. A row is only
// accepted as an origin if it really is the same operator's change under the same key:
// the link is by ID, and this is the guard that it can never show a stranger's row.
func (s *Service) auditOrigins(ctx context.Context, tx *ent.Tx, rows []*ent.SecurityAudit) (map[uuid.UUID]*ent.SecurityAudit, error) {
	var ids []uuid.UUID
	for _, r := range rows {
		if r.OriginID != nil {
			ids = append(ids, *r.OriginID)
		}
	}
	out := map[uuid.UUID]*ent.SecurityAudit{}
	if len(ids) == 0 {
		return out, nil
	}
	found, err := tx.SecurityAudit.Query().Where(securityaudit.IDIn(ids...)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, o := range found {
		out[o.ID] = o
	}
	return out, nil
}

func auditRecordOf(r *ent.SecurityAudit, origins map[uuid.UUID]*ent.SecurityAudit) AuditRecord {
	rec := AuditRecord{
		ID: r.ID, CreatedAt: r.CreatedAt, Channel: r.Channel, UserID: r.UserID,
		Action: r.Action, Result: r.Result, Reason: visibleReason(r), RequestID: safeText(r.RequestID, 64), MutationID: r.MutationID,
		BeforeStatus: r.BeforeStatus, AfterStatus: r.AfterStatus,
		BeforeRoles: splitRoles(r.BeforeRoles), AfterRoles: splitRoles(r.AfterRoles), OriginID: r.OriginID,
	}
	switch r.Channel {
	case ChannelWeb:
		rec.OperatorUserID = r.ActorUserID
	case ChannelSelf:
		rec.OperatorUserID = r.UserID
	case ChannelCLI:
		rec.OperatorLabel = safeText(r.Actor, 64)
	case ChannelSystem:
		rec.OperatorLabel = "system"
	}
	if r.OriginID != nil {
		if o := origins[*r.OriginID]; o != nil && o.MutationID == r.MutationID && sameUser(o.ActorUserID, r.ActorUserID) && sameUser(o.UserID, r.UserID) && o.Result != "replayed" {
			rec.Origin = &AuditOrigin{ID: o.ID, CreatedAt: o.CreatedAt, Action: o.Action, Result: o.Result,
				BeforeStatus: o.BeforeStatus, AfterStatus: o.AfterStatus, BeforeRoles: splitRoles(o.BeforeRoles), AfterRoles: splitRoles(o.AfterRoles)}
		}
	}
	return rec
}

// ReasonHidden is all a reader gets of a reason that may not be shown. The row keeps the text; this
// is what the read layer returns instead, so no caller above it ever holds the original.
const ReasonHidden = "[hidden]"

// visibleReason is the one rule for the free text of an audit row, applied to every row the read
// layer returns. The text is shown only if the write path vouched for it (reason_checked: it passed
// the content check or was generated by code) and it still passes that check now; history that was
// never checked cannot be judged afterwards, so it is hidden whole rather than guessed at. The nested
// origin of a replay carries no free text at all, by construction (see AuditOrigin).
func visibleReason(r *ent.SecurityAudit) string {
	if r.Reason == "" {
		return ""
	}
	if !r.ReasonChecked || checkReason(r.Reason) != nil {
		return ReasonHidden
	}
	return r.Reason
}

func sameUser(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// safeText clips text that comes from outside the audit schema's own vocabulary (a request
// ID, a CLI operator name) and drops anything that is not printable, so a row can never
// carry markup-breaking or control content to the console.
func safeText(s string, max int) string {
	if !utf8.ValidString(s) {
		return ""
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		if !unicode.IsPrint(r) {
			continue
		}
		if n == max {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
