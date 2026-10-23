package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql/sqlgraph"
	"github.com/kongken/mono/internal/metro/ent"
	"github.com/kongken/mono/internal/metro/ent/adminauditlog"
	"github.com/kongken/mono/internal/metro/ent/city"
	"github.com/kongken/mono/internal/metro/ent/line"
	"github.com/kongken/mono/internal/metro/ent/rollingstock"
	"github.com/kongken/mono/internal/platform/rpcauth"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/protoadapt"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Audit entity types.
const (
	entityCity    = "city"
	entityStock   = "rolling_stock"
	entityLine    = "line"
	entityTopic   = "topic"
	defaultPage   = 50
	maxPage       = 200
	maxImportSize = 1 << 20
)

// LineGuard counts what still refers to a line and so keeps it from being
// deleted (for example rider records). entityType names what was counted.
type LineGuard func(ctx context.Context, tx *ent.Tx, lineID string) (entityType string, count int64, err error)

// AdminService maintains the catalog. Every method needs the metro-admin role,
// which the rpcauth interceptor enforces before a handler runs, so handlers only
// deal with the data. Each write runs in one transaction that also writes the
// audit row and raises the catalog version.
type AdminService struct {
	metrov1.UnimplementedAdminServiceServer
	db     *ent.Client
	guards []LineGuard
}

// NewAdminService returns the admin API over db. guards extend the rule that a
// line in use cannot be deleted.
func NewAdminService(db *ent.Client, guards ...LineGuard) *AdminService {
	return &AdminService{db: db, guards: guards}
}

// ---- errors ----

// withDetail returns a status error carrying a typed detail message.
func withDetail(code codes.Code, msg string, detail proto.Message) error {
	st, err := status.New(code, msg).WithDetails(protoadapt.MessageV1Of(detail))
	if err != nil {
		return status.Error(code, msg)
	}
	return st.Err()
}

// summarize makes a readable one-line message from field errors, for clients
// that show only the status message.
func summarize(errs []*metrov1.FieldError) string {
	parts := make([]string, 0, 3)
	for i, e := range errs {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("and %d more", len(errs)-3))
			break
		}
		parts = append(parts, e.GetField()+": "+e.GetReason())
	}
	return strings.Join(parts, "; ")
}

func invalid(errs []*metrov1.FieldError) error {
	return withDetail(codes.InvalidArgument, "invalid catalog data: "+summarize(errs), &metrov1.ValidationDetail{Errors: errs})
}

func invalidField(field, reason string) error {
	return invalid([]*metrov1.FieldError{{Field: field, Reason: reason}})
}

func alreadyExists(field string) error {
	errs := []*metrov1.FieldError{{Field: field, Reason: "already exists"}}
	return withDetail(codes.AlreadyExists, "already exists: "+field, &metrov1.ValidationDetail{Errors: errs})
}

func notFound(what string) error { return status.Error(codes.NotFound, what+" not found") }

func revisionConflict(current int) error {
	return withDetail(codes.FailedPrecondition,
		fmt.Sprintf("the record changed since it was read: current revision is %d", current),
		&metrov1.RevisionConflictDetail{CurrentRevision: int64(current)})
}

func referenced(what string, refs ...*metrov1.EntityCount) error {
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		parts = append(parts, fmt.Sprintf("%d %s", r.GetCount(), r.GetEntityType()))
	}
	return withDetail(codes.FailedPrecondition, what+" is still referenced by "+strings.Join(parts, ", "),
		&metrov1.ReferencedDetail{References: refs})
}

// uniqueOr turns a unique violation into ALREADY_EXISTS on field and passes any
// other error through.
func uniqueOr(err error, field string) error {
	if sqlgraph.IsUniqueConstraintError(err) {
		return alreadyExists(field)
	}
	return err
}

// target validates the id and expected_revision every update and delete carries.
func target(id string, expected int64) error {
	var errs errList
	if id == "" {
		errs.add("id", "is required")
	}
	if expected < 1 {
		errs.add("expected_revision", "is required: send the revision you last read")
	}
	if errs.any() {
		return invalid(errs.errs)
	}
	return nil
}

// ---- transaction with audit ----

type writer struct {
	ctx     context.Context
	tx      *ent.Tx
	who     rpcauth.Call
	changed int
}

// record writes one audit row for a change made in this transaction. before and
// after are the record snapshots (nil when absent).
func (w *writer) record(action, entityType, entityID string, before, after any) error {
	create := w.tx.AdminAuditLog.Create().
		SetActorUserID(w.who.UserID).SetChannel(adminauditlog.Channel(w.who.Channel)).
		SetTokenID(w.who.TokenID).SetRequestID(w.who.RequestID).
		SetAction(action).SetEntityType(entityType).SetEntityID(entityID)
	for _, snap := range []struct {
		v   any
		set func(string) *ent.AdminAuditLogCreate
	}{{before, create.SetBeforeJSON}, {after, create.SetAfterJSON}} {
		if snap.v == nil {
			continue
		}
		b, err := json.Marshal(snap.v)
		if err != nil {
			return err
		}
		snap.set(string(b))
	}
	w.changed++
	return create.Exec(w.ctx)
}

// write runs fn in a transaction. When fn recorded changes the catalog version
// is raised in the same transaction, so a cache keyed by version never misses
// them; the new version is returned.
func (s *AdminService) write(ctx context.Context, fn func(w *writer) error) (int64, error) {
	who := rpcauth.CallFrom(ctx)
	if who.UserID == "" {
		return 0, status.Error(codes.PermissionDenied, "administrator identity required")
	}
	who.Channel = rpcauth.NormalizeChannel(who.Channel)
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return 0, internalError("admin begin", err)
	}
	defer func() { _ = tx.Rollback() }()
	w := &writer{ctx: ctx, tx: tx, who: who}
	if err = fn(w); err != nil {
		if _, ok := status.FromError(err); ok {
			return 0, err
		}
		return 0, internalError("admin write", err)
	}
	var version int64
	if w.changed > 0 {
		version, err = BumpVersion(ctx, tx)
	} else {
		var row *ent.CatalogState
		if row, err = tx.CatalogState.Get(ctx, stateID); err == nil {
			version = row.Version
		}
	}
	if err != nil {
		return 0, internalError("admin version", err)
	}
	if err = tx.Commit(); err != nil {
		return 0, internalError("admin commit", err)
	}
	return version, nil
}

// ---- applying records to rows ----

func applyCity(m *ent.CityMutation, d cityDoc) {
	m.SetSlug(d.Slug)
	m.SetName(d.Name)
	m.SetHeaderImages(d.HeaderImages)
	m.SetMapSvgURL(d.MapSVGURL)
	m.SetSortOrder(d.SortOrder)
}

func applyStock(m *ent.RollingStockMutation, d stockDoc, create bool) {
	m.SetCode(d.Code)
	setOrClear(d.LengthM, create, m.SetLengthM, m.ClearLengthM)
	setOrClear(d.WidthM, create, m.SetWidthM, m.ClearWidthM)
	setOrClear(d.Capacity, create, m.SetCapacity, m.ClearCapacity)
}

func setOrClear[T any](v *T, create bool, set func(T), clear func()) {
	switch {
	case v != nil:
		set(*v)
	case !create:
		clear()
	}
}

// applyLine writes a line's fields; the code is set only on creation because it
// never changes afterwards.
func applyLine(m *ent.LineMutation, d lineDoc, cityID string, stockID *string, create bool) {
	if create {
		m.SetCode(d.Code)
	}
	m.SetCityID(cityID)
	m.SetName(d.Name)
	m.SetNumber(d.Number)
	m.SetStatus(line.Status(d.Status))
	setOrClear(stockID, create, m.SetRollingStockID, m.ClearRollingStockID)
	m.SetCars(d.Cars)
	m.SetCarsAddition(d.CarsAddition)
	m.SetStations(d.Stations)
	m.SetLengthKm(d.LengthKm)
	m.SetColor(d.Color)
	for _, date := range []struct {
		v          string
		set        func(t time.Time)
		clearField func()
	}{{d.CommencedOn, m.SetCommencedOn, m.ClearCommencedOn}, {d.ExtendedOn, m.SetExtendedOn, m.ClearExtendedOn}} {
		if t, ok := parseDate(date.v); ok {
			date.set(t)
		} else if !create {
			date.clearField()
		}
	}
	m.SetIsLoop(d.IsLoop)
}

func applyTopic(m *ent.RollingStockTopicMutation, d topicDoc) {
	m.SetSlug(d.Slug)
	m.SetName(d.Name)
	m.SetRollingStockCodes(d.RollingStockCodes)
	m.SetCars(d.Cars)
}

// resolveRefs finds the city and rolling stock a line names. A missing one is a
// field error, not a failure.
func resolveRefs(ctx context.Context, tx *ent.Tx, d lineDoc, p string, errs *errList) (cityID string, stockID *string, err error) {
	cityID, err = tx.City.Query().Where(city.Slug(d.City)).OnlyID(ctx)
	switch {
	case ent.IsNotFound(err):
		errs.add(p+"city", "no city with slug %q", d.City)
	case err != nil:
		return "", nil, err
	}
	if d.RollingStock != "" {
		id, serr := tx.RollingStock.Query().Where(rollingstock.Code(d.RollingStock)).OnlyID(ctx)
		switch {
		case ent.IsNotFound(serr):
			errs.add(p+"rolling_stock", "no rolling stock with code %q", d.RollingStock)
		case serr != nil:
			return "", nil, serr
		default:
			stockID = &id
		}
	}
	return cityID, stockID, nil
}

// ---- messages ----

func adminCity(c *ent.City) *metrov1.AdminCity {
	return &metrov1.AdminCity{Id: c.ID, Revision: int64(c.Revision), City: cityInputOf(cityDocOf(c)),
		CreatedAt: timestamppb.New(c.CreatedAt), UpdatedAt: timestamppb.New(c.UpdatedAt)}
}

func adminStock(r *ent.RollingStock) *metrov1.AdminRollingStock {
	return &metrov1.AdminRollingStock{Id: r.ID, Revision: int64(r.Revision), RollingStock: stockInputOf(stockDocOf(r)),
		CreatedAt: timestamppb.New(r.CreatedAt), UpdatedAt: timestamppb.New(r.UpdatedAt)}
}

func adminLine(l *ent.Line) *metrov1.AdminLine {
	return &metrov1.AdminLine{Id: l.ID, Revision: int64(l.Revision), Line: lineInputOf(lineDocOf(l)),
		CreatedAt: timestamppb.New(l.CreatedAt), UpdatedAt: timestamppb.New(l.UpdatedAt)}
}

func adminTopic(t *ent.RollingStockTopic) *metrov1.AdminRollingStockTopic {
	return &metrov1.AdminRollingStockTopic{Id: t.ID, Revision: int64(t.Revision), Topic: topicInputOf(topicDocOf(t)),
		CreatedAt: timestamppb.New(t.CreatedAt), UpdatedAt: timestamppb.New(t.UpdatedAt)}
}

// validated returns the record or the validation error for a nil or bad input.
func validated[D interface{ validate(string, *errList) }](d D, present bool, field string) error {
	if !present {
		return invalidField(field, "is required")
	}
	var errs errList
	d.validate("", &errs)
	if errs.any() {
		return invalid(errs.errs)
	}
	return nil
}
