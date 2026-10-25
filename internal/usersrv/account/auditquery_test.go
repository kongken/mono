package account_test

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/securityaudit"
	"github.com/kongken/mono/internal/usersrv/identity"
	"github.com/kongken/mono/internal/usersrv/testutil"
)

// listAll reads every page of a filter, newest first, and fails on a repeated row.
func listAll(t *testing.T, s *account.Service, who uuid.UUID, f account.AuditFilter, size int) []account.AuditRecord {
	t.Helper()
	var out []account.AuditRecord
	seen := map[uuid.UUID]bool{}
	var after *account.AuditCursor
	for pages := 0; ; pages++ {
		if pages > 200 {
			t.Fatal("paging does not end")
		}
		page, err := s.ListAudit(ctx, who, "req-audit", f, size, after)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Records {
			if seen[r.ID] {
				t.Fatalf("row %s appears twice", r.ID)
			}
			seen[r.ID] = true
			out = append(out, r)
		}
		if page.Next == nil {
			return out
		}
		after = page.Next
	}
}

func byAction(rows []account.AuditRecord, action string) []account.AuditRecord {
	var out []account.AuditRecord
	for _, r := range rows {
		if r.Action == action {
			out = append(out, r)
		}
	}
	return out
}

func idText(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// The console shows real changes with their before/after, and tells the channels apart: a web
// change names the operator's account, the CLI keeps its controlled operator name and is never
// an account, a holder's own sign-in is self-service.
func TestAuditListShowsRealChangesAndSeparatesTheChannels(t *testing.T) {
	s, db := setup(t)
	admin := platformAdmin(t, s, "root@example.com")
	target := create(t, s, "plain@example.com")
	if _, _, err := s.Login(ctx, "plain@example.com", password, "req-login"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GrantRole(ctx, op, target, account.RoleMetroAdmin); err != nil { // CLI
		t.Fatal(err)
	}
	v := versionOf(t, db, target)
	if _, err := s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, target, v, "k-disable")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, target, v+1, "k-again")); err != nil { // unchanged
		t.Fatal(err)
	}
	if _, err := s.ApplyAdminChange(ctx, req(admin, account.ActionEnable, target, v, "k-stale")); !errors.Is(err, account.ErrTargetChanged) {
		t.Fatal(err)
	}
	if _, err := s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, admin, versionOf(t, db, admin), "k-protected")); !errors.Is(err, account.ErrTargetProtected) {
		t.Fatal(err)
	}
	if _, err := s.ListUsers(ctx, admin, "req-users", account.UserFilter{}, 0, nil); err != nil {
		t.Fatal(err)
	}

	rows := listAll(t, s, admin, account.AuditFilter{UserID: target.String()}, 7)
	find := func(action, result string) account.AuditRecord {
		t.Helper()
		for _, r := range rows {
			if r.Action == action && r.Result == result {
				return r
			}
		}
		t.Fatalf("no %s/%s row among %d", action, result, len(rows))
		return account.AuditRecord{}
	}

	web := find("admin.account.disable", "ok")
	if web.Channel != account.ChannelWeb || idText(web.OperatorUserID) != admin.String() || web.OperatorLabel != "" ||
		web.BeforeStatus != "active" || web.AfterStatus != "disabled" || strings.Join(web.BeforeRoles, ",") != "metro-admin" ||
		strings.Join(web.AfterRoles, ",") != "metro-admin" || web.MutationID != "k-disable" || web.Reason != "support ticket 77" ||
		web.RequestID != "req-k-disable" || web.CreatedAt.IsZero() {
		t.Fatalf("web change: %+v", web)
	}
	unchanged := find("admin.account.disable", "unchanged")
	if unchanged.BeforeStatus != "disabled" || unchanged.AfterStatus != "disabled" {
		t.Fatalf("unchanged: %+v", unchanged)
	}
	if r := find("admin.account.enable", "refused_version_conflict"); r.Channel != account.ChannelWeb || r.BeforeStatus != "disabled" {
		t.Fatalf("version conflict: %+v", r)
	}

	cli := find("role.grant:metro-admin", "ok")
	if cli.Channel != account.ChannelCLI || cli.OperatorUserID != nil || cli.OperatorLabel != "ops-1" || cli.Reason != "ticket 42" ||
		cli.BeforeRoles == nil || len(cli.BeforeRoles) != 0 || strings.Join(cli.AfterRoles, ",") != "metro-admin" {
		t.Fatalf("cli change: %+v", cli)
	}
	// The CLI's free-text operator is never an account: a web filter by that "name" finds nothing, and
	// asking for the web channel does not return CLI rows.
	for _, r := range listAll(t, s, admin, account.AuditFilter{Channel: account.ChannelWeb}, 50) {
		if r.Channel != account.ChannelWeb || r.OperatorUserID == nil {
			t.Fatalf("a non-web row among the web rows: %+v", r)
		}
	}
	for _, r := range listAll(t, s, admin, account.AuditFilter{Channel: account.ChannelCLI}, 50) {
		if r.Channel != account.ChannelCLI || r.OperatorUserID != nil || r.OperatorLabel == "" {
			t.Fatalf("a non-CLI row among the CLI rows: %+v", r)
		}
	}

	login := find("login", "ok")
	if login.Channel != account.ChannelSelf || idText(login.OperatorUserID) != target.String() || login.OperatorLabel != "" {
		t.Fatalf("self-service: %+v", login)
	}

	// The protected target's refusal is about that target, so it is listed under it and classified.
	protected := listAll(t, s, admin, account.AuditFilter{UserID: admin.String(), Result: "refused_target_protected"}, 50)
	if len(protected) != 1 || protected[0].Action != "admin.account.disable" || protected[0].Channel != account.ChannelWeb {
		t.Fatalf("protected refusal: %+v", protected)
	}
	// A read by an administrator is an access record of its own, with a redacted reason.
	reads := byAction(listAll(t, s, admin, account.AuditFilter{Action: "admin.users.list"}, 50), "admin.users.list")
	if len(reads) != 1 || reads[0].Channel != account.ChannelWeb || idText(reads[0].OperatorUserID) != admin.String() || reads[0].Reason != "lookup=none" {
		t.Fatalf("read record: %+v", reads)
	}
}

func TestAuditListFiltersValidateAndCombine(t *testing.T) {
	s, db := setup(t)
	clock := time.Now().UTC().Truncate(time.Microsecond)
	s.SetClock(func() time.Time { return clock })
	admin := platformAdmin(t, s, "root@example.com")
	other := platformAdmin(t, s, "root2@example.com")
	a := create(t, s, "a@example.com")
	b := create(t, s, "b@example.com")
	step := func(d time.Duration) { clock = clock.Add(d) }

	step(time.Minute)
	t1 := clock
	if _, err := s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, a, versionOf(t, db, a), "m-a")); err != nil {
		t.Fatal(err)
	}
	step(time.Minute)
	t2 := clock
	if _, err := s.ApplyAdminChange(ctx, req(other, account.ActionDisable, b, versionOf(t, db, b), "m-b")); err != nil {
		t.Fatal(err)
	}
	step(time.Minute)
	t3 := clock
	if _, err := s.ApplyAdminChange(ctx, req(other, account.ActionEnable, b, versionOf(t, db, b)-1, "m-c")); !errors.Is(err, account.ErrTargetChanged) {
		t.Fatal(err)
	}

	// Another administrator is refused when it tries to change this one: a row about admin, by someone else.
	if _, err := s.ApplyAdminChange(ctx, req(other, account.ActionEnable, admin, versionOf(t, db, admin), "m-p")); !errors.Is(err, account.ErrTargetProtected) {
		t.Fatal(err)
	}

	count := func(f account.AuditFilter) int { return len(listAll(t, s, admin, f, 2)) }
	// The operator filter is "what this account did" (web) and "what this account's holder did" (self): a row
	// that merely is about the account, made by another operator, is not the account's doing.
	mine := listAll(t, s, admin, account.AuditFilter{OperatorID: admin.String()}, 2)
	if len(mine) == 0 {
		t.Fatal("no row of the operator")
	}
	for _, r := range mine {
		web := r.Channel == account.ChannelWeb && r.OperatorUserID != nil && *r.OperatorUserID == admin
		self := r.Channel == account.ChannelSelf && r.UserID != nil && *r.UserID == admin
		if !web && !self {
			t.Fatalf("a row that is not the operator's doing: %+v", r)
		}
	}
	if n := count(account.AuditFilter{UserID: admin.String(), Result: "refused_target_protected"}); n != 1 {
		t.Fatalf("the refused attempt on the administrator: %d", n)
	}
	if n := count(account.AuditFilter{OperatorID: admin.String(), Result: "refused_target_protected"}); n != 0 {
		t.Fatalf("the refused attempt of another operator shows as this operator's: %d", n)
	}
	if n := count(account.AuditFilter{UserID: a.String(), Action: "admin.account.disable"}); n != 1 {
		t.Fatalf("user+action: %d", n)
	}
	if n := count(account.AuditFilter{OperatorID: other.String(), Channel: account.ChannelWeb, Action: "admin.account.disable"}); n != 1 {
		t.Fatalf("operator+channel+action: %d", n)
	}
	if n := count(account.AuditFilter{OperatorID: other.String(), Action: "admin.account.enable", Result: "refused_version_conflict"}); n != 1 {
		t.Fatalf("operator+result: %d", n)
	}
	if n := count(account.AuditFilter{MutationID: "m-b"}); n != 1 {
		t.Fatalf("mutation: %d", n)
	}
	if n := count(account.AuditFilter{MutationID: "no-such-key"}); n != 0 {
		t.Fatalf("unknown mutation: %d", n)
	}
	// From is inclusive and To exclusive, to the microsecond.
	if n := count(account.AuditFilter{Action: "admin.account.disable", From: t1, To: t2}); n != 1 {
		t.Fatalf("[t1,t2): %d", n)
	}
	if n := count(account.AuditFilter{Action: "admin.account.disable", From: t2, To: t3}); n != 1 {
		t.Fatalf("[t2,t3): %d", n)
	}
	if n := count(account.AuditFilter{Action: "admin.account.disable", From: t1, To: t3}); n != 2 {
		t.Fatalf("[t1,t3): %d", n)
	}
	if n := count(account.AuditFilter{Action: "admin.account.disable", From: t3}); n != 0 {
		t.Fatalf("[t3,): %d", n)
	}
	if n := count(account.AuditFilter{Action: "admin.account.disable", To: t1}); n != 0 {
		t.Fatalf("(,t1): %d", n)
	}

	for name, f := range map[string]account.AuditFilter{
		"user":      {UserID: "not-a-uuid"},
		"nil user":  {UserID: uuid.Nil.String()},
		"operator":  {OperatorID: "x"},
		"channel":   {Channel: "browser"},
		"action":    {Action: "Admin Account"},
		"long":      {Result: strings.Repeat("a", 65)},
		"mutation":  {MutationID: "bad key"},
		"order":     {From: t3, To: t1},
		"equal":     {From: t1, To: t1},
		"injection": {Action: "ok' OR '1'='1"},
	} {
		if _, err := s.ListAudit(ctx, admin, "r", f, 0, nil); !isValidation(err) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := s.ListAudit(ctx, admin, "r", account.AuditFilter{}, -1, nil); !isValidation(err) {
		t.Fatalf("negative page size: %v", err)
	}
	// A page size above the maximum is clamped, zero means the default.
	page, err := s.ListAudit(ctx, admin, "r", account.AuditFilter{}, 100000, nil)
	if err != nil || len(page.Records) > account.MaxAuditPageSize {
		t.Fatalf("clamp: %d %v", len(page.Records), err)
	}
}

func isValidation(err error) bool {
	_, ok := identity.IsValidation(err)
	return ok
}

// Paging walks the whole log newest first without repeating or skipping a row, also when
// many rows share one timestamp, and rows written meanwhile (they are newer) do not disturb the
// pages still to come.
func TestAuditListPagesStablyEvenWhenRowsShareATimestamp(t *testing.T) {
	s, db := setup(t)
	frozen := time.Now().UTC().Truncate(time.Microsecond)
	s.SetClock(func() time.Time { return frozen })
	admin := platformAdmin(t, s, "root@example.com")
	target := create(t, s, "plain@example.com")
	// Twelve rows with the very same created_at: only the ID can order them.
	for i := 0; i < 12; i++ {
		if _, err := s.ApplyAdminChange(ctx, req(admin, account.ActionEnable, target, versionOf(t, db, target), fmt.Sprintf("same-%02d", i))); err != nil {
			t.Fatal(err)
		}
	}
	want := map[uuid.UUID]bool{}
	for _, r := range db.SecurityAudit.Query().Where(securityaudit.Action("admin.account.enable")).AllX(ctx) {
		want[r.ID] = true
	}
	if len(want) != 12 {
		t.Fatalf("%d rows", len(want))
	}

	f := account.AuditFilter{Action: "admin.account.enable"}
	first, err := s.ListAudit(ctx, admin, "r", f, 5, nil)
	if err != nil || len(first.Records) != 5 || first.Next == nil {
		t.Fatalf("first page: %+v %v", first, err)
	}
	// Something is written while the reader is between pages: it is newer, so it is not on the later pages.
	frozen = frozen.Add(time.Second)
	if _, err = s.ApplyAdminChange(ctx, req(admin, account.ActionEnable, target, versionOf(t, db, target), "late")); err != nil {
		t.Fatal(err)
	}
	got := map[uuid.UUID]bool{}
	for _, r := range first.Records {
		got[r.ID] = true
	}
	after := first.Next
	for after != nil {
		p, err := s.ListAudit(ctx, admin, "r", f, 5, after)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range p.Records {
			if got[r.ID] {
				t.Fatalf("row %s repeated", r.ID)
			}
			got[r.ID] = true
		}
		after = p.Next
	}
	for id := range want {
		if !got[id] {
			t.Fatalf("row %s skipped", id)
		}
	}
	if len(got) != 12 {
		t.Fatalf("%d rows paged, want the 12 that existed when paging began", len(got))
	}
	// The order is the documented total order.
	all := listAll(t, s, admin, f, 4)
	for i := 1; i < len(all); i++ {
		a, b := all[i-1], all[i]
		if b.CreatedAt.After(a.CreatedAt) || (b.CreatedAt.Equal(a.CreatedAt) && b.ID.String() > a.ID.String()) {
			t.Fatalf("not newest first at %d: %s/%s then %s/%s", i, a.CreatedAt, a.ID, b.CreatedAt, b.ID)
		}
	}
}

// Only a platform administrator who is one now reads the audit: ordinary, metro-only, unknown,
// revoked and disabled callers get nothing, and each refusal is itself audited.
func TestAuditListNeedsThePlatformRoleNow(t *testing.T) {
	s, db := setup(t)
	admin := platformAdmin(t, s, "root@example.com")
	target := create(t, s, "plain@example.com")
	if _, err := s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, target, versionOf(t, db, target), "seed")); err != nil {
		t.Fatal(err)
	}
	metro := create(t, s, "metro@example.com")
	if _, err := s.GrantRole(ctx, op, metro, account.RoleMetroAdmin); err != nil {
		t.Fatal(err)
	}
	revoked := platformAdmin(t, s, "revoked@example.com")
	if _, err := s.RevokeRole(ctx, op, revoked, account.RolePlatformAdmin); err != nil {
		t.Fatal(err)
	}
	disabled := platformAdmin(t, s, "disabled@example.com")
	if _, err := s.SetDisabled(ctx, op, disabled, true); err != nil {
		t.Fatal(err)
	}
	callers := map[string]uuid.UUID{"ordinary": target, "metro-admin only": metro, "unknown": uuid.New(), "revoked": revoked, "disabled": disabled}
	for name, who := range callers {
		page, err := s.ListAudit(ctx, who, "r-"+name, account.AuditFilter{}, 0, nil)
		if !errors.Is(err, account.ErrForbidden) || len(page.Records) != 0 {
			t.Fatalf("%s: %+v %v", name, page, err)
		}
	}
	if n := db.SecurityAudit.Query().Where(securityaudit.Action("admin.audit.list"), securityaudit.Result("refused_forbidden")).CountX(ctx); n != len(callers) {
		t.Fatalf("%d refused reads audited, want %d", n, len(callers))
	}
	if _, err := s.ListAudit(ctx, admin, "ok", account.AuditFilter{}, 0, nil); err != nil {
		t.Fatal(err)
	}
}

// The read is audited with the kinds of filter used and nothing else: no account ID, key, time
// or other value a reader typed ever lands in the audit rows.
func TestAuditListReadIsAuditedWithoutFilterValues(t *testing.T) {
	s, db := setup(t)
	admin := platformAdmin(t, s, "root@example.com")
	target := create(t, s, "plain@example.com")
	if _, err := s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, target, versionOf(t, db, target), "secret-looking-key")); err != nil {
		t.Fatal(err)
	}
	f := account.AuditFilter{UserID: target.String(), OperatorID: admin.String(), Action: "admin.account.disable", Result: "ok", MutationID: "secret-looking-key",
		Channel: account.ChannelWeb, From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)}
	page, err := s.ListAudit(ctx, admin, "req-read-1", f, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListAudit(ctx, admin, "req-read-2", f, 1, &account.AuditCursor{CreatedAt: time.Now().Add(time.Hour), ID: uuid.New()}); err != nil {
		t.Fatal(err)
	}
	_ = page
	reads := db.SecurityAudit.Query().Where(securityaudit.Action("admin.audit.list")).Order(ent.Asc(securityaudit.FieldCreatedAt)).AllX(ctx)
	if len(reads) != 2 {
		t.Fatalf("%d read records", len(reads))
	}
	for i, want := range []string{"filters=user,operator,channel,action,result,mutation,time", "filters=user,operator,channel,action,result,mutation,time paged"} {
		r := reads[i]
		if r.Reason != want || r.Result != "ok" || r.Channel != "web" || r.ActorUserID == nil || *r.ActorUserID != admin || r.UserID != nil || r.MutationID != "" {
			t.Fatalf("read record %d: %+v", i, r)
		}
		text := strings.Join([]string{r.Actor, r.Action, r.RequestID, r.Result, r.Reason, r.MutationID}, "|")
		for _, value := range []string{target.String(), "secret-looking-key", "@example.com"} {
			if strings.Contains(text, value) {
				t.Fatalf("the read record holds %q: %s", value, text)
			}
		}
	}
}

// Nothing a reader sees can be a secret: after flows that handle emails and passwords, no record
// carries an address, a password, a hash or a token.
func TestAuditListHoldsNoSensitiveContent(t *testing.T) {
	s, db := setup(t)
	admin := platformAdmin(t, s, "root@example.com")
	target := create(t, s, "plain@example.com")
	if _, _, err := s.Login(ctx, "plain@example.com", "wrong password here", "req-bad"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if _, _, err := s.Login(ctx, "plain@example.com", password, "req-good"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyAdminChange(ctx, req(admin, account.ActionGrantMetroAdmin, target, versionOf(t, db, target), "g-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListUsers(ctx, admin, "req-list", account.UserFilter{Email: "plain@example.com"}, 0, nil); err != nil {
		t.Fatal(err)
	}
	rows := listAll(t, s, admin, account.AuditFilter{}, 10)
	if len(rows) < 6 {
		t.Fatalf("only %d rows", len(rows))
	}
	for _, r := range rows {
		text := fmt.Sprintf("%+v", r)
		for _, secret := range []string{"@example.com", password, "wrong password", "argon2", "Bearer", "mcp_"} {
			if strings.Contains(text, secret) {
				t.Fatalf("record holds %q: %s", secret, text)
			}
		}
	}
	// The lookup of the list read is recorded by kind only.
	if reads := byAction(rows, "admin.users.list"); len(reads) != 1 || reads[0].Reason != "lookup=email" {
		t.Fatalf("list read: %+v", reads)
	}
}

// A mutation ID is reusable once its receipt has expired and a refused attempt may use any key, so
// (operator, mutation ID) is not one event. A replay points at its original by that row's ID: it
// never links across use cycles, and a receipt that predates the link has no origin to show.
func TestAuditReplayLinksTheExactOriginAcrossReusedKeys(t *testing.T) {
	db, raw := testutil.DBWithSQL(t)
	s := account.NewService(db, testutil.Hasher(t))
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := now
	s.SetClock(func() time.Time { return clock })
	admin := platformAdmin(t, s, "root@example.com")
	target := create(t, s, "plain@example.com")

	// Cycle 1: disable under "k", then the lost response is retried.
	v := versionOf(t, db, target)
	first, err := s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, target, v, "k"))
	if err != nil || !first.Changed {
		t.Fatalf("cycle 1: %+v %v", first, err)
	}
	if _, err = s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, target, v, "k")); err != nil {
		t.Fatal(err)
	}
	// A refused attempt under the same key, in the middle of the cycle (different content).
	if _, err = s.ApplyAdminChange(ctx, req(admin, account.ActionEnable, target, v, "k")); !errors.Is(err, account.ErrMutationReused) {
		t.Fatal(err)
	}

	// The receipt expires; the key is used again for a different change - and a third time, refused.
	clock = now.Add(account.AdminMutationTTL + time.Second)
	v2 := versionOf(t, db, target)
	second, err := s.ApplyAdminChange(ctx, req(admin, account.ActionEnable, target, v2, "k"))
	if err != nil || !second.Changed || second.Replayed {
		t.Fatalf("cycle 2: %+v %v", second, err)
	}
	if r, err := s.ApplyAdminChange(ctx, req(admin, account.ActionEnable, target, v2, "k")); err != nil || !r.Replayed || r.Status != account.StatusActive {
		t.Fatalf("cycle 2 replay: %+v %v", r, err)
	}
	if _, err = s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, target, v2-1, "k")); !errors.Is(err, account.ErrMutationReused) {
		t.Fatal(err)
	}

	rows := listAll(t, s, admin, account.AuditFilter{MutationID: "k"}, 50)
	var originals, replays []account.AuditRecord
	results := map[string]int{}
	for _, r := range rows {
		results[r.Result]++
		if r.Result == "replayed" {
			replays = append(replays, r)
		} else if r.Result == "ok" || r.Result == "unchanged" {
			originals = append(originals, r)
		}
	}
	// One key, two real changes and refused attempts: the key does not name one event.
	if len(originals) != 2 || len(replays) != 2 || results["refused_mutation_reuse"] != 2 || len(rows) != 6 {
		t.Fatalf("rows under the key: %v (%d)", results, len(rows))
	}
	byID := map[uuid.UUID]account.AuditRecord{}
	for _, o := range originals {
		byID[o.ID] = o
	}
	for _, rep := range replays {
		if rep.OriginID == nil || rep.Origin == nil || rep.BeforeStatus != "" || rep.AfterStatus != "" || len(rep.BeforeRoles)+len(rep.AfterRoles) != 0 {
			t.Fatalf("replay row: %+v", rep)
		}
		orig, ok := byID[*rep.OriginID]
		if !ok || rep.Origin.ID != orig.ID || rep.Origin.Result != orig.Result || rep.Origin.BeforeStatus != orig.BeforeStatus ||
			rep.Origin.AfterStatus != orig.AfterStatus || rep.Origin.Action != orig.Action || !rep.Origin.CreatedAt.Equal(orig.CreatedAt) {
			t.Fatalf("origin of %s: %+v vs %+v", rep.ID, rep.Origin, orig)
		}
		// The origin is of the same action the replay repeated, and older than the replay.
		if rep.Action != orig.Action || orig.CreatedAt.After(rep.CreatedAt) {
			t.Fatalf("replay %+v is not after its origin %+v", rep, orig)
		}
	}
	// The replay of the second cycle points at the second change, never the first one.
	for _, rep := range replays {
		if rep.Action == "admin.account.enable" && rep.Origin.AfterStatus != "active" {
			t.Fatalf("the second cycle's replay linked to the first cycle: %+v", rep.Origin)
		}
		if rep.Action == "admin.account.disable" && rep.Origin.AfterStatus != "disabled" {
			t.Fatalf("the first cycle's replay linked to the second cycle: %+v", rep.Origin)
		}
	}
	if replays[0].OriginID == nil || *replays[0].OriginID == *replays[1].OriginID {
		t.Fatalf("both replays name the same origin")
	}

	// A receipt written before the link existed has no audit row to name: its replay has no origin, and
	// says so instead of guessing one from (operator, key).
	clock = clock.Add(time.Minute)
	v3 := versionOf(t, db, target)
	if _, err = s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, target, v3, "old-receipt")); err != nil {
		t.Fatal(err)
	}
	if _, err = raw.ExecContext(ctx, `UPDATE admin_mutations SET audit_id = NULL WHERE mutation_id = 'old-receipt'`); err != nil {
		t.Fatal(err)
	}
	if r, err := s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, target, v3, "old-receipt")); err != nil || !r.Replayed {
		t.Fatalf("replay of a receipt without a link: %+v %v", r, err)
	}
	legacy := listAll(t, s, admin, account.AuditFilter{MutationID: "old-receipt", Result: "replayed"}, 10)
	if len(legacy) != 1 || legacy[0].OriginID != nil || legacy[0].Origin != nil {
		t.Fatalf("replay of an unlinked receipt: %+v", legacy)
	}

	// An origin the retention purge removed leaves the link with no summary.
	db.SecurityAudit.Delete().Where(securityaudit.ID(*replays[0].OriginID)).ExecX(ctx)
	after := listAll(t, s, admin, account.AuditFilter{MutationID: "k", Result: "replayed"}, 10)
	var missing int
	for _, r := range after {
		if r.OriginID != nil && r.Origin == nil {
			missing++
		}
	}
	if missing != 1 {
		t.Fatalf("%d replays with a purged origin, want 1", missing)
	}
}

// The classification of rows that predate the channel (and the actor UUID of a console read) is
// part of the migration; this checks the rules on the old row shapes through the real SQL.
func TestAuditRowsOfEveryChannelAreClassifiedByTheMigration(t *testing.T) {
	db, raw := testutil.DBWithSQL(t)
	s := account.NewService(db, testutil.Hasher(t))
	admin := platformAdmin(t, s, "root@example.com")
	holder := create(t, s, "holder@example.com")
	// Rows as T4 and earlier wrote them: no channel, no actor UUID column.
	insert := func(actor, action string, user *uuid.UUID) uuid.UUID {
		id := uuid.New()
		if _, err := raw.ExecContext(ctx, `INSERT INTO security_audit (id, user_id, actor, action, request_id, result, reason, created_at)
			VALUES ($1, $2, $3, $4, '', 'ok', '', now())`, id, user, actor, action); err != nil {
			t.Fatal(err)
		}
		return id
	}
	system := insert("system", "reset.request", nil)
	readOld := insert("user:"+admin.String(), "admin.users.list", nil)
	self := insert("user:"+holder.String(), "login", &holder)
	cli := insert("ops-legacy", "role.grant:metro-admin", &holder)
	for _, st := range migrationBackfill(t) {
		if _, err := raw.ExecContext(ctx, st); err != nil {
			t.Fatalf("%s: %v", st, err)
		}
	}
	rows := map[uuid.UUID]*ent.SecurityAudit{}
	for _, r := range db.SecurityAudit.Query().AllX(ctx) {
		rows[r.ID] = r
	}
	if r := rows[system]; r.Channel != "system" {
		t.Fatalf("system: %+v", r)
	}
	if r := rows[readOld]; r.Channel != "web" || r.ActorUserID == nil || *r.ActorUserID != admin {
		t.Fatalf("old console read: %+v", r)
	}
	if r := rows[self]; r.Channel != "self" || r.ActorUserID != nil {
		t.Fatalf("self-service: %+v", r)
	}
	if r := rows[cli]; r.Channel != "cli" || r.Actor != "ops-legacy" {
		t.Fatalf("cli: %+v", r)
	}
	// Whatever the code wrote since then is already classified, so the backfill touches nothing else.
	for _, r := range rows {
		if r.Channel == "" {
			t.Fatalf("unclassified row: %+v", r)
		}
	}
}

// migrationBackfill reads the classification statements out of the real migration file, so the test
// runs exactly what an upgrade runs.
func migrationBackfill(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../../../migrations/usersrv/20261025140000_audit_query.sql")
	if err != nil {
		t.Fatal(err)
	}
	var code []string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			code = append(code, line)
		}
	}
	text := strings.Join(code, "\n")
	text = text[strings.Index(text, "UPDATE"):]
	var out []string
	for _, st := range strings.Split(text, ";") {
		if st = strings.TrimSpace(st); st != "" {
			out = append(out, st)
		}
	}
	if len(out) != 4 {
		t.Fatalf("%d backfill statements", len(out))
	}
	return out
}
