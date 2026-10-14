package application

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"connectrpc.com/connect"
	"go.orx.me/apps/trip-map/internal/auth"
	"go.orx.me/apps/trip-map/internal/ent"
	"go.orx.me/apps/trip-map/internal/ent/cityvisit"
	"go.orx.me/apps/trip-map/internal/ent/trip"
	"go.orx.me/apps/trip-map/internal/regions"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
	"go.orx.me/apps/trip-map/pkg/proto/tripmap/v1/tripmapv1connect"
)

type SearchService struct {
	tripmapv1connect.UnimplementedSearchServiceHandler
	src     StoreSource
	regions *regions.Index
}

func NewSearchService(src StoreSource, idx *regions.Index) *SearchService {
	return &SearchService{src: src, regions: idx}
}

func searchTerms(query string) ([]string, error) {
	if !utf8.ValidString(query) || len(utf16.Encode([]rune(query))) > 100 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("search query exceeds 100 characters"))
	}
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) > 8 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("search query exceeds 8 terms"))
	}
	return terms, nil
}

// SearchNotes uses parameterized, escaped LIKE predicates rather than a query
// language. Chinese fragments, SQL wildcard characters and quotes are literals.
// No FTS extension/tokenizer or persistent duplicate of private notes is needed.
func (s *SearchService) SearchNotes(ctx context.Context, req *connect.Request[tripmapv1.SearchNotesRequest]) (*connect.Response[tripmapv1.SearchNotesResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	terms, err := searchTerms(req.Msg.Query)
	if err != nil {
		return nil, err
	}
	if req.Msg.ProvinceCode != "" && !s.regions.HasProvince(req.Msg.ProvinceCode) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown province"))
	}
	if req.Msg.Limit > 50 || req.Msg.Offset > 100000 || (req.Msg.Offset > 0 && req.Msg.ExpectedRevision == nil) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid search page"))
	}
	limit := int(req.Msg.Limit)
	if limit == 0 {
		limit = 20
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	allowed, err := auth.NewLimiter(st.Redis, st.prefix()).Allow(ctx, "note-search:"+p.UserID, 60, time.Minute)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("search temporarily unavailable"))
	}
	if !allowed {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("too many search requests"))
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := st.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	u, err := tx.User.Get(ctx, p.UserID)
	if ent.IsNotFound(err) {
		return nil, errUnauthenticated
	}
	if err != nil {
		return nil, internalError(err)
	}
	if u.Disabled {
		return nil, errUnauthenticated
	}
	if req.Msg.ExpectedRevision != nil && *req.Msg.ExpectedRevision != u.Revision {
		return nil, connect.NewError(connect.CodeAborted, errors.New("footprints changed; restart search"))
	}
	res := &tripmapv1.SearchNotesResponse{Revision: u.Revision}
	if len(terms) == 0 {
		return privateSearchResponse(res), nil
	}
	codes := s.regions.CityCodes(req.Msg.ProvinceCode)
	if u.SchemaVersion >= schemaTrips {
		q := tx.Trip.Query().Where(trip.UserID(u.ID), trip.RegionCodeIn(codes...), trip.HasVisitWith(cityvisit.UserID(u.ID)))
		for _, term := range terms {
			q.Where(trip.NoteContainsFold(term))
		}
		rows, err := q.Order(ent.Desc(trip.FieldTravelDate), ent.Asc(trip.FieldRegionCode), ent.Asc(trip.FieldTripID)).Offset(int(req.Msg.Offset)).Limit(limit + 1).All(ctx)
		if err != nil {
			return nil, internalError(err)
		}
		res.HasMore = len(rows) > limit
		for _, row := range rows[:min(limit, len(rows))] {
			res.Matches = append(res.Matches, s.match(row.RegionCode, row.TripID, row.TravelDate, row.Note))
		}
	} else {
		// The frozen city note must never be searched after schema 2 upgrade.
		q := tx.CityVisit.Query().Where(cityvisit.UserID(u.ID), cityvisit.RegionCodeIn(codes...))
		for _, term := range terms {
			q.Where(cityvisit.NoteContainsFold(term))
		}
		rows, err := q.Order(ent.Desc(cityvisit.FieldTravelDate), ent.Asc(cityvisit.FieldRegionCode)).Offset(int(req.Msg.Offset)).Limit(limit + 1).All(ctx)
		if err != nil {
			return nil, internalError(err)
		}
		res.HasMore = len(rows) > limit
		for _, row := range rows[:min(limit, len(rows))] {
			res.Matches = append(res.Matches, s.match(row.RegionCode, legacyTripID(row.RegionCode), row.TravelDate, row.Note))
		}
	}
	res.NextOffset = req.Msg.Offset + uint32(len(res.Matches))
	return privateSearchResponse(res), nil
}

func (s *SearchService) match(code, id, date, note string) *tripmapv1.NoteSearchMatch {
	name, province, _ := s.regions.City(code)
	return &tripmapv1.NoteSearchMatch{RegionCode: code, RegionName: name, ProvinceCode: province, TripId: id, Date: date, Note: note}
}

func privateSearchResponse(res *tripmapv1.SearchNotesResponse) *connect.Response[tripmapv1.SearchNotesResponse] {
	out := connect.NewResponse(res)
	out.Header().Set("Cache-Control", "no-store")
	return out
}
