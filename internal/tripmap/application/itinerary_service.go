package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/kongken/mono/internal/tripmap/auth"
	"github.com/kongken/mono/internal/tripmap/ent"
	entitinerary "github.com/kongken/mono/internal/tripmap/ent/itinerary"
	"github.com/kongken/mono/internal/tripmap/ent/schema"
	"github.com/kongken/mono/internal/tripmap/ent/syncreceipt"
	"github.com/kongken/mono/internal/tripmap/itinerary"
	"github.com/kongken/mono/internal/tripmap/regions"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"github.com/kongken/mono/pkg/proto/tripmap/v1/tripmapv1connect"
)

const (
	itineraryReadLimit   = 120
	itineraryReadWindow  = time.Minute
	itineraryWriteLimit  = 60
	itineraryWriteWindow = time.Minute
	itineraryLegCacheTTL = 24 * time.Hour
	maxItineraryStops    = 100
	minItineraryStops    = 2
	maxTitleUTF16        = 80
	maxStopNoteUTF16     = 200
)

// ItineraryService implements multi-city itinerary management and route estimation (F11).
type ItineraryService struct {
	tripmapv1connect.UnimplementedItineraryServiceHandler
	src     StoreSource
	regions *regions.Index
}

func NewItineraryService(src StoreSource, idx *regions.Index) *ItineraryService {
	return &ItineraryService{src: src, regions: idx}
}

func (s *ItineraryService) allow(ctx context.Context, st *Store, key string, limit int64, window time.Duration) error {
	ok, err := auth.NewLimiter(st.Redis, st.prefix()).Allow(ctx, key, limit, window)
	if err != nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("itinerary service temporarily unavailable"))
	}
	if !ok {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("too many itinerary requests, slow down"))
	}
	return nil
}

func legCacheKey(st *Store, fromCode, toCode, transport string) string {
	return fmt.Sprintf("%s:itinerary:leg:v1:%s:%s:%s", st.prefix(), fromCode, toCode, itinerary.NormalizeTransport(transport))
}

func (s *ItineraryService) newLegResolver(ctx context.Context, st *Store) itinerary.LegResolver {
	memo := make(map[string]itinerary.LegEstimate)
	return func(fromCode, toCode, transport string) itinerary.LegEstimate {
		mode := itinerary.NormalizeTransport(transport)
		if fromCode == toCode {
			return itinerary.LegEstimate{Transport: mode, FromCache: true}
		}
		key := legCacheKey(st, fromCode, toCode, mode)
		if cached, ok := memo[key]; ok {
			return cached
		}
		if st != nil && st.Redis != nil {
			readCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			raw, err := st.Redis.Get(readCtx, key).Bytes()
			cancel()
			if err == nil && len(raw) > 0 {
				var est itinerary.LegEstimate
				if json.Unmarshal(raw, &est) == nil && est.StraightTenthsKm > 0 {
					est.Transport = mode
					est.FromCache = true
					memo[key] = est
					return est
				}
			}
		}
		est := itinerary.ComputeLeg(fromCode, toCode, mode)
		if st != nil && st.Redis != nil && est.StraightTenthsKm > 0 {
			if raw, err := json.Marshal(est); err == nil {
				writeCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
				_ = st.Redis.Set(writeCtx, key, raw, itineraryLegCacheTTL).Err()
				cancel()
			}
		}
		memo[key] = est
		return est
	}
}

func (s *ItineraryService) toItineraryInfo(row *ent.Itinerary, resolveLeg itinerary.LegResolver) *tripmapv1.ItineraryInfo {
	stops := make([]itinerary.StopRecord, 0, len(row.Stops))
	for _, st := range row.Stops {
		stops = append(stops, itinerary.StopRecord{
			ID:         st.ID,
			RegionCode: st.RegionCode,
			Date:       st.Date,
			Note:       st.Note,
			Transport:  st.Transport,
			TripID:     st.TripID,
		})
	}
	return itinerary.BuildInfo(
		s.regions,
		row.ItineraryID,
		row.Title,
		row.Note,
		row.Color,
		row.Version,
		stops,
		row.CreatedAt,
		row.UpdatedAt,
		resolveLeg,
	)
}

func (s *ItineraryService) ListItineraries(ctx context.Context, _ *connect.Request[tripmapv1.ListItinerariesRequest]) (*connect.Response[tripmapv1.ListItinerariesResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "itinerary:read:"+p.UserID, itineraryReadLimit, itineraryReadWindow); err != nil {
		return nil, err
	}
	rows, err := st.DB.Itinerary.Query().
		Where(entitinerary.UserID(p.UserID)).
		Order(ent.Desc(entitinerary.FieldUpdatedAt), ent.Asc(entitinerary.FieldItineraryID)).
		All(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	resolver := s.newLegResolver(ctx, st)
	items := make([]*tripmapv1.ItineraryInfo, 0, len(rows))
	for _, row := range rows {
		items = append(items, s.toItineraryInfo(row, resolver))
	}
	res := connect.NewResponse(&tripmapv1.ListItinerariesResponse{
		Itineraries: items,
		Policy:      itinerary.EstimationPolicy(),
	})
	res.Header().Set("Cache-Control", "no-store")
	return res, nil
}

func (s *ItineraryService) GetItinerary(ctx context.Context, req *connect.Request[tripmapv1.GetItineraryRequest]) (*connect.Response[tripmapv1.GetItineraryResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "itinerary:read:"+p.UserID, itineraryReadLimit, itineraryReadWindow); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(req.Msg.GetId())
	if !tripIDPattern.MatchString(id) {
		return nil, invalid("invalid itinerary id")
	}
	row, err := st.DB.Itinerary.Query().
		Where(entitinerary.UserID(p.UserID), entitinerary.ItineraryID(id)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("itinerary not found"))
	}
	if err != nil {
		return nil, internalError(err)
	}
	resolver := s.newLegResolver(ctx, st)
	res := connect.NewResponse(&tripmapv1.GetItineraryResponse{
		Itinerary: s.toItineraryInfo(row, resolver),
		Policy:    itinerary.EstimationPolicy(),
	})
	res.Header().Set("Cache-Control", "no-store")
	return res, nil
}

func (s *ItineraryService) validateSaveInput(msg *tripmapv1.SaveItineraryRequest) (string, string, string, string, []schema.ItineraryStopRecord, error) {
	id := strings.TrimSpace(msg.GetId())
	if id == "" {
		id = "itin_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	}
	if !tripIDPattern.MatchString(id) {
		return "", "", "", "", nil, invalid("invalid itinerary id")
	}
	title := strings.TrimSpace(msg.GetTitle())
	if title == "" {
		return "", "", "", "", nil, invalid("itinerary title is required")
	}
	if len(utf16.Encode([]rune(title))) > maxTitleUTF16 {
		return "", "", "", "", nil, invalid("itinerary title exceeds 80 characters")
	}
	note := msg.GetNote()
	if len(utf16.Encode([]rune(note))) > noteLimit {
		return "", "", "", "", nil, invalid("itinerary note exceeds 1000 characters")
	}
	color := strings.TrimSpace(msg.GetColor())
	if color == "" {
		color = defaultCityColor
	}
	if !footprintColors[color] {
		return "", "", "", "", nil, invalid("unsupported itinerary color")
	}
	rawStops := msg.GetStops()
	if len(rawStops) < minItineraryStops {
		return "", "", "", "", nil, invalid("an itinerary requires at least 2 city stops")
	}
	if len(rawStops) > maxItineraryStops {
		return "", "", "", "", nil, invalid("an itinerary cannot exceed 100 stops")
	}
	seenStopIDs := make(map[string]bool, len(rawStops))
	stops := make([]schema.ItineraryStopRecord, 0, len(rawStops))
	for i, raw := range rawStops {
		if raw == nil {
			return "", "", "", "", nil, invalid("stop is required")
		}
		stopID := strings.TrimSpace(raw.GetId())
		if stopID == "" {
			stopID = fmt.Sprintf("stop-%d", i+1)
		}
		if !tripIDPattern.MatchString(stopID) || seenStopIDs[stopID] {
			return "", "", "", "", nil, invalid("invalid or duplicate stop id")
		}
		seenStopIDs[stopID] = true

		code, ok := s.regions.ResolveCode(strings.TrimSpace(raw.GetRegionCode()))
		if !ok {
			return "", "", "", "", nil, invalid("unknown stop region_code")
		}
		if _, _, isCity := s.regions.City(code); !isCity {
			return "", "", "", "", nil, invalid("unknown stop region_code")
		}
		date := strings.TrimSpace(raw.GetDate())
		if validateDateNote(date, "") != nil {
			return "", "", "", "", nil, invalid("invalid stop date")
		}
		stopNote := raw.GetNote()
		if len(utf16.Encode([]rune(stopNote))) > maxStopNoteUTF16 {
			return "", "", "", "", nil, invalid("stop note exceeds 200 characters")
		}
		transport := strings.TrimSpace(raw.GetTransport())
		if !itinerary.ValidTransport(transport) {
			return "", "", "", "", nil, invalid("unsupported stop transport mode")
		}
		tripID := strings.TrimSpace(raw.GetTripId())
		if tripID != "" && !tripIDPattern.MatchString(tripID) {
			return "", "", "", "", nil, invalid("invalid stop trip_id")
		}
		stops = append(stops, schema.ItineraryStopRecord{
			ID:         stopID,
			RegionCode: code,
			Date:       date,
			Note:       stopNote,
			Transport:  itinerary.NormalizeTransport(transport),
			TripID:     tripID,
		})
	}
	return id, title, note, color, stops, nil
}

func saveRequestHash(id, title, note, color string, expectedVersion int64, stops []schema.ItineraryStopRecord) string {
	payload, _ := json.Marshal(struct {
		ID              string                       `json:"id"`
		Title           string                       `json:"title"`
		Note            string                       `json:"note"`
		Color           string                       `json:"color"`
		ExpectedVersion int64                        `json:"expected_version"`
		Stops           []schema.ItineraryStopRecord `json:"stops"`
	}{
		ID:              id,
		Title:           title,
		Note:            note,
		Color:           color,
		ExpectedVersion: expectedVersion,
		Stops:           stops,
	})
	sum := sha256.Sum256(append([]byte("itinerary-save:v1:"), payload...))
	return fmt.Sprintf("%x", sum)
}

func (s *ItineraryService) SaveItinerary(ctx context.Context, req *connect.Request[tripmapv1.SaveItineraryRequest]) (*connect.Response[tripmapv1.SaveItineraryResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	msg := req.Msg
	if msg.GetExpectedVersion() < 0 {
		return nil, invalid("expected_version must be non-negative")
	}
	mutationID := strings.TrimSpace(msg.GetMutationId())
	if mutationID != "" && !mutationIDPattern.MatchString(mutationID) {
		return nil, invalid("invalid mutation_id")
	}
	id, title, note, color, stops, err := s.validateSaveInput(msg)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	hash := saveRequestHash(id, title, note, color, msg.GetExpectedVersion(), stops)
	replayFromReceipt := func(receipt *ent.SyncReceipt) (*connect.Response[tripmapv1.SaveItineraryResponse], error) {
		if receipt.RequestHash != hash {
			return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("mutation_id was already used"))
		}
		row, rerr := st.DB.Itinerary.Query().
			Where(entitinerary.UserID(p.UserID), entitinerary.ItineraryID(id)).
			Only(ctx)
		if ent.IsNotFound(rerr) {
			return nil, connect.NewError(connect.CodeAborted, errors.New("replayed itinerary was subsequently deleted"))
		}
		if rerr != nil {
			return nil, internalError(rerr)
		}
		resolver := s.newLegResolver(ctx, st)
		return connect.NewResponse(&tripmapv1.SaveItineraryResponse{
			Itinerary: s.toItineraryInfo(row, resolver),
			Policy:    itinerary.EstimationPolicy(),
			Replayed:  true,
		}), nil
	}
	if mutationID != "" {
		if receipt, rerr := st.DB.SyncReceipt.Query().
			Where(syncreceipt.UserID(p.UserID), syncreceipt.MutationIDEQ(mutationID)).
			Only(ctx); rerr == nil {
			return replayFromReceipt(receipt)
		} else if !ent.IsNotFound(rerr) {
			return nil, internalError(rerr)
		}
	}

	if err = s.allow(ctx, st, "itinerary:write:"+p.UserID, itineraryWriteLimit, itineraryWriteWindow); err != nil {
		return nil, err
	}

	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	u, err := lockUser(ctx, tx, p.UserID)
	if err != nil {
		return nil, err
	}
	if mutationID != "" {
		if receipt, rerr := tx.SyncReceipt.Query().
			Where(syncreceipt.UserID(u.ID), syncreceipt.MutationIDEQ(mutationID)).
			Only(ctx); rerr == nil {
			_ = tx.Rollback()
			return replayFromReceipt(receipt)
		} else if !ent.IsNotFound(rerr) {
			return nil, internalError(rerr)
		}
	}

	existing, err := tx.Itinerary.Query().
		Where(entitinerary.UserID(u.ID), entitinerary.ItineraryID(id)).
		Only(ctx)
	var savedRow *ent.Itinerary
	switch {
	case ent.IsNotFound(err):
		if msg.GetExpectedVersion() > 0 {
			return nil, connect.NewError(connect.CodeAborted, errors.New("itinerary changed or was deleted since expected_version"))
		}
		savedRow, err = tx.Itinerary.Create().
			SetUserID(u.ID).
			SetItineraryID(id).
			SetTitle(title).
			SetNote(note).
			SetColor(color).
			SetStops(stops).
			SetVersion(1).
			Save(ctx)
		if err != nil {
			return nil, wrapWrite(err)
		}
	case err != nil:
		return nil, internalError(err)
	default:
		if msg.GetExpectedVersion() == 0 {
			return nil, connect.NewError(connect.CodeAborted, errors.New("itinerary already exists; provide expected_version to update"))
		}
		if msg.GetExpectedVersion() != existing.Version {
			return nil, connect.NewError(connect.CodeAborted, errors.New("itinerary changed since expected_version"))
		}
		savedRow, err = existing.Update().
			SetTitle(title).
			SetNote(note).
			SetColor(color).
			SetStops(stops).
			SetVersion(existing.Version + 1).
			Save(ctx)
		if err != nil {
			return nil, wrapWrite(err)
		}
	}

	if mutationID != "" {
		if err = tx.SyncReceipt.Create().
			SetUserID(u.ID).
			SetMutationID(mutationID).
			SetRequestHash(hash).
			SetRevision(savedRow.Version).
			SetChangedCount(len(stops)).
			Exec(ctx); err != nil {
			return nil, internalError(err)
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	resolver := s.newLegResolver(ctx, st)
	return connect.NewResponse(&tripmapv1.SaveItineraryResponse{
		Itinerary: s.toItineraryInfo(savedRow, resolver),
		Policy:    itinerary.EstimationPolicy(),
	}), nil
}

func (s *ItineraryService) DeleteItinerary(ctx context.Context, req *connect.Request[tripmapv1.DeleteItineraryRequest]) (*connect.Response[tripmapv1.DeleteItineraryResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	id := strings.TrimSpace(req.Msg.GetId())
	if !tripIDPattern.MatchString(id) {
		return nil, invalid("invalid itinerary id")
	}
	if req.Msg.GetExpectedVersion() < 0 {
		return nil, invalid("expected_version must be non-negative")
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "itinerary:write:"+p.UserID, itineraryWriteLimit, itineraryWriteWindow); err != nil {
		return nil, err
	}

	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	u, err := lockUser(ctx, tx, p.UserID)
	if err != nil {
		return nil, err
	}
	existing, err := tx.Itinerary.Query().
		Where(entitinerary.UserID(u.ID), entitinerary.ItineraryID(id)).
		Only(ctx)
	switch {
	case ent.IsNotFound(err):
		if req.Msg.GetExpectedVersion() > 0 {
			return nil, connect.NewError(connect.CodeAborted, errors.New("itinerary changed since expected_version"))
		}
		if err = tx.Commit(); err != nil {
			return nil, internalError(err)
		}
		return connect.NewResponse(&tripmapv1.DeleteItineraryResponse{Deleted: false}), nil
	case err != nil:
		return nil, internalError(err)
	default:
		if req.Msg.GetExpectedVersion() > 0 && req.Msg.GetExpectedVersion() != existing.Version {
			return nil, connect.NewError(connect.CodeAborted, errors.New("itinerary changed since expected_version"))
		}
		if err = tx.Itinerary.DeleteOneID(existing.ID).Exec(ctx); err != nil {
			return nil, internalError(err)
		}
		if err = tx.Commit(); err != nil {
			return nil, internalError(err)
		}
		return connect.NewResponse(&tripmapv1.DeleteItineraryResponse{Deleted: true}), nil
	}
}
