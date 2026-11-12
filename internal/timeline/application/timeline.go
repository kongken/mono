package application

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"google.golang.org/protobuf/encoding/protojson"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql/sqlgraph"
	"github.com/google/uuid"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/timeline/ent"
	"github.com/kongken/mono/internal/timeline/ent/category"
	"github.com/kongken/mono/internal/timeline/ent/entry"
	"github.com/kongken/mono/internal/timeline/ent/receipt"
	timelinev1 "github.com/kongken/mono/pkg/proto/timeline/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
var timeRE = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d(:[0-5]\d)?$`)
var publicRefRE = regexp.MustCompile(`^ent_[a-f0-9]{26}$`)
var mutationIDRE = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)

// TimelineService owns all record operations. The caller identity always comes
// from the internal auth context; request messages never contain a user id.
type TimelineService struct {
	timelinev1.UnimplementedTimelineServiceServer
	db *ent.Client
}

func NewTimelineService(db *ent.Client) *TimelineService { return &TimelineService{db: db} }

func user(ctx context.Context) (string, error) {
	id := rpcauth.CallFrom(ctx).UserID
	if id == "" {
		return "", status.Error(codes.Unauthenticated, "authentication required")
	}
	return id, nil
}
func validate(in *timelinev1.Entry, requireID bool) error {
	if in == nil || strings.TrimSpace(in.Title) == "" {
		return status.Error(codes.InvalidArgument, "title is required")
	}
	if !dateRE.MatchString(in.Date) {
		return status.Error(codes.InvalidArgument, "date must be YYYY-MM-DD")
	}
	if _, err := time.Parse("2006-01-02", in.Date); err != nil {
		return status.Error(codes.InvalidArgument, "invalid date")
	}
	if in.Time != "" && !timeRE.MatchString(in.Time) {
		return status.Error(codes.InvalidArgument, "invalid time")
	}
	if in.Timezone == "" {
		return status.Error(codes.InvalidArgument, "timezone is required")
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return status.Error(codes.InvalidArgument, "invalid timezone")
	}
	if _, err := localOccurrence(in); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if len([]rune(in.Title)) > 200 || len([]rune(in.Note)) > 10000 || len([]rune(in.Location)) > 300 {
		return status.Error(codes.InvalidArgument, "field is too long")
	}
	if in.AmountMinor < 0 || (in.Currency != "" && !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(in.Currency)) {
		return status.Error(codes.InvalidArgument, "invalid amount or currency")
	}
	if in.AmountPresent && in.Currency == "" {
		return status.Error(codes.InvalidArgument, "currency is required when amount is present")
	}
	// A currency without an amount is not a zero-value amount: the two are
	// distinct per the product rules, and an orphan currency is refused.
	if !in.AmountPresent && in.AmountMinor == 0 && in.Currency != "" {
		return status.Error(codes.InvalidArgument, "currency requires an amount")
	}
	if in.Url != "" {
		u, err := url.Parse(in.Url)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.IndexFunc(in.Url, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
			return status.Error(codes.InvalidArgument, "invalid URL")
		}
		host := strings.ToLower(u.Hostname())
		ip := net.ParseIP(host)
		if ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
			return status.Error(codes.InvalidArgument, "private URL is not allowed")
		}
	}
	if requireID && in.Id == "" && in.EntryRef == "" {
		return status.Error(codes.InvalidArgument, "entry_ref is required")
	}

	return nil
}
func validateLocator(ref, id string) error {
	if ref != "" && id != "" {
		return status.Error(codes.InvalidArgument, "provide either entry_ref or id")
	}
	if ref != "" && !publicRefRE.MatchString(ref) {
		return status.Error(codes.InvalidArgument, "invalid entry_ref")
	}

	if ref == "" && id == "" {
		return status.Error(codes.InvalidArgument, "entry_ref is required")
	}
	return nil
}
func occurred(in *timelinev1.Entry) time.Time {
	t, _ := localOccurrence(in)
	return t
}

// localOccurrence validates the wall-clock value before converting it to UTC.
// time.ParseInLocation silently normalizes DST gaps and chooses one side of a
// repeated hour, so compare the parsed components back to the request and
// reject either ambiguous representation.
func localOccurrence(in *timelinev1.Entry) (time.Time, error) {
	loc, err := time.LoadLocation(in.Timezone)
	if err != nil {
		return time.Time{}, errors.New("invalid timezone")
	}
	value := in.Date
	layout := "2006-01-02"
	if in.Time != "" {
		value += "T" + in.Time
		layout = "2006-01-02T15:04"
		if len(in.Time) == 8 {
			layout = "2006-01-02T15:04:05"
		}
	}
	parsed, err := time.ParseInLocation(layout, value, loc)
	if err != nil {
		return time.Time{}, errors.New("invalid local date or time")
	}
	if parsed.Format(layout) != value {
		return time.Time{}, errors.New("local time does not exist in this timezone")
	}
	// A repeated wall time has another instant with the same full local
	// timestamp. A date-only record has no ambiguous clock value to reject.
	if in.Time != "" {
		for _, delta := range []time.Duration{-2 * time.Hour, -90 * time.Minute, -time.Hour, time.Hour, 90 * time.Minute, 2 * time.Hour} {
			candidate := parsed.Add(delta)
			if candidate.In(loc).Format(layout) == value && !candidate.Equal(parsed) {
				return time.Time{}, errors.New("local time is ambiguous in this timezone")
			}
		}
	}
	now := time.Now().In(loc)
	if in.Time == "" {
		if in.Date > now.Format("2006-01-02") {
			return time.Time{}, errors.New("future dates are not allowed")
		}
	} else if parsed.After(now) {
		return time.Time{}, errors.New("future times are not allowed")
	}
	return parsed.UTC(), nil
}
func publicRef(prefix string) string {
	raw := strings.ReplaceAll(uuid.NewString(), "-", "")
	return prefix + raw[:26]
}

func validateMutationID(id string) error {
	if !mutationIDRE.MatchString(id) {
		return status.Error(codes.InvalidArgument, "mutation_id must be 8-128 ASCII characters")
	}
	return nil
}

func validateCategoryTx(ctx context.Context, tx *ent.Tx, uid, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	c, err := tx.Category.Query().Where(category.UserIDEQ(uid), category.NameEQ(name)).Only(ctx)
	if ent.IsNotFound(err) {
		return status.Error(codes.InvalidArgument, "category does not exist")
	}
	if err != nil {
		return err
	}
	if !c.Active {
		return status.Error(codes.FailedPrecondition, "category is inactive")
	}
	return nil
}
func requireRevision(revision int64) error {
	if revision <= 0 {
		return status.Error(codes.InvalidArgument, "expected_revision is required")
	}
	return nil
}
func mapEntry(e *ent.Entry) *timelinev1.Entry {
	out := &timelinev1.Entry{EntryRef: e.PublicRef, Title: e.Title, Date: e.OccurredDate, Time: e.OccurredTime, Timezone: e.Timezone, Category: e.Category, Location: e.Location, AmountMinor: e.AmountMinor, AmountPresent: e.AmountPresent || e.AmountMinor != 0 || e.Currency != "", Currency: e.Currency, Note: e.Body, Url: e.URL, UrlTitle: e.URLTitle, UrlNote: e.URLNote, Revision: int64(e.Revision), CreatedAt: timestamppb.New(e.CreatedAt), UpdatedAt: timestamppb.New(e.UpdatedAt)}
	if e.DeletedAt != nil {
		out.DeletedAt = timestamppb.New(*e.DeletedAt)
	}
	return out
}
func mutationHash(operation string, value any) string {
	b, _ := json.Marshal(struct {
		Operation string `json:"operation"`
		Value     any    `json:"value"`
	}{operation, value})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func requestHash(owner, operation, ref, legacyID string, revision int64, value any) string {
	return mutationHash(operation, struct {
		Owner, Ref, ID   string
		ExpectedRevision int64
		Value            any
	}{owner, ref, legacyID, revision, value})
}
func receiptResponse(r *ent.Receipt, replayed bool) *timelinev1.MutationReceipt {
	if r == nil {
		return nil
	}
	out := &timelinev1.MutationReceipt{MutationId: r.MutationID, TargetKind: r.TargetKind, Operation: r.Operation, ResultRevision: int64(r.ResultRevision), ResultState: r.ResultState, Replayed: replayed, RecordedAt: timestamppb.New(r.CreatedAt), ResultSnapshot: r.ResultSnapshot}
	if r.EntryRef != nil {
		out.EntryRef = *r.EntryRef
	}
	if r.MediaRef != nil {
		out.MediaRef = *r.MediaRef
	}
	return out
}
func (s *TimelineService) priorReceiptTx(ctx context.Context, tx *ent.Tx, uid, mutationID, operation, hash string) (*ent.Receipt, error) {
	if mutationID == "" {
		return nil, status.Error(codes.InvalidArgument, "mutation_id is required")
	}
	r, err := tx.Receipt.Query().Where(receipt.UserIDEQ(uid), receipt.MutationIDEQ(mutationID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r.Operation != operation || r.RequestHash != hash {
		return nil, status.Error(codes.AlreadyExists, "mutation_id was already used for a different request")
	}
	return r, nil
}
func saveReceiptTx(ctx context.Context, tx *ent.Tx, uid, mutationID, operation, hash, ref string, revision int, state string, snapshot []byte) (*ent.Receipt, error) {
	return tx.Receipt.Create().SetUserID(uid).SetMutationID(mutationID).SetTargetKind("entry").SetOperation(operation).SetRequestHash(hash).SetEntryRef(ref).SetResultRevision(revision).SetResultState(state).SetResultSnapshot(snapshot).Save(ctx)
}
func (s *TimelineService) priorReceipt(ctx context.Context, uid, mutationID, operation, hash string) (*ent.Receipt, error) {
	if mutationID == "" {
		return nil, status.Error(codes.InvalidArgument, "mutation_id is required")
	}
	r, err := s.db.Receipt.Query().Where(receipt.UserIDEQ(uid), receipt.MutationIDEQ(mutationID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r.Operation != operation || r.RequestHash != hash {
		return nil, status.Error(codes.AlreadyExists, "mutation_id was already used for a different request")
	}
	return r, nil
}

func snapshotEntry(e *ent.Entry) ([]byte, error) { return protojson.Marshal(mapEntry(e)) }
func restoreEntrySnapshot(b []byte) (*timelinev1.Entry, error) {
	e := &timelinev1.Entry{}
	err := protojson.Unmarshal(b, e)
	return e, err
}

func (s *TimelineService) saveReceipt(ctx context.Context, uid, mutationID, operation, hash, entryRef string, revision int, state string, snapshot []byte) (*ent.Receipt, error) {
	return s.db.Receipt.Create().SetUserID(uid).SetMutationID(mutationID).SetTargetKind("entry").SetOperation(operation).SetRequestHash(hash).SetEntryRef(entryRef).SetResultRevision(revision).SetResultState(state).SetResultSnapshot(snapshot).Save(ctx)
}
func (s *TimelineService) ListEntries(ctx context.Context, req *timelinev1.ListEntriesRequest) (*timelinev1.ListEntriesResponse, error) {
	uid, err := user(ctx)
	if err != nil {
		return nil, err
	}
	q := s.db.Entry.Query().Where(entry.UserID(uid))
	if !req.IncludeDeleted {
		q = q.Where(entry.DeletedAtIsNil())
	}
	if req.Category != "" {
		q = q.Where(entry.CategoryEQ(req.Category))
	}
	if req.Query != "" {
		q = q.Where(entry.Or(entry.TitleContainsFold(req.Query), entry.BodyContainsFold(req.Query), entry.LocationContainsFold(req.Query)))
	}
	if req.FromDate != "" {
		q = q.Where(entry.OccurredDateGTE(req.FromDate))
	}
	if req.ToDate != "" {
		q = q.Where(entry.OccurredDateLTE(req.ToDate))
	}
	pageSize := int(req.PageSize)
	if pageSize == 0 {
		pageSize = 50
	}
	if pageSize < 1 || pageSize > 100 {
		return nil, status.Error(codes.InvalidArgument, "page_size must be between 1 and 100")
	}
	if req.PageToken != "" {
		cursor, err := decodeListCursor(req.PageToken)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid page_token")
		}
		if cursor.Filter != listFilter(req) {
			return nil, status.Error(codes.InvalidArgument, "page_token does not match filters")
		}
		q = q.Where(entry.Or(
			entry.OccurredDateLT(cursor.OccurredDate),
			entry.And(entry.OccurredDateEQ(cursor.OccurredDate), entry.Or(
				entry.OccurredTimeLT(cursor.OccurredTime),
				entry.And(entry.OccurredTimeEQ(cursor.OccurredTime), entry.Or(
					entry.CreatedAtLT(cursor.CreatedAt),
					entry.And(entry.CreatedAtEQ(cursor.CreatedAt), entry.IDGT(cursor.ID)),
				)),
			)),
		))
	}
	rows, err := q.Order(ent.Desc(entry.FieldOccurredDate), ent.Desc(entry.FieldOccurredTime), ent.Desc(entry.FieldCreatedAt), ent.Asc(entry.FieldID)).Limit(pageSize + 1).All(ctx)
	if err != nil {
		return nil, err
	}
	out := &timelinev1.ListEntriesResponse{}
	if len(rows) > pageSize {
		last := rows[pageSize-1]
		out.NextPageToken = encodeListCursor(last.OccurredDate, last.OccurredTime, last.CreatedAt, last.ID, listFilter(req))
		rows = rows[:pageSize]
	}
	for _, row := range rows {
		out.Entries = append(out.Entries, mapEntry(row))
	}
	return out, nil
}

type listCursor struct {
	OccurredDate string    `json:"occurred_date"`
	OccurredTime string    `json:"occurred_time"`
	CreatedAt    time.Time `json:"created_at"`
	ID           string    `json:"id"`
	Filter       string    `json:"filter"`
}

func encodeListCursor(occurredDate, occurredTime string, createdAt time.Time, id string, filters ...string) string {
	filter := "legacy"
	if len(filters) > 0 {
		filter = filters[0]
	}
	b, _ := json.Marshal(listCursor{OccurredDate: occurredDate, OccurredTime: occurredTime, CreatedAt: createdAt.UTC(), ID: id, Filter: filter})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeListCursor(token string) (listCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return listCursor{}, err
	}
	var c listCursor
	if err := json.Unmarshal(b, &c); err != nil || c.ID == "" || c.OccurredDate == "" || c.CreatedAt.IsZero() || c.Filter == "" {
		return listCursor{}, errors.New("invalid cursor")
	}
	return c, nil
}

func listFilter(req *timelinev1.ListEntriesRequest) string {
	b, _ := json.Marshal(struct {
		Query, Category, From, To string
		Deleted                   bool
	}{req.Query, req.Category, req.FromDate, req.ToDate, req.IncludeDeleted})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *TimelineService) CreateEntry(ctx context.Context, req *timelinev1.CreateEntryRequest) (*timelinev1.EntryResponse, error) {
	uid, err := user(ctx)
	if err != nil {
		return nil, err
	}
	if err = validate(req.Entry, false); err != nil {
		return nil, err
	}
	if err := validateMutationID(req.MutationId); err != nil {
		return nil, err
	}

	hash := requestHash(uid, "create", "", "", 0, req.Entry)
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := ensureCategories(ctx, tx, uid); err != nil {
		return nil, err
	}
	if err := validateCategoryTx(ctx, tx, uid, req.Entry.Category); err != nil {
		return nil, err
	}
	old, err := s.priorReceiptTx(ctx, tx, uid, req.MutationId, "create", hash)
	if err != nil {
		return nil, err
	}
	if old != nil {
		e, eerr := restoreEntrySnapshot(old.ResultSnapshot)
		if eerr != nil {
			return nil, eerr
		}
		return &timelinev1.EntryResponse{Entry: e, Receipt: receiptResponse(old, true)}, nil
	}
	row, err := tx.Entry.Create().SetPublicRef(publicRef("ent_")).SetUserID(uid).SetTitle(strings.TrimSpace(req.Entry.Title)).SetBody(req.Entry.Note).SetOccurredDate(req.Entry.Date).SetOccurredTime(req.Entry.Time).SetTimezone(req.Entry.Timezone).SetCategory(req.Entry.Category).SetLocation(req.Entry.Location).SetAmountMinor(req.Entry.AmountMinor).SetAmountPresent(req.Entry.AmountPresent || req.Entry.AmountMinor != 0 || req.Entry.Currency != "").SetCurrency(req.Entry.Currency).SetURL(req.Entry.Url).SetURLTitle(req.Entry.UrlTitle).SetURLNote(req.Entry.UrlNote).SetOccurredAt(occurred(req.Entry)).Save(ctx)
	if err != nil {
		return nil, err
	}
	snap, err := snapshotEntry(row)
	if err != nil {
		return nil, err
	}
	r, err := saveReceiptTx(ctx, tx, uid, req.MutationId, "create", hash, row.PublicRef, row.Revision, "active", snap)
	if err != nil {
		if sqlgraph.IsConstraintError(err) {
			_ = tx.Rollback()
			committed = true
			if old, readErr := s.priorReceipt(ctx, uid, req.MutationId, "create", hash); readErr != nil {
				return nil, readErr
			} else if old != nil {
				e, eerr := restoreEntrySnapshot(old.ResultSnapshot)
				if eerr != nil {
					return nil, eerr
				}
				return &timelinev1.EntryResponse{Entry: e, Receipt: receiptResponse(old, true)}, nil
			}
		}
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return &timelinev1.EntryResponse{Entry: mapEntry(row), Receipt: receiptResponse(r, false)}, nil
}
func (s *TimelineService) UpdateEntry(ctx context.Context, req *timelinev1.UpdateEntryRequest) (*timelinev1.EntryResponse, error) {
	uid, err := user(ctx)
	if err != nil {
		return nil, err
	}
	if err = validate(req.Entry, true); err != nil {
		return nil, err
	}
	if err = validateLocator(req.Entry.EntryRef, req.Entry.Id); err != nil {
		return nil, err
	}
	if err := validateMutationID(req.MutationId); err != nil {
		return nil, err
	}
	if err := requireRevision(req.ExpectedRevision); err != nil {
		return nil, err
	}

	hash := requestHash(uid, "update", req.Entry.EntryRef, req.Entry.Id, req.ExpectedRevision, req.Entry)
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCategoryTx(ctx, tx, uid, req.Entry.Category); err != nil {
		return nil, err
	}
	old, err := s.priorReceiptTx(ctx, tx, uid, req.MutationId, "update", hash)
	if err != nil {
		return nil, err
	}
	if old != nil {
		e, eerr := restoreEntrySnapshot(old.ResultSnapshot)
		if eerr != nil {
			return nil, eerr
		}
		return &timelinev1.EntryResponse{Entry: e, Receipt: receiptResponse(old, true)}, nil
	}
	q := tx.Entry.Query().Where(entry.UserID(uid), entry.DeletedAtIsNil())
	if req.Entry.EntryRef != "" {
		q = q.Where(entry.PublicRefEQ(req.Entry.EntryRef))
	} else {
		q = q.Where(entry.ID(req.Entry.Id))
	}
	row, err := q.Only(ctx)
	if err != nil {
		return nil, status.Error(codes.NotFound, "entry not found")
	}
	if req.ExpectedRevision != 0 && int64(row.Revision) != req.ExpectedRevision {
		return nil, status.Error(codes.Aborted, "revision conflict")
	}
	updated, err := tx.Entry.Update().Where(entry.ID(row.ID), entry.UserID(uid), entry.RevisionEQ(row.Revision), entry.DeletedAtIsNil()).SetTitle(strings.TrimSpace(req.Entry.Title)).SetBody(req.Entry.Note).SetOccurredDate(req.Entry.Date).SetOccurredTime(req.Entry.Time).SetTimezone(req.Entry.Timezone).SetCategory(req.Entry.Category).SetLocation(req.Entry.Location).SetAmountMinor(req.Entry.AmountMinor).SetAmountPresent(req.Entry.AmountPresent || req.Entry.AmountMinor != 0 || req.Entry.Currency != "").SetCurrency(req.Entry.Currency).SetURL(req.Entry.Url).SetURLTitle(req.Entry.UrlTitle).SetURLNote(req.Entry.UrlNote).SetOccurredAt(occurred(req.Entry)).SetRevision(row.Revision + 1).Save(ctx)
	if err != nil {
		return nil, err
	}
	if updated != 1 {
		return nil, status.Error(codes.Aborted, "revision conflict")
	}
	row, err = tx.Entry.Query().Where(entry.ID(row.ID), entry.UserID(uid)).Only(ctx)
	if err != nil {
		return nil, err
	}
	snap, err := snapshotEntry(row)
	if err != nil {
		return nil, err
	}
	r, err := saveReceiptTx(ctx, tx, uid, req.MutationId, "update", hash, row.PublicRef, row.Revision, "active", snap)
	if err != nil {
		if sqlgraph.IsConstraintError(err) {
			_ = tx.Rollback()
			committed = true
			if old, readErr := s.priorReceipt(ctx, uid, req.MutationId, "update", hash); readErr != nil {
				return nil, readErr
			} else if old != nil {
				e, eerr := restoreEntrySnapshot(old.ResultSnapshot)
				if eerr != nil {
					return nil, eerr
				}
				return &timelinev1.EntryResponse{Entry: e, Receipt: receiptResponse(old, true)}, nil
			}
		}
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return &timelinev1.EntryResponse{Entry: mapEntry(row), Receipt: receiptResponse(r, false)}, nil
}
func (s *TimelineService) DeleteEntry(ctx context.Context, req *timelinev1.DeleteEntryRequest) (*timelinev1.DeleteEntryResponse, error) {
	uid, err := user(ctx)
	if err != nil {
		return nil, err
	}
	if err = validateLocator(req.EntryRef, req.Id); err != nil {
		return nil, err
	}
	if req.Permanent {
		return nil, status.Error(codes.InvalidArgument, "permanent deletion is not supported; use PurgeEntry")
	}
	if err := validateMutationID(req.MutationId); err != nil {
		return nil, err
	}
	if err := requireRevision(req.ExpectedRevision); err != nil {
		return nil, err
	}

	hash := requestHash(uid, "trash", req.EntryRef, req.Id, req.ExpectedRevision, req.Permanent)
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	old, err := s.priorReceiptTx(ctx, tx, uid, req.MutationId, "trash", hash)
	if err != nil {
		return nil, err
	}
	if old != nil {
		e, eerr := restoreEntrySnapshot(old.ResultSnapshot)
		if eerr != nil {
			return nil, eerr
		}
		return &timelinev1.DeleteEntryResponse{Entry: e, Receipt: receiptResponse(old, true)}, nil
	}
	q := tx.Entry.Query().Where(entry.UserID(uid))
	if req.EntryRef != "" {
		q = q.Where(entry.PublicRefEQ(req.EntryRef))
	} else {
		q = q.Where(entry.ID(req.Id))
	}
	row, err := q.Only(ctx)
	if err != nil {
		return nil, status.Error(codes.NotFound, "entry not found")
	}
	if req.ExpectedRevision != 0 && int64(row.Revision) != req.ExpectedRevision {
		return nil, status.Error(codes.Aborted, "revision conflict")
	}
	updated, err := tx.Entry.Update().Where(entry.ID(row.ID), entry.UserID(uid), entry.RevisionEQ(row.Revision), entry.DeletedAtIsNil()).SetDeletedAt(time.Now().UTC()).SetRevision(row.Revision + 1).Save(ctx)
	if err != nil {
		return nil, err
	}
	if updated != 1 {
		return nil, status.Error(codes.Aborted, "revision conflict")
	}
	row, err = tx.Entry.Query().Where(entry.ID(row.ID), entry.UserID(uid)).Only(ctx)
	if err != nil {
		return nil, err
	}
	snap, err := snapshotEntry(row)
	if err != nil {
		return nil, err
	}
	r, err := saveReceiptTx(ctx, tx, uid, req.MutationId, "trash", hash, row.PublicRef, row.Revision, "trashed", snap)
	if err != nil {
		if sqlgraph.IsConstraintError(err) {
			_ = tx.Rollback()
			committed = true
			if old, readErr := s.priorReceipt(ctx, uid, req.MutationId, "trash", hash); readErr != nil {
				return nil, readErr
			} else if old != nil {
				e, eerr := restoreEntrySnapshot(old.ResultSnapshot)
				if eerr != nil {
					return nil, eerr
				}
				return &timelinev1.DeleteEntryResponse{Entry: e, Receipt: receiptResponse(old, true)}, nil
			}
		}
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return &timelinev1.DeleteEntryResponse{Entry: mapEntry(row), Receipt: receiptResponse(r, false)}, nil
}
func (s *TimelineService) RestoreEntry(ctx context.Context, req *timelinev1.RestoreEntryRequest) (*timelinev1.EntryResponse, error) {
	uid, err := user(ctx)
	if err != nil {
		return nil, err
	}
	if err = validateLocator(req.EntryRef, req.Id); err != nil {
		return nil, err
	}
	if err := validateMutationID(req.MutationId); err != nil {
		return nil, err
	}
	if err := requireRevision(req.ExpectedRevision); err != nil {
		return nil, err
	}

	hash := requestHash(uid, "restore", req.EntryRef, req.Id, req.ExpectedRevision, nil)
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	old, err := s.priorReceiptTx(ctx, tx, uid, req.MutationId, "restore", hash)
	if err != nil {
		return nil, err
	}
	if old != nil {
		e, eerr := restoreEntrySnapshot(old.ResultSnapshot)
		if eerr != nil {
			return nil, eerr
		}
		return &timelinev1.EntryResponse{Entry: e, Receipt: receiptResponse(old, true)}, nil
	}
	q := tx.Entry.Query().Where(entry.UserID(uid))
	if req.EntryRef != "" {
		q = q.Where(entry.PublicRefEQ(req.EntryRef))
	} else {
		q = q.Where(entry.ID(req.Id))
	}
	row, err := q.Only(ctx)
	if err != nil {
		return nil, status.Error(codes.NotFound, "entry not found")
	}
	if req.ExpectedRevision != 0 && int64(row.Revision) != req.ExpectedRevision {
		return nil, status.Error(codes.Aborted, "revision conflict")
	}
	updated, err := tx.Entry.Update().Where(entry.ID(row.ID), entry.UserID(uid), entry.RevisionEQ(row.Revision)).ClearDeletedAt().SetRevision(row.Revision + 1).Save(ctx)
	if err != nil {
		return nil, err
	}
	if updated != 1 {
		return nil, status.Error(codes.Aborted, "revision conflict")
	}
	row, err = tx.Entry.Query().Where(entry.ID(row.ID), entry.UserID(uid)).Only(ctx)
	if err != nil {
		return nil, err
	}
	snap, err := snapshotEntry(row)
	if err != nil {
		return nil, err
	}
	r, err := saveReceiptTx(ctx, tx, uid, req.MutationId, "restore", hash, row.PublicRef, row.Revision, "active", snap)
	if err != nil {
		if sqlgraph.IsConstraintError(err) {
			_ = tx.Rollback()
			committed = true
			if old, readErr := s.priorReceipt(ctx, uid, req.MutationId, "restore", hash); readErr != nil {
				return nil, readErr
			} else if old != nil {
				e, eerr := restoreEntrySnapshot(old.ResultSnapshot)
				if eerr != nil {
					return nil, eerr
				}
				return &timelinev1.EntryResponse{Entry: e, Receipt: receiptResponse(old, true)}, nil
			}
		}
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return &timelinev1.EntryResponse{Entry: mapEntry(row), Receipt: receiptResponse(r, false)}, nil
}
func (s *TimelineService) PurgeEntry(ctx context.Context, req *timelinev1.PurgeEntryRequest) (*timelinev1.PurgeEntryResponse, error) {
	uid, err := user(ctx)
	if err != nil {
		return nil, err
	}
	if err = validateLocator(req.EntryRef, req.Id); err != nil {
		return nil, err
	}
	if err := validateMutationID(req.MutationId); err != nil {
		return nil, err
	}
	if err := requireRevision(req.ExpectedRevision); err != nil {
		return nil, err
	}

	hash := requestHash(uid, "purge", req.EntryRef, req.Id, req.ExpectedRevision, nil)
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	old, err := s.priorReceiptTx(ctx, tx, uid, req.MutationId, "purge", hash)
	if err != nil {
		return nil, err
	}
	if old != nil {
		return &timelinev1.PurgeEntryResponse{Receipt: receiptResponse(old, true)}, nil
	}
	q := tx.Entry.Query().Where(entry.UserID(uid))
	if req.EntryRef != "" {
		q = q.Where(entry.PublicRefEQ(req.EntryRef))
	} else {
		q = q.Where(entry.ID(req.Id))
	}
	row, err := q.Only(ctx)
	if err != nil {
		return nil, status.Error(codes.NotFound, "entry not found")
	}
	if req.ExpectedRevision != 0 && int64(row.Revision) != req.ExpectedRevision {
		return nil, status.Error(codes.Aborted, "revision conflict")
	}
	snap, err := snapshotEntry(row)
	if err != nil {
		return nil, err
	}
	deleted, err := tx.Entry.Delete().Where(entry.ID(row.ID), entry.UserID(uid), entry.RevisionEQ(row.Revision)).Exec(ctx)
	if err != nil {
		return nil, err
	}
	if deleted != 1 {
		return nil, status.Error(codes.Aborted, "revision conflict")
	}
	r, err := saveReceiptTx(ctx, tx, uid, req.MutationId, "purge", hash, row.PublicRef, row.Revision, "purged", snap)
	if err != nil {
		if sqlgraph.IsConstraintError(err) {
			_ = tx.Rollback()
			committed = true
			if old, readErr := s.priorReceipt(ctx, uid, req.MutationId, "purge", hash); readErr != nil {
				return nil, readErr
			} else if old != nil {
				return &timelinev1.PurgeEntryResponse{Receipt: receiptResponse(old, true)}, nil
			}
		}
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return &timelinev1.PurgeEntryResponse{Receipt: receiptResponse(r, false)}, nil
}

var _ = errors.New
var _ = fmt.Sprintf
