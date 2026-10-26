package account_test

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/user"
	"github.com/kongken/mono/internal/usersrv/ent/userrole"
	"github.com/kongken/mono/internal/usersrv/testutil"
)

// The account row lock is what keeps "begin deletion" and "change the platform role" apart:
// BeginDeletion decides under the row lock, and so do GrantRole / RevokeRole (the usersrv-admin
// CLI). These tests run both on a real PostgreSQL and make the order in which they take the lock
// explicit, in both directions, instead of hoping a race lands on each side.
//
// The order is forced with a gate: the test holds the row lock itself, starts the first operation,
// waits until PostgreSQL reports it blocked on that lock, starts the second, waits until it is
// blocked as well, then releases the gate. Waiters on one row lock are served in arrival order, so
// the first one runs to the end of its transaction before the second reads anything.

type race struct {
	t      *testing.T
	s      *account.Service
	db     *ent.Client
	sqldb  *sql.DB
	userID uuid.UUID
}

func newRace(t *testing.T, email string) *race {
	t.Helper()
	db, sqldb := testutil.DBWithSQL(t)
	s := account.NewService(db, testutil.Hasher(t))
	return &race{t: t, s: s, db: db, sqldb: sqldb, userID: create(t, s, email)}
}

// waiters counts the sessions that hold a lock on this test's users table and are waiting for
// another session (the gate, or each other). The table is the test's own throwaway schema, so
// nothing a neighbour runs on the same server is counted.
func (r *race) waiters() int {
	var n int
	err := r.sqldb.QueryRowContext(ctx, `
		SELECT count(*) FROM pg_locks l
		WHERE l.locktype = 'relation' AND l.relation = 'users'::regclass AND l.granted
		  AND cardinality(pg_blocking_pids(l.pid)) > 0`).Scan(&n)
	if err != nil {
		r.t.Fatalf("pg_locks: %v", err)
	}
	return n
}

func (r *race) waitForWaiters(n int) {
	r.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if r.waiters() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatalf("PostgreSQL never showed %d sessions blocked on the account row (saw %d)", n, r.waiters())
}

// inOrder runs first, then second, with both blocked on the row lock before either may start.
func (r *race) inOrder(first, second func() error) (firstErr, secondErr error) {
	r.t.Helper()
	gate, err := r.db.Tx(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err = gate.User.Query().Where(user.ID(r.userID)).ForUpdate().Only(ctx); err != nil {
		_ = gate.Rollback()
		r.t.Fatal(err)
	}
	var wg sync.WaitGroup
	run := func(f func() error, out *error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			*out = f()
		}()
	}
	run(first, &firstErr)
	r.waitForWaiters(1)
	run(second, &secondErr)
	r.waitForWaiters(2)
	if err = gate.Rollback(); err != nil {
		r.t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		r.t.Fatal("the operations never finished after the gate opened")
	}
	return firstErr, secondErr
}

// invariant is the property the lock exists for: an account that is being deleted never holds
// the platform role (a deleting account cannot be given it, and a holder cannot start deleting).
func (r *race) invariant() (status user.Status, holds bool) {
	r.t.Helper()
	u := r.db.User.GetX(ctx, r.userID)
	holds = r.db.UserRole.Query().Where(userrole.UserID(r.userID), userrole.Role(account.RolePlatformAdmin)).ExistX(ctx)
	if u.Status == user.StatusDeleting && holds {
		r.t.Fatalf("the account is deleting and holds %s at the same time", account.RolePlatformAdmin)
	}
	return u.Status, holds
}

func (r *race) grant() error {
	_, err := r.s.GrantRole(ctx, op, r.userID, account.RolePlatformAdmin)
	return err
}

func (r *race) revoke() error {
	_, err := r.s.RevokeRole(ctx, op, r.userID, account.RolePlatformAdmin)
	return err
}

func (r *race) begin() error {
	_, err := r.s.BeginDeletion(ctx, r.userID, "m-race", "req-race")
	return err
}

func TestDeletionAndRoleGrantTakeTheRowLockInEitherOrder(t *testing.T) {
	t.Run("the grant takes the lock first: the deletion is refused", func(t *testing.T) {
		r := newRace(t, "grant-first@example.com")
		grantErr, beginErr := r.inOrder(r.grant, r.begin)
		if grantErr != nil {
			t.Fatalf("grant: %v", grantErr)
		}
		if !errors.Is(beginErr, account.ErrPlatformAdmin) {
			t.Fatalf("begin deletion behind a grant: %v", beginErr)
		}
		if status, holds := r.invariant(); status != user.StatusActive || !holds {
			t.Fatalf("status %s, holds role %v: want an active platform administrator", status, holds)
		}
	})
	t.Run("the deletion takes the lock first: the grant is refused", func(t *testing.T) {
		r := newRace(t, "begin-first@example.com")
		beginErr, grantErr := r.inOrder(r.begin, r.grant)
		if beginErr != nil {
			t.Fatalf("begin deletion: %v", beginErr)
		}
		if !errors.Is(grantErr, account.ErrNotActive) {
			t.Fatalf("grant behind a deletion: %v", grantErr)
		}
		if status, holds := r.invariant(); status != user.StatusDeleting || holds {
			t.Fatalf("status %s, holds role %v: want a deleting account without the role", status, holds)
		}
	})
}

// The same gate with the role already held: a revocation that wins the lock lets the deletion
// through; a deletion that wins is refused and the revocation then leaves an ordinary account that
// is NOT closing (no workflow was started, so nothing finishes it later).
func TestDeletionAndRoleRevokeTakeTheRowLockInEitherOrder(t *testing.T) {
	held := func(t *testing.T, email string) *race {
		r := newRace(t, email)
		if err := r.grant(); err != nil {
			t.Fatal(err)
		}
		return r
	}
	t.Run("the revocation takes the lock first: the deletion starts", func(t *testing.T) {
		r := held(t, "revoke-first@example.com")
		revokeErr, beginErr := r.inOrder(r.revoke, r.begin)
		if revokeErr != nil || beginErr != nil {
			t.Fatalf("revoke: %v, begin deletion: %v", revokeErr, beginErr)
		}
		if status, holds := r.invariant(); status != user.StatusDeleting || holds {
			t.Fatalf("status %s, holds role %v", status, holds)
		}
	})
	t.Run("the deletion takes the lock first: it is refused and nothing is left closing", func(t *testing.T) {
		r := held(t, "begin-then-revoke@example.com")
		beginErr, revokeErr := r.inOrder(r.begin, r.revoke)
		if !errors.Is(beginErr, account.ErrPlatformAdmin) {
			t.Fatalf("begin deletion of a holder: %v", beginErr)
		}
		if revokeErr != nil {
			t.Fatalf("revoke: %v", revokeErr)
		}
		status, holds := r.invariant()
		if status != user.StatusActive || holds {
			t.Fatalf("status %s, holds role %v: want an ordinary active account", status, holds)
		}
		if u := r.db.User.GetX(ctx, r.userID); u.DeletionMutationID != nil || u.DeletionStartedAt != nil {
			t.Fatalf("a refused deletion left workflow state on the account: %+v", u)
		}
		// Only a new, explicit request closes it, and now it is allowed.
		if err := r.begin(); err != nil {
			t.Fatalf("a new request after the revocation: %v", err)
		}
	})
}

// Without the gate, many truly concurrent pairs: whichever side wins, the outcome is one of the two
// consistent ones and never "deleting with the role".
func TestDeletionAndRoleGrantRacingFreely(t *testing.T) {
	const rounds = 40
	db, _ := testutil.DBWithSQL(t)
	s := account.NewService(db, testutil.Hasher(t))
	var grantFirst, beginFirst int
	for i := 0; i < rounds; i++ {
		id := create(t, s, fmt.Sprintf("free-%d@example.com", i))
		start := make(chan struct{})
		var wg sync.WaitGroup
		var grantErr, beginErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, grantErr = s.GrantRole(ctx, op, id, account.RolePlatformAdmin)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, beginErr = s.BeginDeletion(ctx, id, fmt.Sprintf("m-%d", i), fmt.Sprintf("req-%d", i))
		}()
		close(start)
		wg.Wait()

		u := db.User.GetX(ctx, id)
		holds := db.UserRole.Query().Where(userrole.UserID(id), userrole.Role(account.RolePlatformAdmin)).ExistX(ctx)
		switch {
		case grantErr == nil && errors.Is(beginErr, account.ErrPlatformAdmin):
			grantFirst++
			if u.Status != user.StatusActive || !holds {
				t.Fatalf("round %d: grant won but status %s holds %v", i, u.Status, holds)
			}
		case beginErr == nil && errors.Is(grantErr, account.ErrNotActive):
			beginFirst++
			if u.Status != user.StatusDeleting || holds {
				t.Fatalf("round %d: deletion won but status %s holds %v", i, u.Status, holds)
			}
		default:
			t.Fatalf("round %d: grant=%v begin=%v is neither consistent outcome (status %s, holds %v)", i, grantErr, beginErr, u.Status, holds)
		}
	}
	t.Logf("%d rounds: grant first %d, deletion first %d", rounds, grantFirst, beginFirst)
}
