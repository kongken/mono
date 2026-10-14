package application

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"regexp"
	"time"

	"connectrpc.com/connect"
	"go.orx.me/apps/trip-map/internal/auth"
	"go.orx.me/apps/trip-map/internal/config"
	"go.orx.me/apps/trip-map/internal/ent"
	"go.orx.me/apps/trip-map/internal/ent/cityvisit"
	"go.orx.me/apps/trip-map/internal/ent/provincemark"
	"go.orx.me/apps/trip-map/internal/ent/user"
	"go.orx.me/apps/trip-map/internal/regions"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// noteLimit counts UTF-16 code units, matching the browser's string length.
	noteLimit       = 1000
	writeWindow     = time.Minute
	writesPerWindow = 600
)

// footprintColors is the frontend palette; the Ent schema enforces it as well.
var footprintColors = map[string]bool{"#4b937d": true, "#e2af5c": true, "#d88370": true, "#719fc6": true, "#a08cbd": true}

var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// FootprintService implements tripmap.v1.FootprintService. Every query is
// scoped by the principal's user ID; the ID never comes from the request.
type FootprintService struct {
	cfg     *config.AppConfig
	src     StoreSource
	regions *regions.Index
}

func NewFootprintService(cfg *config.AppConfig, src StoreSource, idx *regions.Index) *FootprintService {
	return &FootprintService{cfg: cfg, src: src, regions: idx}
}

func invalid(msg string) error { return connect.NewError(connect.CodeInvalidArgument, errors.New(msg)) }

func (s *FootprintService) validateVisit(v *tripmapv1.Visit) error {
	if v == nil {
		return invalid("visit is required")
	}
	name, province, ok := s.regions.City(v.GetCode())
	if !ok {
		return invalid("unknown region code")
	}
	if v.GetName() != name || v.GetProvince() != province {
		return invalid("region name or province does not match the region index")
	}
	if !footprintColors[v.GetColor()] {
		return invalid("unsupported color")
	}
	return validateDateNote(v.GetDate(), v.GetNote())
}

func (s *FootprintService) validateMark(m *tripmapv1.ProvinceMark) error {
	if m == nil {
		return invalid("mark is required")
	}
	if !s.regions.HasProvince(m.GetProvince()) {
		return invalid("unknown province code")
	}
	if !footprintColors[m.GetColor()] {
		return invalid("unsupported color")
	}
	return nil
}

func toVisit(v *ent.CityVisit) *tripmapv1.Visit {
	return &tripmapv1.Visit{Code: v.RegionCode, Name: v.RegionName, Province: regions.CanonicalProvince(v.RegionCode, v.ProvinceCode), Color: v.Color, Date: v.TravelDate, Note: v.Note, UpdatedAt: timestamppb.New(v.UpdatedAt)}
}

func toMark(m *ent.ProvinceMark) *tripmapv1.ProvinceMark {
	return &tripmapv1.ProvinceMark{Province: m.ProvinceCode, Color: m.Color, UpdatedAt: timestamppb.New(m.UpdatedAt)}
}

func (s *FootprintService) GetFootprints(ctx context.Context, _ *connect.Request[tripmapv1.GetFootprintsRequest]) (*connect.Response[tripmapv1.GetFootprintsResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	// One snapshot so the revision matches the records returned with it.
	tx, err := st.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	u, err := tx.User.Get(ctx, p.UserID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, errUnauthenticated
		}
		return nil, internalError(err)
	}
	visits, err := tx.CityVisit.Query().Where(cityvisit.UserID(p.UserID)).Order(ent.Asc(cityvisit.FieldRegionCode)).All(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	marks, err := tx.ProvinceMark.Query().Where(provincemark.UserID(p.UserID)).Order(ent.Asc(provincemark.FieldProvinceCode)).All(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	if err = gateRead(ctx, u.SchemaVersion); err != nil {
		return nil, err
	}
	out := &tripmapv1.GetFootprintsResponse{Revision: u.Revision}
	if clientSchema(ctx) >= schemaTrips {
		// Schema 2 view: cities without date or note, and the trips. A schema 1
		// account that has not been upgraded yet is read as one legacy trip per city.
		out.SchemaVersion = schemaTrips
		for _, v := range visits {
			out.Visits = append(out.Visits, toCity(v))
		}
		if u.SchemaVersion >= schemaTrips {
			trips, terr := loadTrips(ctx, tx, p.UserID)
			if terr != nil {
				return nil, internalError(terr)
			}
			for _, t := range trips {
				out.Trips = append(out.Trips, toTrip(t))
			}
		} else {
			for _, v := range visits {
				out.Trips = append(out.Trips, legacyTrip(v))
			}
		}
	} else {
		for _, v := range visits {
			out.Visits = append(out.Visits, toVisit(v))
		}
	}
	for _, m := range marks {
		out.ProvinceMarks = append(out.ProvinceMarks, toMark(m))
	}
	return connect.NewResponse(out), nil
}

// write runs apply inside a transaction that holds the user's row lock, so
// writes for one user are serialized and revisions are gap-free. apply reports
// whether it changed anything; only changes bump the revision, which keeps
// retries idempotent. A stale checked base revision aborts a changing write;
// requireRevision also checks zero without changing legacy client semantics.
func (s *FootprintService) write(ctx context.Context, base int64, requireRevision bool, mutation *tripmapv1.FootprintMutation, apply func(tx *ent.Tx, userID string, schema int) (bool, error)) (revision int64, changed bool, err error) {
	p, err := principal(ctx)
	if err != nil {
		return 0, false, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return 0, false, err
	}
	if base < 0 {
		return 0, false, invalid("base_revision must not be negative")
	}
	ok, err := auth.NewLimiter(st.Redis, st.prefix()).Allow(ctx, "write:"+p.UserID, writesPerWindow, writeWindow)
	if err != nil {
		slog.Error("rate limiter unavailable", "error", err)
		return 0, false, connect.NewError(connect.CodeUnavailable, errors.New("service temporarily unavailable"))
	}
	if !ok {
		return 0, false, connect.NewError(connect.CodeResourceExhausted, errors.New("too many writes, slow down"))
	}
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return 0, false, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	u, err := tx.User.Query().Where(user.ID(p.UserID)).ForUpdate().Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, false, errUnauthenticated
		}
		return 0, false, internalError(err)
	}
	if u.Disabled {
		return 0, false, errUnauthenticated
	}
	schema, err := gateWrite(ctx, tx, u)
	if err != nil {
		return 0, false, err
	}
	changed, err = apply(tx, p.UserID, schema)
	if err != nil {
		return 0, false, err
	}
	if !changed {
		return u.Revision, false, nil
	}
	if (requireRevision || base != 0) && base != u.Revision {
		conflict := connect.NewError(connect.CodeAborted, errors.New("footprints changed since base_revision"))
		if detail, derr := connect.NewErrorDetail(&tripmapv1.RevisionConflict{CurrentRevision: u.Revision}); derr == nil {
			conflict.AddDetail(detail)
		}
		return 0, false, conflict
	}
	if err = tx.User.UpdateOneID(p.UserID).AddRevision(1).Exec(ctx); err != nil {
		return 0, false, internalError(err)
	}
	if err = recordChange(ctx, tx, p.UserID, u.Revision+1, mutation, schema); err != nil {
		return 0, false, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return 0, false, internalError(err)
	}
	return u.Revision + 1, true, nil
}

func (s *FootprintService) UpsertVisit(ctx context.Context, req *connect.Request[tripmapv1.UpsertVisitRequest]) (*connect.Response[tripmapv1.UpsertVisitResponse], error) {
	v := req.Msg.GetVisit()
	if v != nil {
		v = proto.Clone(v).(*tripmapv1.Visit)
		v.Province = regions.CanonicalProvince(v.Code, v.Province)
	}
	if err := s.validateVisit(v); err != nil {
		return nil, err
	}
	mutation := &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertVisit{UpsertVisit: v}}
	var stored *ent.CityVisit
	var storedSchema int
	rev, changed, err := s.write(ctx, req.Msg.GetBaseRevision(), req.Msg.GetRequireRevision(), mutation, func(tx *ent.Tx, uid string, schema int) (bool, error) {
		changed, err := s.applyMutation(ctx, tx, uid, mutation, schema)
		if err != nil {
			return false, err
		}
		stored, err = tx.CityVisit.Query().Where(cityvisit.UserID(uid), cityvisit.RegionCode(v.GetCode())).Only(ctx)
		storedSchema = schema
		return changed, internalIf(err)
	})
	if err != nil {
		return nil, err
	}
	out := toVisit(stored)
	if storedSchema >= schemaTrips {
		// The date and note live in the legacy trip; the request carried the values just stored.
		out.Date, out.Note = v.GetDate(), v.GetNote()
	}
	return connect.NewResponse(&tripmapv1.UpsertVisitResponse{Visit: out, Revision: rev, Changed: changed}), nil
}

func internalIf(err error) error {
	if err == nil {
		return nil
	}
	return internalError(err)
}

func (s *FootprintService) DeleteVisit(ctx context.Context, req *connect.Request[tripmapv1.DeleteVisitRequest]) (*connect.Response[tripmapv1.DeleteVisitResponse], error) {
	if _, _, ok := s.regions.City(req.Msg.GetCode()); !ok {
		return nil, invalid("unknown region code")
	}
	rev, changed, err := s.write(ctx, req.Msg.GetBaseRevision(), req.Msg.GetRequireRevision(), &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_DeleteVisit{DeleteVisit: req.Msg.GetCode()}}, func(tx *ent.Tx, uid string, schema int) (bool, error) {
		n, err := tx.CityVisit.Delete().Where(cityvisit.UserID(uid), cityvisit.RegionCode(req.Msg.GetCode())).Exec(ctx)
		return n > 0, wrapWrite(err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&tripmapv1.DeleteVisitResponse{Revision: rev, Deleted: changed}), nil
}

func (s *FootprintService) UpsertProvinceMark(ctx context.Context, req *connect.Request[tripmapv1.UpsertProvinceMarkRequest]) (*connect.Response[tripmapv1.UpsertProvinceMarkResponse], error) {
	if err := s.validateMark(req.Msg.GetMark()); err != nil {
		return nil, err
	}
	m := req.Msg.GetMark()
	var stored *ent.ProvinceMark
	rev, changed, err := s.write(ctx, req.Msg.GetBaseRevision(), req.Msg.GetRequireRevision(), &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertMark{UpsertMark: m}}, func(tx *ent.Tx, uid string, schema int) (bool, error) {
		existing, err := tx.ProvinceMark.Query().Where(provincemark.UserID(uid), provincemark.ProvinceCode(m.GetProvince())).Only(ctx)
		switch {
		case ent.IsNotFound(err):
			stored, err = tx.ProvinceMark.Create().SetUserID(uid).SetProvinceCode(m.GetProvince()).SetColor(m.GetColor()).Save(ctx)
			return true, wrapWrite(err)
		case err != nil:
			return false, internalError(err)
		}
		stored = existing
		if existing.Color == m.GetColor() {
			return false, nil
		}
		stored, err = existing.Update().SetColor(m.GetColor()).Save(ctx)
		return true, wrapWrite(err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&tripmapv1.UpsertProvinceMarkResponse{Mark: toMark(stored), Revision: rev, Changed: changed}), nil
}

func (s *FootprintService) DeleteProvinceMark(ctx context.Context, req *connect.Request[tripmapv1.DeleteProvinceMarkRequest]) (*connect.Response[tripmapv1.DeleteProvinceMarkResponse], error) {
	if !s.regions.HasProvince(req.Msg.GetProvince()) {
		return nil, invalid("unknown province code")
	}
	rev, changed, err := s.write(ctx, req.Msg.GetBaseRevision(), req.Msg.GetRequireRevision(), &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_DeleteMark{DeleteMark: req.Msg.GetProvince()}}, func(tx *ent.Tx, uid string, schema int) (bool, error) {
		n, err := tx.ProvinceMark.Delete().Where(provincemark.UserID(uid), provincemark.ProvinceCode(req.Msg.GetProvince())).Exec(ctx)
		return n > 0, wrapWrite(err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&tripmapv1.DeleteProvinceMarkResponse{Revision: rev, Deleted: changed}), nil
}

func wrapWrite(err error) error {
	if err == nil {
		return nil
	}
	if ent.IsValidationError(err) {
		return invalid("record failed validation")
	}
	slog.Error("footprint write", "error", err)
	return internalError(err)
}
