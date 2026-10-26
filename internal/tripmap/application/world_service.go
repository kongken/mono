package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/ent/user"
	"github.com/kongken/mono/internal/tripmap/ent/worlddocument"
	"github.com/kongken/mono/internal/tripmap/ent/worldreceipt"
	"github.com/kongken/mono/internal/tripmap/world"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"github.com/kongken/mono/pkg/proto/tripmap/v1/tripmapv1connect"
)

const (
	worldReadLimit   = 120
	worldReadWindow  = time.Minute
	worldWriteLimit  = 60
	worldWriteWindow = time.Minute
	// worldReceiptKeep is how many successful requests a user's receipts remember.
	worldReceiptKeep = 32
	// worldWriteAttempts bounds the retries after a lost first-creation race. One retry
	// settles it (the winner's row is then visible); the extra attempts are a backstop.
	worldWriteAttempts = 3
)

// WorldService stores one world footprint document per user (docs/trip-map/world-spec.md
// section 9). It never touches footprints, snapshots, search, sharing, reports,
// collaboration, reminders or media.
type WorldService struct {
	tripmapv1connect.UnimplementedWorldServiceHandler
	src StoreSource
	cat world.Catalog
}

// NewWorldService builds the service with the embedded country catalog.
func NewWorldService(src StoreSource) (*WorldService, error) {
	cat, err := world.DefaultCatalog()
	if err != nil {
		return nil, err
	}
	return &WorldService{src: src, cat: cat}, nil
}

func (s *WorldService) allow(ctx context.Context, st *Store, key string, limit int64, window time.Duration) error {
	ok, err := NewLimiter(st.Redis, st.prefix()).Allow(ctx, key, limit, window)
	if err != nil {
		slog.Error("rate limiter unavailable", "error", err)
		return connect.NewError(connect.CodeUnavailable, errors.New("world service temporarily unavailable"))
	}
	if !ok {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("too many world requests, slow down"))
	}
	return nil
}

// worldState is a user's stored state: revision 0 and the empty document before the first write.
type worldState struct {
	revision int64
	doc      *tripmapv1.WorldDocument
}

func readWorld(row *ent.WorldDocument) (worldState, error) {
	doc, err := world.Decode(row.Payload)
	if err != nil {
		return worldState{}, err
	}
	return worldState{revision: row.Revision, doc: doc}, nil
}

func (s *WorldService) GetWorld(ctx context.Context, _ *connect.Request[tripmapv1.GetWorldRequest]) (*connect.Response[tripmapv1.GetWorldResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "world:read:"+p.UserID, worldReadLimit, worldReadWindow); err != nil {
		return nil, err
	}
	state := worldState{doc: world.Empty()}
	row, err := st.DB.WorldDocument.Query().Where(worlddocument.UserID(p.UserID)).Only(ctx)
	switch {
	case ent.IsNotFound(err):
		exists, uerr := st.DB.User.Query().Where(user.ID(p.UserID)).Exist(ctx)
		if uerr != nil {
			return nil, internalError(uerr)
		}
		if !exists {
			return nil, errUnauthenticated
		}
	case err != nil:
		return nil, internalError(err)
	default:
		if state, err = readWorld(row); err != nil {
			slog.Error("world document unreadable", "error", err)
			return nil, internalError(err)
		}
	}
	res := connect.NewResponse(&tripmapv1.GetWorldResponse{Revision: state.revision, Document: state.doc})
	res.Header().Set("Cache-Control", "no-store")
	return res, nil
}

func (s *WorldService) ReplaceWorld(ctx context.Context, req *connect.Request[tripmapv1.ReplaceWorldRequest]) (*connect.Response[tripmapv1.ReplaceWorldResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	msg := req.Msg
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "world:write:"+p.UserID, worldWriteLimit, worldWriteWindow); err != nil {
		return nil, err
	}

	// 1. Format and document validation. Nothing is read or written yet, and a
	// rejected request leaves no receipt.
	if msg.GetBaseRevision() < 0 {
		return nil, invalid("base_revision must not be negative")
	}
	if !world.ValidID(msg.GetRequestId()) {
		return nil, invalid("request_id must match [A-Za-z0-9_-]{1,64}")
	}
	canon, err := world.Validate(s.cat, msg.GetDocument())
	if err != nil {
		return nil, invalid("invalid world document: " + err.Error())
	}
	digest := world.Digest(msg.GetBaseRevision(), canon)
	payload, err := world.Encode(canon)
	if err != nil {
		return nil, internalError(err)
	}
	attempt := replaceAttempt{
		userID: p.UserID, base: msg.GetBaseRevision(), requestID: msg.GetRequestId(),
		doc: canon, digest: digest, payload: payload,
	}

	for range worldWriteAttempts {
		out, err := s.replaceOnce(ctx, st, attempt)
		if errors.Is(err, errWorldCreateRace) {
			continue
		}
		if err != nil {
			return nil, err
		}
		res := connect.NewResponse(out)
		res.Header().Set("Cache-Control", "no-store")
		return res, nil
	}
	return nil, connect.NewError(connect.CodeAborted, errors.New("the world document is being changed, retry"))
}

type replaceAttempt struct {
	userID    string
	base      int64
	requestID string
	doc       *tripmapv1.WorldDocument
	digest    string
	payload   []byte
}

// errWorldCreateRace means the per-user unique constraint refused a first write because
// another transaction created the row first: the caller reads again and, finding another
// revision, answers with a conflict. (The user's row lock already serialises writers; the
// constraint is the backstop that keeps a race from ever becoming an internal error.)
var errWorldCreateRace = errors.New("world document created concurrently")

// replaceOnce runs steps 2 and 3 of the decision order in one transaction under the user's
// row lock, which is also what serialises concurrent writers of the same base_revision.
func (s *WorldService) replaceOnce(ctx context.Context, st *Store, a replaceAttempt) (*tripmapv1.ReplaceWorldResponse, error) {
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	u, err := lockUser(ctx, tx, a.userID)
	if err != nil {
		return nil, err
	}

	row, err := tx.WorldDocument.Query().Where(worlddocument.UserID(u.ID)).Only(ctx)
	var cur worldState
	switch {
	case ent.IsNotFound(err):
		cur = worldState{doc: world.Empty()}
		row = nil
	case err != nil:
		return nil, internalError(err)
	default:
		if cur, err = readWorld(row); err != nil {
			slog.Error("world document unreadable", "error", err)
			return nil, internalError(err)
		}
	}

	// 2. Receipt replay, before base_revision is looked at.
	receipt, err := tx.WorldReceipt.Query().Where(worldreceipt.UserID(u.ID), worldreceipt.RequestID(a.requestID)).Only(ctx)
	switch {
	case err == nil:
		if receipt.Digest != a.digest {
			return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("request_id was already used for a different request"))
		}
		return &tripmapv1.ReplaceWorldResponse{
			Applied: true, Replayed: true, Revision: receipt.Revision, Document: a.doc, CloudRevision: cur.revision,
		}, nil
	case !ent.IsNotFound(err):
		return nil, internalError(err)
	}

	// 3. Compare and swap.
	if a.base != cur.revision {
		return &tripmapv1.ReplaceWorldResponse{
			Applied: false, Revision: cur.revision, Document: cur.doc, CloudRevision: cur.revision,
		}, nil
	}
	revision := cur.revision
	if !world.Equal(cur.doc, a.doc) {
		revision = cur.revision + 1
		places, trips := world.Counts(a.doc)
		if row == nil {
			_, err = tx.WorldDocument.Create().SetUserID(u.ID).SetRevision(revision).
				SetPayload(a.payload).SetPlaceCount(places).SetTripCount(trips).Save(ctx)
		} else {
			_, err = row.Update().SetRevision(revision).
				SetPayload(a.payload).SetPlaceCount(places).SetTripCount(trips).Save(ctx)
		}
		if ent.IsConstraintError(err) {
			return nil, errWorldCreateRace
		}
		if err != nil {
			return nil, wrapWorldWrite(err)
		}
	}
	if err = recordWorldReceipt(ctx, tx, u.ID, a.requestID, a.digest, revision); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		if ent.IsConstraintError(err) {
			return nil, errWorldCreateRace
		}
		return nil, internalError(err)
	}
	return &tripmapv1.ReplaceWorldResponse{
		Applied: true, Revision: revision, Document: a.doc, CloudRevision: revision,
	}, nil
}

// recordWorldReceipt stores the receipt of a successful request (also an unchanged one)
// and drops the receipts older than the newest worldReceiptKeep.
func recordWorldReceipt(ctx context.Context, tx *ent.Tx, userID, requestID, digest string, revision int64) error {
	var seq int64 = 1
	last, err := tx.WorldReceipt.Query().Where(worldreceipt.UserID(userID)).Order(ent.Desc(worldreceipt.FieldSeq)).First(ctx)
	switch {
	case err == nil:
		seq = last.Seq + 1
	case !ent.IsNotFound(err):
		return internalError(err)
	}
	if err = tx.WorldReceipt.Create().SetUserID(userID).SetRequestID(requestID).SetDigest(digest).
		SetRevision(revision).SetSeq(seq).Exec(ctx); err != nil {
		return wrapWorldWrite(err)
	}
	if _, err = tx.WorldReceipt.Delete().Where(worldreceipt.UserID(userID), worldreceipt.SeqLTE(seq-worldReceiptKeep)).Exec(ctx); err != nil {
		return internalError(err)
	}
	return nil
}

func wrapWorldWrite(err error) error {
	if ent.IsValidationError(err) {
		return invalid("record failed validation")
	}
	slog.Error("world write", "error", fmt.Sprint(err))
	return internalError(err)
}
