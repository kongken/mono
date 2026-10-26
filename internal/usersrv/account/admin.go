package account

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/user"
	"github.com/kongken/mono/internal/usersrv/ent/userrole"
	"github.com/kongken/mono/internal/usersrv/identity"
)

// RoleMetroAdmin is the role of the metro catalogue administrators.
const RoleMetroAdmin = "metro-admin"

// RolePlatformAdmin is the platform administrator: it opens the user section of the
// unified admin console. It is independent of RoleMetroAdmin (neither implies the
// other) and is granted and revoked only through the operator CLI (the console never
// touches it, nor the accounts that hold it). Registration never
// grants it, whoever registers first, and an account that holds it cannot close itself
// from the web (see BeginDeletion).
const RolePlatformAdmin = "ozid-admin"

// Roles is the whitelist of grantable roles.
var Roles = []string{RoleMetroAdmin, RolePlatformAdmin}

// Operator identifies who runs an operator-CLI change and why. Both are recorded
// in the audit row of the change, whatever its result. The CLI is the only path to
// the platform role; the console's narrower path is ApplyAdminChange. No user
// becomes an administrator because of who registered first.
type Operator struct {
	// Name is a short identifier of the person or ticket, such as "alice" or "ops-1234".
	Name string
	// Reason is a short free text. It must not contain an email address.
	Reason string
	// RequestID correlates the audit row with the CLI invocation.
	RequestID string
}

var (
	emailLike = regexp.MustCompile(`[^\s@]+@[^\s@]+\.[^\s@]+`)
	tokenLike = regexp.MustCompile(`[A-Za-z0-9_+/=-]{32,}`)
)

// checkReason bounds the free-text reason of an administrative change: 1-200
// characters, no control characters, and nothing that looks like a secret or a
// person's address. The audit row keeps the text for as long as the audit
// retention, so an email, a password or a token must never get in.
func checkReason(reason string) error {
	if !utf8.ValidString(reason) || utf8.RuneCountInString(strings.TrimSpace(reason)) == 0 || utf8.RuneCountInString(reason) > 200 {
		return &identity.ValidationError{Field: "reason", Reason: "must be 1-200 characters"}
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return &identity.ValidationError{Field: "reason", Reason: "invalid characters"}
		}
	}
	if emailLike.MatchString(reason) || tokenLike.MatchString(reason) || strings.Contains(reason, "mcp_") {
		return &identity.ValidationError{Field: "reason", Reason: "must not contain an email address, password or token"}
	}
	return nil
}

func (o Operator) validate() error {
	if o.Name == "" || len(o.Name) > 64 {
		return &identity.ValidationError{Field: "operator", Reason: "must be 1-64 characters"}
	}
	for i := 0; i < len(o.Name); i++ {
		c := o.Name[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c == '@' || c == ':') {
			return &identity.ValidationError{Field: "operator", Reason: "invalid characters"}
		}
	}
	if err := checkReason(o.Reason); err != nil {
		return err
	}
	if len(o.RequestID) > 64 {
		return &identity.ValidationError{Field: "request_id", Reason: "too long"}
	}
	return nil
}

// ChangeResult is the account state after an administrative change. Changed is
// false when the account was already in the requested state: nothing was
// written except the audit row, and auth_version did not move.
type ChangeResult struct {
	Changed     bool
	Status      Status
	AuthVersion int64
	Roles       []string
}

// ResolveEmail finds the account of an address, for the CLI. The address is
// normalized like every other entry point.
func (s *Service) ResolveEmail(ctx context.Context, email string) (uuid.UUID, error) {
	canonical, err := identity.NormalizeEmail(email)
	if err != nil {
		return uuid.Nil, err
	}
	u, err := s.db.User.Query().Where(user.EmailCanonical(canonical)).Only(ctx)
	if err != nil {
		return uuid.Nil, notFoundOr(err)
	}
	return u.ID, nil
}

// GrantRole gives a whitelisted role to a verified, active account and bumps
// auth_version, so every older session and management token fails its next check.
func (s *Service) GrantRole(ctx context.Context, op Operator, id uuid.UUID, role string) (ChangeResult, error) {
	if err := checkRole(role); err != nil {
		return ChangeResult{}, err
	}
	return s.adminChange(ctx, op, id, "role.grant:"+role, func(tx *ent.Tx, u *ent.User) (bool, error, error) {
		if Status(u.Status) != StatusActive || u.EmailVerifiedAt.IsZero() {
			return false, ErrNotActive, nil
		}
		exists, err := tx.UserRole.Query().Where(userrole.UserID(u.ID), userrole.Role(role)).Exist(ctx)
		if err != nil || exists {
			return false, nil, err
		}
		err = tx.UserRole.Create().SetUserID(u.ID).SetRole(role).SetGrantedAt(s.now()).SetGrantedBy(op.Name).Exec(ctx)
		return err == nil, nil, err
	})
}

// RevokeRole removes a role from an account in any state and bumps auth_version
// when the role was held. Granting the role again later does not bring back
// tokens issued before the revocation.
func (s *Service) RevokeRole(ctx context.Context, op Operator, id uuid.UUID, role string) (ChangeResult, error) {
	if err := checkRole(role); err != nil {
		return ChangeResult{}, err
	}
	return s.adminChange(ctx, op, id, "role.revoke:"+role, func(tx *ent.Tx, u *ent.User) (bool, error, error) {
		n, err := tx.UserRole.Delete().Where(userrole.UserID(u.ID), userrole.Role(role)).Exec(ctx)
		return n > 0, nil, err
	})
}

// SetDisabled disables an active account or enables a disabled one. A deleting
// account is refused. Both directions bump auth_version, so enabling never
// brings back an older session.
func (s *Service) SetDisabled(ctx context.Context, op Operator, id uuid.UUID, disabled bool) (ChangeResult, error) {
	action, from, to := "account.enable", user.StatusDisabled, user.StatusActive
	if disabled {
		action, from, to = "account.disable", user.StatusActive, user.StatusDisabled
	}
	return s.adminChange(ctx, op, id, action, func(tx *ent.Tx, u *ent.User) (bool, error, error) {
		switch {
		case u.Status == to:
			return false, nil, nil
		case u.Status != from:
			return false, ErrNotActive, nil
		}
		return true, nil, tx.User.UpdateOneID(u.ID).SetStatus(to).Exec(ctx)
	})
}

func checkRole(role string) error {
	for _, r := range Roles {
		if r == role {
			return nil
		}
	}
	return &identity.ValidationError{Field: "role", Reason: "not a known role"}
}

// adminChange is the shared frame of the administrative operations: lock the
// account, apply the change (bumping auth_version when it changed anything),
// and record operator, reason and result in the audit row of the same
// transaction. A refused or unknown-target attempt is audited too.
func (s *Service) adminChange(ctx context.Context, op Operator, id uuid.UUID, action string,
	apply func(tx *ent.Tx, u *ent.User) (changed bool, refusal error, err error)) (ChangeResult, error) {
	if err := op.validate(); err != nil {
		return ChangeResult{}, err
	}
	var res ChangeResult
	var refusal error
	err := s.inTx(ctx, func(tx *ent.Tx) error {
		entry := auditEntry{actor: op.Name, action: action, requestID: op.RequestID, reason: op.Reason, channel: ChannelCLI}
		u, err := tx.User.Query().Where(user.ID(id)).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			refusal, entry.result = ErrNotFound, "not_found"
			return s.audit(ctx, tx, entry)
		}
		if err != nil {
			return err
		}
		entry.userID = &u.ID
		entry.beforeStatus = Status(u.Status)
		if entry.beforeRoles, err = roleNames(ctx, tx, id); err != nil {
			return err
		}
		changed, ref, err := apply(tx, u)
		if err != nil {
			return err
		}
		if ref != nil {
			refusal, entry.result = ref, "refused_not_active"
			entry.afterStatus, entry.afterRoles = entry.beforeStatus, entry.beforeRoles
			return s.audit(ctx, tx, entry)
		}
		if changed {
			if u, err = tx.User.UpdateOneID(id).AddAuthVersion(1).Save(ctx); err != nil {
				return err
			}
			entry.result = "ok"
		} else {
			entry.result = "unchanged"
		}
		roles, err := roleNames(ctx, tx, id)
		if err != nil {
			return err
		}
		res = ChangeResult{Changed: changed, Status: Status(u.Status), AuthVersion: u.AuthVersion, Roles: roles}
		entry.afterStatus, entry.afterRoles = res.Status, roles
		return s.audit(ctx, tx, entry)
	})
	if err != nil {
		return ChangeResult{}, mapStoreError(err)
	}
	if refusal != nil {
		return ChangeResult{}, refusal
	}
	return res, nil
}

// Describe formats an operation result for the CLI.
func (r ChangeResult) Describe() string {
	state := "unchanged"
	if r.Changed {
		state = "changed"
	}
	return fmt.Sprintf("%s: status=%s auth_version=%d roles=%v", state, r.Status, r.AuthVersion, r.Roles)
}

// IsRefusal reports whether err is a refused-but-valid operation.
func IsRefusal(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrNotActive)
}
