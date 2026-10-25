package account

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/user"
	"github.com/kongken/mono/internal/usersrv/ent/userrole"
	"github.com/kongken/mono/internal/usersrv/identity"
)

// The page sizes of the administrator user list.
const (
	DefaultUserPageSize = 50
	MaxUserPageSize     = 100
)

// AdminUser is an account as an administrator may see it: enough to recognise and
// support the account, and nothing else. There is no password hash, token, session
// or product data in it.
type AdminUser struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	Status      Status
	Roles       []string
	CreatedAt   time.Time
	// AuthVersion is the version a console change must name (optimistic concurrency).
	AuthVersion int64
}

// UserFilter selects accounts. At most one of Email, UserID and Nickname may be set;
// Status and Role narrow the result further.
type UserFilter struct {
	// Email is an exact lookup; it is normalised like every other entry point.
	Email string
	// UserID is an exact lookup by UUID.
	UserID string
	// Nickname is a literal, case-insensitive substring of the display name.
	Nickname string
	// Status is "" for any.
	Status Status
	// Role is "" for any, otherwise a whitelisted role.
	Role string
}

// UserCursor is the position after the last row of a page: created_at then id, both
// descending, which is a total order (the ID breaks ties), so paging neither repeats
// nor skips a row while the list does not change.
type UserCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// UserPage is one page of accounts. Next is nil at the last page.
type UserPage struct {
	Users []AdminUser
	Next  *UserCursor
}

// validate checks the filter and returns the predicates it stands for plus a
// redacted description for the audit row: which filters were used, never their values.
func (f UserFilter) validate() (lookup, canonicalEmail string, id uuid.UUID, err error) {
	set := 0
	if f.Email != "" {
		set++
		lookup = "email"
	}
	if f.UserID != "" {
		set++
		lookup = "user_id"
	}
	if f.Nickname != "" {
		set++
		lookup = "nickname"
	}
	if set > 1 {
		return "", "", uuid.Nil, &identity.ValidationError{Field: "filter", Reason: "use at most one of email, user_id and nickname"}
	}
	switch lookup {
	case "email":
		if canonicalEmail, err = identity.NormalizeEmail(f.Email); err != nil {
			return "", "", uuid.Nil, err
		}
	case "user_id":
		if id, err = uuid.Parse(f.UserID); err != nil || id == uuid.Nil {
			return "", "", uuid.Nil, &identity.ValidationError{Field: "user_id", Reason: "not a UUID"}
		}
	case "nickname":
		if err = checkNickname(f.Nickname); err != nil {
			return "", "", uuid.Nil, err
		}
	}
	switch f.Status {
	case "", StatusActive, StatusDisabled, StatusDeleting:
	default:
		return "", "", uuid.Nil, &identity.ValidationError{Field: "status", Reason: "not a known status"}
	}
	if f.Role != "" {
		if err = checkRole(f.Role); err != nil {
			return "", "", uuid.Nil, err
		}
	}
	return lookup, canonicalEmail, id, nil
}

// checkNickname bounds the search text like a nickname: 1-40 characters, no controls.
func checkNickname(q string) error {
	if !utf8.ValidString(q) || utf8.RuneCountInString(q) < 1 || utf8.RuneCountInString(q) > 40 {
		return &identity.ValidationError{Field: "nickname", Reason: "must be 1-40 characters"}
	}
	for _, r := range q {
		if unicode.IsControl(r) {
			return &identity.ValidationError{Field: "nickname", Reason: "invalid characters"}
		}
	}
	return nil
}

// requireAdmin proves, inside the reading transaction, that the caller is an active
// account that holds the platform role now. It does not depend on the roles the
// credential carried: a revoked or disabled administrator is refused here even
// within the lifetime of a credential signed a moment ago.
func (s *Service) requireAdmin(ctx context.Context, tx *ent.Tx, actor uuid.UUID) error {
	u, err := tx.User.Query().Where(user.ID(actor)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrForbidden
		}
		return err
	}
	if Status(u.Status) != StatusActive {
		return ErrForbidden
	}
	held, err := tx.UserRole.Query().Where(userrole.UserID(actor), userrole.Role(RolePlatformAdmin)).Exist(ctx)
	if err != nil {
		return err
	}
	if !held {
		return ErrForbidden
	}
	return nil
}

func adminActor(actor uuid.UUID) string { return userActor(actor) }

// ListUsers returns one page of accounts to the administrator actor, newest first.
// The read and its audit row (who, what kind of query, which request, how it ended;
// never a filter value) are one transaction: no data is returned without the record.
// size is clamped to [1, MaxUserPageSize] with 0 meaning DefaultUserPageSize.
func (s *Service) ListUsers(ctx context.Context, actor uuid.UUID, requestID string, f UserFilter, size int, after *UserCursor) (UserPage, error) {
	lookup, canonical, id, err := f.validate()
	if err != nil {
		return UserPage{}, err
	}
	switch {
	case size < 0:
		return UserPage{}, &identity.ValidationError{Field: "page_size", Reason: "must not be negative"}
	case size == 0:
		size = DefaultUserPageSize
	case size > MaxUserPageSize:
		size = MaxUserPageSize
	}
	var page UserPage
	var denied bool
	err = s.inTx(ctx, func(tx *ent.Tx) error {
		entry := auditEntry{actor: adminActor(actor), action: "admin.users.list", requestID: requestID, reason: listReason(lookup, f, after != nil)}
		if err := s.requireAdmin(ctx, tx, actor); err != nil {
			if err != ErrForbidden {
				return err
			}
			denied, entry.result = true, "refused_forbidden"
			return s.audit(ctx, tx, entry)
		}
		q := tx.User.Query().WithRoles()
		switch lookup {
		case "email":
			q.Where(user.EmailCanonical(canonical))
		case "user_id":
			q.Where(user.ID(id))
		case "nickname":
			q.Where(user.DisplayNameContainsFold(f.Nickname))
		}
		if f.Status != "" {
			q.Where(user.StatusEQ(user.Status(f.Status)))
		}
		if f.Role != "" {
			q.Where(user.HasRolesWith(userrole.Role(f.Role)))
		}
		if after != nil {
			q.Where(func(sel *sql.Selector) {
				sel.Where(sql.Or(
					sql.LT(sel.C(user.FieldCreatedAt), after.CreatedAt),
					sql.And(sql.EQ(sel.C(user.FieldCreatedAt), after.CreatedAt), sql.LT(sel.C(user.FieldID), after.ID)),
				))
			})
		}
		rows, err := q.Order(ent.Desc(user.FieldCreatedAt), ent.Desc(user.FieldID)).Limit(size + 1).All(ctx)
		if err != nil {
			return err
		}
		if len(rows) > size {
			rows = rows[:size]
			last := rows[len(rows)-1]
			page.Next = &UserCursor{CreatedAt: last.CreatedAt, ID: last.ID}
		}
		page.Users = make([]AdminUser, 0, len(rows))
		for _, u := range rows {
			page.Users = append(page.Users, adminUserOf(u))
		}
		entry.result = "ok"
		return s.audit(ctx, tx, entry)
	})
	if err != nil {
		return UserPage{}, mapStoreError(err)
	}
	if denied {
		return UserPage{}, ErrForbidden
	}
	return page, nil
}

// GetUser reads one account for the administrator actor, with the same audit rule.
func (s *Service) GetUser(ctx context.Context, actor uuid.UUID, requestID string, id uuid.UUID) (AdminUser, error) {
	if id == uuid.Nil {
		return AdminUser{}, &identity.ValidationError{Field: "user_id", Reason: "not a UUID"}
	}
	var out AdminUser
	var refusal error
	err := s.inTx(ctx, func(tx *ent.Tx) error {
		entry := auditEntry{actor: adminActor(actor), action: "admin.users.get", requestID: requestID, userID: &id}
		if err := s.requireAdmin(ctx, tx, actor); err != nil {
			if err != ErrForbidden {
				return err
			}
			refusal, entry.result = ErrForbidden, "refused_forbidden"
			return s.audit(ctx, tx, entry)
		}
		u, err := tx.User.Query().Where(user.ID(id)).WithRoles().Only(ctx)
		if ent.IsNotFound(err) {
			refusal, entry.result = ErrNotFound, "not_found"
			return s.audit(ctx, tx, entry)
		}
		if err != nil {
			return err
		}
		out, entry.result = adminUserOf(u), "ok"
		return s.audit(ctx, tx, entry)
	})
	if err != nil {
		return AdminUser{}, mapStoreError(err)
	}
	if refusal != nil {
		return AdminUser{}, refusal
	}
	return out, nil
}

// listReason is the redacted description of a list query: the kind of lookup and
// the filters that were used, never their values (they can be an email address).
func listReason(lookup string, f UserFilter, paged bool) string {
	parts := []string{"lookup=none"}
	if lookup != "" {
		parts[0] = "lookup=" + lookup
	}
	if f.Status != "" {
		parts = append(parts, "status")
	}
	if f.Role != "" {
		parts = append(parts, "role")
	}
	if paged {
		parts = append(parts, "paged")
	}
	return strings.Join(parts, " ")
}

func adminUserOf(u *ent.User) AdminUser {
	roles := make([]string, 0, len(u.Edges.Roles))
	for _, r := range u.Edges.Roles {
		roles = append(roles, r.Role)
	}
	sort.Strings(roles)
	return AdminUser{ID: u.ID, Email: u.EmailCanonical, DisplayName: u.DisplayName, Status: Status(u.Status), Roles: roles, CreatedAt: u.CreatedAt, AuthVersion: u.AuthVersion}
}
