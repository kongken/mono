package account

import (
	"context"
	"crypto/sha256"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/adminmutation"
	"github.com/kongken/mono/internal/usersrv/ent/user"
	"github.com/kongken/mono/internal/usersrv/ent/userrole"
	"github.com/kongken/mono/internal/usersrv/identity"
)

// AdminMutationTTL is how long the receipt of a console change answers retries.
const AdminMutationTTL = 24 * time.Hour

// AdminAction is one of the four changes the administration console may make to an
// ordinary account. There is deliberately nothing else: no free role name, no field
// list, no password, no deletion, no platform role.
type AdminAction string

const (
	ActionDisable          AdminAction = "disable"
	ActionEnable           AdminAction = "enable"
	ActionGrantMetroAdmin  AdminAction = "grant_metro_admin"
	ActionRevokeMetroAdmin AdminAction = "revoke_metro_admin"
)

// auditAction is the action name in security_audit.
func (a AdminAction) auditAction() string {
	switch a {
	case ActionDisable:
		return "admin.account.disable"
	case ActionEnable:
		return "admin.account.enable"
	case ActionGrantMetroAdmin:
		return "admin.role.grant:" + RoleMetroAdmin
	case ActionRevokeMetroAdmin:
		return "admin.role.revoke:" + RoleMetroAdmin
	}
	return ""
}

// AdminChange is one console request. Operator is the account of the signed-in
// administrator as the trusted session and credential know it; it is never read
// from the browser's body.
type AdminChange struct {
	Operator uuid.UUID
	Action   AdminAction
	Target   uuid.UUID
	// ExpectedVersion is the target's auth_version as the administrator saw it.
	ExpectedVersion int64
	// MutationID is the idempotency key, unique per operator for AdminMutationTTL.
	MutationID string
	Reason     string
	RequestID  string
}

// AdminResult is the target after the change, or as the first answer left it when
// Replayed is set. Changed is false when the account was already in the requested
// state: the audit row and receipt were written, auth_version did not move.
type AdminResult struct {
	ChangeResult
	Replayed bool
}

func (c AdminChange) validate() (string, error) {
	if c.Operator == uuid.Nil {
		return "", ErrForbidden
	}
	if c.Target == uuid.Nil {
		return "", &identity.ValidationError{Field: "user_id", Reason: "not a UUID"}
	}
	if c.auditAction() == "" {
		return "", &identity.ValidationError{Field: "action", Reason: "not a known action"}
	}
	if c.ExpectedVersion < 1 {
		return "", &identity.ValidationError{Field: "expected_auth_version", Reason: "must be at least 1"}
	}
	mutation, err := identity.NormalizeMutationID(c.MutationID)
	if err != nil {
		return "", err
	}
	if len(c.RequestID) > 64 {
		return "", &identity.ValidationError{Field: "request_id", Reason: "too long"}
	}
	if err := checkReason(strings.TrimSpace(c.Reason)); err != nil {
		return "", err
	}
	return mutation, nil
}

func (c AdminChange) auditAction() string { return c.Action.auditAction() }

// hash is the content of the request that a retry must repeat exactly: what, to
// whom, as of which version, and the reason. The operator is the receipt's key and
// the mutation ID is its name, so neither is part of the content.
func (c AdminChange) hash() []byte {
	h := sha256.Sum256([]byte(strings.Join([]string{string(c.Action), c.Target.String(), strconv.FormatInt(c.ExpectedVersion, 10),
		strings.TrimSpace(c.Reason)}, "\x00")))
	return h[:]
}

// ApplyAdminChange runs one console change. Everything below happens in one
// transaction, in this order:
//
//  1. the operator is checked to be an active holder of the platform role now;
//  2. the target row is locked (the same lock the role CLI and the deletion take, so
//     a concurrent CLI grant of the platform role cannot slip past step 4);
//  3. an unexpired receipt of (operator, mutation ID) answers a retry: the same
//     request gets the first answer back, another request is refused;
//  4. a target that holds the platform role is refused;
//  5. a target whose auth_version is not ExpectedVersion is refused;
//  6. the status or role changes (a deleting account is refused), auth_version
//     advances only when something changed;
//  7. the audit row and the receipt are written.
//
// A refusal is audited as well and leaves no receipt: the retry is evaluated afresh.
func (s *Service) ApplyAdminChange(ctx context.Context, in AdminChange) (AdminResult, error) {
	mutation, err := in.validate()
	if err != nil {
		return AdminResult{}, err
	}
	in.MutationID, in.Reason = mutation, strings.TrimSpace(in.Reason)
	// Two requests of the same operator and key but for different targets lock different
	// rows and may both reach the receipt insert; the unique index lets one win and
	// the retry sees its receipt, so it is refused as a reuse.
	for attempt := 0; ; attempt++ {
		res, err := s.applyAdminChange(ctx, in)
		if err == nil || attempt > 0 || !isReceiptRace(err) {
			return res, err
		}
	}
}

// receiptKey is the unique index that makes (operator, mutation ID) one receipt.
const receiptKey = "adminmutation_operator_id_mutation_id"

func isReceiptRace(err error) bool { return isUniqueViolation(err, receiptKey) }

func (s *Service) applyAdminChange(ctx context.Context, in AdminChange) (AdminResult, error) {
	var res AdminResult
	var refusal error
	err := s.inTx(ctx, func(tx *ent.Tx) error {
		now := s.now()
		operator := in.Operator
		entry := auditEntry{
			actor: adminActor(operator), actorUser: &operator, action: in.auditAction(), requestID: in.RequestID, reason: in.Reason,
			channel: ChannelWeb, mutationID: in.MutationID, userID: &in.Target, id: uuid.New(),
		}
		refuse := func(err error, result string) error {
			refusal, entry.result = err, result
			return s.audit(ctx, tx, entry)
		}
		if err := s.requireAdmin(ctx, tx, operator); err != nil {
			if err != ErrForbidden {
				return err
			}
			return refuse(ErrForbidden, "refused_forbidden")
		}
		u, err := tx.User.Query().Where(user.ID(in.Target)).ForUpdate().Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		missing := ent.IsNotFound(err)

		// An expired receipt no longer answers anything, and must not block the key.
		if _, err = tx.AdminMutation.Delete().Where(adminmutation.OperatorID(operator), adminmutation.MutationID(in.MutationID),
			adminmutation.ExpiresAtLTE(now)).Exec(ctx); err != nil {
			return err
		}
		receipt, err := tx.AdminMutation.Query().Where(adminmutation.OperatorID(operator), adminmutation.MutationID(in.MutationID)).Only(ctx)
		switch {
		case err == nil:
			if string(receipt.RequestHash) != string(in.hash()) {
				return refuse(ErrMutationReused, "refused_mutation_reuse")
			}
			roles := splitRoles(receipt.Roles)
			res = AdminResult{ChangeResult: ChangeResult{Changed: receipt.Changed, Status: Status(receipt.Status),
				AuthVersion: receipt.AuthVersion, Roles: roles}, Replayed: true}
			// A replay only hands the original answer back. Its audit row says so and nothing more: no
			// before/after (the receipt is a snapshot of the first request, not of the account now, and
			// nothing changed). It points at the original row by that row's ID, which is exact: a
			// mutation ID can be used again once its receipt has expired, so (operator, mutation_id)
			// alone is not one event. The original row carries the real before/after values.
			entry.result, entry.originID = "replayed", receipt.AuditID
			return s.audit(ctx, tx, entry)
		case !ent.IsNotFound(err):
			return err
		}

		if missing {
			entry.userID = nil
			return refuse(ErrNotFound, "not_found")
		}
		if entry.beforeRoles, err = roleNames(ctx, tx, u.ID); err != nil {
			return err
		}
		entry.beforeStatus = Status(u.Status)
		entry.afterStatus, entry.afterRoles = entry.beforeStatus, entry.beforeRoles
		for _, r := range entry.beforeRoles {
			if r == RolePlatformAdmin {
				return refuse(ErrTargetProtected, "refused_target_protected")
			}
		}
		if u.AuthVersion != in.ExpectedVersion {
			return refuse(ErrTargetChanged, "refused_version_conflict")
		}
		changed, refused, err := s.applyAction(ctx, tx, u, in)
		if err != nil {
			return err
		}
		if refused != nil {
			return refuse(refused, "refused_not_active")
		}
		if changed {
			if u, err = tx.User.UpdateOneID(u.ID).AddAuthVersion(1).Save(ctx); err != nil {
				return err
			}
			entry.result = "ok"
		} else {
			entry.result = "unchanged"
		}
		roles, err := roleNames(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		res = AdminResult{ChangeResult: ChangeResult{Changed: changed, Status: Status(u.Status), AuthVersion: u.AuthVersion, Roles: roles}}
		entry.afterStatus, entry.afterRoles = res.Status, roles
		if err = tx.AdminMutation.Create().SetOperatorID(operator).SetMutationID(in.MutationID).SetAction(string(in.Action)).
			SetTargetID(u.ID).SetRequestHash(in.hash()).SetChanged(changed).SetStatus(string(res.Status)).SetRoles(strings.Join(roles, ",")).
			SetAuthVersion(res.AuthVersion).SetAuditID(entry.id).SetCreatedAt(now).SetExpiresAt(now.Add(AdminMutationTTL)).Exec(ctx); err != nil {
			return err
		}
		return s.audit(ctx, tx, entry)
	})
	if err != nil {
		if isReceiptRace(err) {
			return AdminResult{}, err
		}
		return AdminResult{}, mapStoreError(err)
	}
	if refusal != nil {
		return AdminResult{}, refusal
	}
	return res, nil
}

// applyAction performs the change on the locked target. refused is a state the
// action cannot start from; a deleting account is refused by every action, so the
// console can neither enable it (which would cancel the deletion) nor edit it.
func (s *Service) applyAction(ctx context.Context, tx *ent.Tx, u *ent.User, in AdminChange) (changed bool, refused error, err error) {
	if Status(u.Status) == StatusDeleting {
		return false, ErrNotActive, nil
	}
	switch in.Action {
	case ActionDisable, ActionEnable:
		to := user.StatusDisabled
		if in.Action == ActionEnable {
			to = user.StatusActive
		}
		if u.Status == to {
			return false, nil, nil
		}
		return true, nil, tx.User.UpdateOneID(u.ID).SetStatus(to).Exec(ctx)
	case ActionGrantMetroAdmin:
		if Status(u.Status) != StatusActive || u.EmailVerifiedAt.IsZero() {
			return false, ErrNotActive, nil
		}
		held, err := tx.UserRole.Query().Where(userrole.UserID(u.ID), userrole.Role(RoleMetroAdmin)).Exist(ctx)
		if err != nil || held {
			return false, nil, err
		}
		err = tx.UserRole.Create().SetUserID(u.ID).SetRole(RoleMetroAdmin).SetGrantedAt(s.now()).SetGrantedBy(adminActor(in.Operator)).Exec(ctx)
		return err == nil, nil, err
	case ActionRevokeMetroAdmin:
		n, err := tx.UserRole.Delete().Where(userrole.UserID(u.ID), userrole.Role(RoleMetroAdmin)).Exec(ctx)
		return n > 0, nil, err
	}
	return false, nil, &identity.ValidationError{Field: "action", Reason: "not a known action"}
}

func splitRoles(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}
