package auth

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/ent"
	"github.com/kongken/mono/internal/bff/ent/accesstoken"
	"github.com/kongken/mono/internal/bff/ent/accountdeletion"
	"github.com/kongken/mono/internal/bff/ent/schema"
	entsession "github.com/kongken/mono/internal/bff/ent/session"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/bff/usersrv"
	"github.com/kongken/mono/internal/platform/rpcauth"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// DeletionReceiptTTL is how long the Cookie that started a deletion can replay
	// its result after it finished.
	DeletionReceiptTTL = 24 * time.Hour
	// deletionLease is how long one runner owns a workflow; a crashed runner's lease
	// simply runs out.
	deletionLease = 2 * time.Minute
	// DeletionRunTimeout bounds one background pass over a workflow.
	DeletionRunTimeout = 2 * time.Minute

	maxBackoff  = 15 * time.Minute
	baseBackoff = 30 * time.Second
)

var mutationIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// errBusy is returned while another runner owns the workflow; the caller retries.
var errBusy = connect.NewError(connect.CodeUnavailable, errors.New("account deletion is running; retry shortly"))

func errDeletionStep(msg string) error {
	return connect.NewError(connect.CodeUnavailable, errors.New(msg))
}

// DeletionByCookie finds the deletion workflow the Cookie token started. Only the
// digest of the Cookie opens a workflow: a workflow or user ID alone never does.
// A finished workflow answers only until its receipt expires.
func (s *Service) DeletionByCookie(ctx context.Context, token string) (*ent.AccountDeletion, error) {
	if token == "" {
		return nil, errUnauth
	}
	wf, err := s.db.AccountDeletion.Query().Where(accountdeletion.TokenHash(session.HashToken(token))).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, errUnauth
		}
		return nil, internal(err)
	}
	if wf.Stage == schema.DeletionStageDone && (wf.ReceiptExpiresAt == nil || !wf.ReceiptExpiresAt.After(s.now())) {
		return nil, errUnauth
	}
	return wf, nil
}

// HasDeletion reports whether the Cookie token started a deletion that can still be
// read or replayed. The ingress uses it to let DeleteAccount through once the
// session itself is gone.
func (s *Service) HasDeletion(ctx context.Context, token string) bool {
	_, err := s.DeletionByCookie(ctx, token)
	return err == nil
}

// DeletionPending reports whether the Cookie token started a deletion that has not
// finished. That Cookie is the only credential to see or continue it, so nothing but
// the finished workflow may clear it (Logout leaves it alone).
func (s *Service) DeletionPending(ctx context.Context, token string) bool {
	wf, err := s.DeletionByCookie(ctx, token)
	return err == nil && wf.Stage != schema.DeletionStageDone
}

// StartDeletion persists the workflow of deleting the caller's account and runs it
// as far as it gets. The workflow row is written before usersrv or any product is
// touched, so a response lost anywhere afterwards can be resumed with the same
// mutation_id.
func (s *Service) StartDeletion(ctx context.Context, bs session.BrowserSession, token string, req *bffv1.DeleteAccountRequest) (*ent.AccountDeletion, error) {
	if req.GetConfirmation() != "DELETE" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("explicit DELETE confirmation is required"))
	}
	if !mutationIDPattern.MatchString(req.GetMutationId()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("mutation_id must be 1-64 characters of A-Z a-z 0-9 . _ -"))
	}
	if err := s.allow(ctx, "delete:"+bs.UserID, deletionLimit, deletionWindow); err != nil {
		return nil, err
	}
	if err := s.RequireRecentPassword(bs); err != nil {
		return nil, err
	}
	wf, err := s.createWorkflow(ctx, bs.UserID, req.GetMutationId(), token)
	if err != nil {
		return nil, err
	}
	// Persist first, look at the session second (a sign-out revokes first and looks at the
	// deletion second): a session that was ended after this request was admitted must not leave
	// a workflow whose Cookie the sign-out is clearing. Nothing has run yet, so dropping the row
	// is a clean refusal.
	if wf.Stage == schema.DeletionStageBegin && wf.TokenHash == session.HashToken(token) {
		live, lerr := s.db.Session.Query().
			Where(entsession.TokenHash(wf.TokenHash), entsession.Revoked(false), entsession.ExpiresAtGT(s.now())).Exist(ctx)
		if lerr != nil {
			return nil, internal(lerr)
		}
		if !live {
			if _, derr := s.db.AccountDeletion.Delete().Where(accountdeletion.ID(wf.ID), accountdeletion.Stage(schema.DeletionStageBegin)).Exec(ctx); derr != nil {
				return nil, internal(derr)
			}
			return nil, errUnauth
		}
	}
	return s.advance(ctx, wf)
}

// createWorkflow writes the workflow, or returns the existing one for the same
// mutation_id. A workflow that has not even begun at usersrv may be replaced by a
// new request: nothing has been committed yet.
func (s *Service) createWorkflow(ctx context.Context, userID, mutationID, token string) (*ent.AccountDeletion, error) {
	products := make([]string, 0, len(s.cleaners))
	for _, c := range s.cleaners {
		products = append(products, c.Name())
	}
	for attempt := 0; attempt < 2; attempt++ {
		existing, err := s.db.AccountDeletion.Query().Where(accountdeletion.UserID(userID)).Only(ctx)
		switch {
		case err == nil:
			if existing.MutationID == mutationID {
				return existing, nil
			}
			if existing.Stage != schema.DeletionStageBegin {
				return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("another account deletion is already in progress"))
			}
			if _, err = s.db.AccountDeletion.Delete().Where(accountdeletion.ID(existing.ID), accountdeletion.Stage(schema.DeletionStageBegin)).Exec(ctx); err != nil {
				return nil, internal(err)
			}
		case !ent.IsNotFound(err):
			return nil, internal(err)
		}
		wf, err := s.db.AccountDeletion.Create().SetUserID(userID).SetMutationID(mutationID).
			SetTokenHash(session.HashToken(token)).SetProducts(products).SetCompletedProducts([]string{}).
			SetNextAttemptAt(s.now()).Save(ctx)
		if err == nil {
			return wf, nil
		}
		if !ent.IsConstraintError(err) {
			return nil, internal(err)
		}
		// A concurrent request created it first; read it back on the next pass.
	}
	return nil, errBusy
}

// ContinueDeletion runs the workflow of the Cookie forward and returns its state.
func (s *Service) ContinueDeletion(ctx context.Context, token string) (*ent.AccountDeletion, error) {
	wf, err := s.DeletionByCookie(ctx, token)
	if err != nil {
		return nil, err
	}
	if wf.Stage == schema.DeletionStageDone {
		return wf, nil
	}
	if err = s.allow(ctx, "delete:"+wf.UserID, deletionLimit, deletionWindow); err != nil {
		return nil, err
	}
	return s.advance(ctx, wf)
}

// advance takes the workflow's lease and runs every remaining stage. Each stage
// is idempotent and recorded as it completes, so a crash or a failure leaves a row
// that the next call (or the background runner) continues from. On a failure it
// returns UNAVAILABLE together with the row, so a caller can show the progress.
func (s *Service) advance(ctx context.Context, wf *ent.AccountDeletion) (*ent.AccountDeletion, error) {
	now := s.now()
	n, err := s.db.AccountDeletion.Update().
		Where(accountdeletion.ID(wf.ID), accountdeletion.Or(accountdeletion.LeaseUntilIsNil(), accountdeletion.LeaseUntilLT(now))).
		SetLeaseUntil(now.Add(deletionLease)).Save(ctx)
	if err != nil {
		return nil, internal(err)
	}
	if n == 0 {
		return wf, errBusy
	}
	defer func() {
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		// A workflow dropped by a permanent refusal has no lease left to release.
		if err := s.db.AccountDeletion.UpdateOneID(wf.ID).ClearLeaseUntil().Exec(release); err != nil && !ent.IsNotFound(err) {
			slog.Warn("release deletion lease", "workflow", wf.ID, "error", err)
		}
	}()
	// Persist progress even when the request that drives it is cancelled.
	persist := func(f func(*ent.AccountDeletionUpdateOne)) (*ent.AccountDeletion, error) {
		pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		up := s.db.AccountDeletion.UpdateOneID(wf.ID)
		f(up)
		return up.Save(pctx)
	}
	cur, err := s.db.AccountDeletion.Get(ctx, wf.ID)
	if err != nil {
		return nil, internal(err)
	}
	fail := func(class string, cause error) (*ent.AccountDeletion, error) {
		attempts := cur.Attempts + 1
		backoff := min(baseBackoff<<min(attempts-1, 10), maxBackoff)
		slog.Warn("account deletion step failed", "workflow", cur.ID, "user", cur.UserID, "stage", cur.Stage, "class", class, "error", cause)
		next, perr := persist(func(u *ent.AccountDeletionUpdateOne) {
			u.SetAttempts(attempts).SetLastError(class).SetNextAttemptAt(s.now().Add(backoff))
		})
		if perr != nil {
			slog.Error("record deletion failure", "workflow", cur.ID, "error", perr)
			next = cur
		}
		return next, deletionError(cause)
	}
	callCtx := withUser(ctx, cur.UserID)
	for {
		switch cur.Stage {
		case schema.DeletionStageBegin:
			if err = s.users.BeginDeletion(callCtx, cur.UserID, cur.MutationID); err != nil {
				if usersrv.IsPlatformAdminProtected(err) {
					// A refusal for good, not a failure to retry: keeping the workflow would
					// let the account be closed by itself the day the role is revoked.
					persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					defer cancel()
					if _, derr := s.db.AccountDeletion.Delete().Where(accountdeletion.ID(cur.ID), accountdeletion.Stage(schema.DeletionStageBegin)).Exec(persistCtx); derr != nil {
						slog.Error("drop refused deletion workflow", "workflow", cur.ID, "error", derr)
					}
					slog.Warn("account deletion refused: platform administrator", "user", cur.UserID)
					return nil, err
				}
				return fail("begin:"+errClass(err), err)
			}
			if cur, err = persist(func(u *ent.AccountDeletionUpdateOne) { u.SetStage(schema.DeletionStageCleanup) }); err != nil {
				return nil, internal(err)
			}
		case schema.DeletionStageCleanup:
			var failed error
			var failedClass string
			for _, product := range cur.Products {
				if slices.Contains(cur.CompletedProducts, product) {
					continue
				}
				cleaner := s.cleanerFor(product)
				if cleaner == nil {
					// A product listed when the deletion started must be cleaned; it is
					// never skipped because the configuration changed or it is not deployed.
					if failed == nil {
						failed, failedClass = errors.New("cleaner for "+product+" is not configured"), "cleanup:"+product+":not_configured"
					}
					continue
				}
				if cerr := cleaner.CleanupUserData(callCtx, cur.UserID); cerr != nil {
					if failed == nil {
						failed, failedClass = cerr, "cleanup:"+product+":"+errClass(cerr)
					}
					continue
				}
				done := append(slices.Clone(cur.CompletedProducts), product)
				if cur, err = persist(func(u *ent.AccountDeletionUpdateOne) { u.SetCompletedProducts(done) }); err != nil {
					return nil, internal(err)
				}
			}
			if failed != nil {
				return fail(failedClass, failed)
			}
			if cur, err = persist(func(u *ent.AccountDeletionUpdateOne) { u.SetStage(schema.DeletionStageFinish).SetLastError("") }); err != nil {
				return nil, internal(err)
			}
		case schema.DeletionStageFinish:
			if err = s.users.FinishDeletion(callCtx, cur.UserID, cur.MutationID); err != nil {
				return fail("finish:"+errClass(err), err)
			}
			if cur, err = persist(func(u *ent.AccountDeletionUpdateOne) { u.SetStage(schema.DeletionStageLocal) }); err != nil {
				return nil, internal(err)
			}
		case schema.DeletionStageLocal:
			if err = s.finishLocal(ctx, cur); err != nil {
				return fail("local:"+errClass(err), err)
			}
			if cur, err = s.db.AccountDeletion.Get(ctx, cur.ID); err != nil {
				return nil, internal(err)
			}
		default:
			return cur, nil
		}
	}
}

// finishLocal removes the BFF's own rows of the account and writes the receipt, in
// one transaction: sessions, management tokens, then the stage.
func (s *Service) finishLocal(ctx context.Context, wf *ent.AccountDeletion) error {
	tx, err := s.db.Tx(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Session.Delete().Where(entsession.UserID(wf.UserID)).Exec(ctx); err != nil {
		return err
	}
	if _, err = tx.AccessToken.Delete().Where(accesstoken.UserID(wf.UserID)).Exec(ctx); err != nil {
		return err
	}
	now := s.now()
	if err = tx.AccountDeletion.UpdateOneID(wf.ID).SetStage(schema.DeletionStageDone).SetLastError("").
		SetCompletedAt(now).SetReceiptExpiresAt(now.Add(DeletionReceiptTTL)).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) cleanerFor(name string) UserDataCleaner {
	for _, c := range s.cleaners {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

// PurgeDeletionReceipts removes finished workflows whose receipt has expired.
func (s *Service) PurgeDeletionReceipts(ctx context.Context) (int, error) {
	return s.db.AccountDeletion.Delete().
		Where(accountdeletion.Stage(schema.DeletionStageDone), accountdeletion.ReceiptExpiresAtLTE(s.now())).Exec(ctx)
}

// RunDeletionsOnce advances every workflow that has begun at usersrv and whose
// retry time has come, so a deletion keeps going when its Cookie has expired or the
// browser never came back. A workflow that has not begun (the caller got an error
// for the request itself) waits for its owner and is never started in the
// background. It returns how many workflows it advanced.
func (s *Service) RunDeletionsOnce(ctx context.Context) (int, error) {
	now := s.now()
	rows, err := s.db.AccountDeletion.Query().
		Where(accountdeletion.StageIn(schema.DeletionStageCleanup, schema.DeletionStageFinish, schema.DeletionStageLocal),
			accountdeletion.NextAttemptAtLTE(now),
			accountdeletion.Or(accountdeletion.LeaseUntilIsNil(), accountdeletion.LeaseUntilLT(now))).
		Order(ent.Asc(accountdeletion.FieldNextAttemptAt)).Limit(20).All(ctx)
	if err != nil {
		return 0, err
	}
	advanced := 0
	for _, wf := range rows {
		runCtx, cancel := context.WithTimeout(ctx, DeletionRunTimeout)
		_, err := s.advance(runCtx, wf)
		cancel()
		if err != nil && !errors.Is(err, errBusy) {
			slog.Warn("background account deletion", "workflow", wf.ID, "error", err)
		}
		advanced++
	}
	if _, err := s.PurgeDeletionReceipts(ctx); err != nil {
		slog.Warn("purge deletion receipts", "error", err)
	}
	return advanced, nil
}

// RetryDeletionByID continues one workflow by its ID. It is the operator's tool
// (cmd/bff-admin); no browser or RPC path takes a workflow ID.
func (s *Service) RetryDeletionByID(ctx context.Context, id string) (*ent.AccountDeletion, error) {
	wf, err := s.db.AccountDeletion.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("deletion not found"))
		}
		return nil, internal(err)
	}
	if wf.Stage == schema.DeletionStageDone {
		return wf, nil
	}
	return s.advance(ctx, wf)
}

// DeletionMessage converts a workflow to its browser form.
func DeletionMessage(wf *ent.AccountDeletion) *bffv1.AccountDeletion {
	state := bffv1.AccountDeletionState_ACCOUNT_DELETION_STATE_PENDING
	out := &bffv1.AccountDeletion{
		MutationId: wf.MutationID, Products: slices.Clone(wf.Products), CompletedProducts: slices.Clone(wf.CompletedProducts),
	}
	if wf.Stage == schema.DeletionStageDone {
		state = bffv1.AccountDeletionState_ACCOUNT_DELETION_STATE_COMPLETED
		if wf.ReceiptExpiresAt != nil {
			out.ReceiptExpiresAt = timestamppb.New(*wf.ReceiptExpiresAt)
		}
	}
	out.State = state
	return out
}

// withUser describes a call made on behalf of userID, keeping the request's
// address and ID.
func withUser(ctx context.Context, userID string) context.Context {
	c := rpcauth.CallFrom(ctx)
	c.UserID, c.Roles, c.TokenID = userID, nil, ""
	return rpcauth.WithCall(ctx, c)
}

// errClass reduces a failure to a short class for the workflow row, so no message
// of a service is stored.
func errClass(err error) string {
	if r := usersrv.Reason(err); r != "" {
		return r
	}
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Code().String()
	}
	if st, ok := status.FromError(err); ok {
		return st.Code().String()
	}
	return "error"
}

// deletionError is what the caller of a failed step sees: UNAVAILABLE for anything a
// retry can fix (a product or usersrv that cannot answer, a cleanup that did not
// finish), so the client retries; a request usersrv refused for good is passed on.
func deletionError(err error) error {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return err
	}
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.Unavailable:
			return errDeletionStep(st.Message())
		case codes.FailedPrecondition, codes.NotFound, codes.PermissionDenied:
			if usersrv.Reason(err) != "" {
				return usersrv.MapError(err)
			}
		}
	}
	return errDeletionStep("account deletion is incomplete; retry")
}
