package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/tripmap/auth"
	"github.com/kongken/mono/internal/tripmap/config"
	"github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/ent/cityvisit"
	"github.com/kongken/mono/internal/tripmap/ent/mediaobject"
	"github.com/kongken/mono/internal/tripmap/ent/provincemark"
	entshare "github.com/kongken/mono/internal/tripmap/ent/share"
	"github.com/kongken/mono/internal/tripmap/ent/syncreceipt"
	"github.com/kongken/mono/internal/tripmap/ent/user"
	"github.com/kongken/mono/internal/tripmap/regions"
	"github.com/kongken/mono/internal/tripmap/share"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	shareWriteLimit    = 20
	shareWriteWindow   = time.Minute
	sharePreviewLimit  = 30
	sharePreviewWindow = time.Minute
)

// ShareService implements tripmap.v1.ShareService. The account always comes from
// the session. Writes run under the user's row lock, the same one footprint,
// media and account-deletion writes take, so a publish sees one consistent
// state and can never race with a deletion.
type ShareService struct {
	cfg     *config.AppConfig
	src     StoreSource
	regions *regions.Index
}

func NewShareService(cfg *config.AppConfig, src StoreSource, idx *regions.Index) *ShareService {
	return &ShareService{cfg: cfg, src: src, regions: idx}
}

var errShareDisabled = connect.NewError(connect.CodeFailedPrecondition, errors.New("sharing is not enabled on this server"))

func shareConflict(msg string, revision int64) error {
	conflict := connect.NewError(connect.CodeAborted, errors.New(msg))
	if d, err := connect.NewErrorDetail(&tripmapv1.RevisionConflict{CurrentRevision: revision}); err == nil {
		conflict.AddDetail(d)
	}
	return conflict
}

func parseVisibility(v string) (entshare.Visibility, error) {
	switch vis := entshare.Visibility(v); vis {
	case entshare.VisibilityPrivate, entshare.VisibilityLink, entshare.VisibilityPublic:
		return vis, nil
	}
	return "", invalid(`visibility must be "private", "link" or "public"`)
}

func settingsFrom(m *tripmapv1.ShareSettings) (entshare.Visibility, share.Settings, error) {
	vis, err := parseVisibility(m.GetVisibility())
	if err != nil {
		return "", share.Settings{}, err
	}
	set := share.Settings{Cities: m.GetShowCities(), Dates: m.GetShowDates(), Notes: m.GetShowNotes(), Photos: m.GetShowPhotos(), PhotoIDs: slices.Clone(m.GetPhotoIds())}
	if err = set.Validate(); err != nil {
		return "", share.Settings{}, invalid(err.Error())
	}
	return vis, set, nil
}

func rowSettings(row *ent.Share) share.Settings {
	return share.Settings{Cities: row.ShowCities, Dates: row.ShowDates, Notes: row.ShowNotes, Photos: row.ShowPhotos, PhotoIDs: slices.Clone(row.PhotoIds)}
}

func (s *ShareService) shareURL(row *ent.Share) string {
	if !s.cfg.Share.Enabled() || row == nil || row.Token == nil || row.Visibility == entshare.VisibilityPrivate || len(row.Payload) == 0 {
		return ""
	}
	return s.cfg.Share.BaseURL() + "/s/" + *row.Token
}

func (s *ShareService) info(row *ent.Share, revision int64) *tripmapv1.ShareInfo {
	out := &tripmapv1.ShareInfo{Settings: &tripmapv1.ShareSettings{Visibility: string(entshare.VisibilityPrivate)}, CurrentRevision: revision}
	if row == nil {
		return out
	}
	out.Settings = &tripmapv1.ShareSettings{
		Visibility: string(row.Visibility), ShowCities: row.ShowCities, ShowDates: row.ShowDates, ShowNotes: row.ShowNotes, ShowPhotos: row.ShowPhotos,
		PhotoIds: slices.Clone(row.PhotoIds),
	}
	out.Version = row.Version
	if out.Url = s.shareURL(row); out.Url != "" {
		out.Published = true
		out.PublishedRevision = row.PublishedRevision
		if row.PublishedAt != nil {
			out.PublishedAt = timestamppb.New(*row.PublishedAt)
		}
	}
	return out
}

func (s *ShareService) allow(ctx context.Context, st *Store, key string, limit int64, window time.Duration) error {
	ok, err := auth.NewLimiter(st.Redis, st.prefix()).Allow(ctx, key, limit, window)
	if err != nil {
		slog.Error("rate limiter unavailable", "error", err)
		return connect.NewError(connect.CodeUnavailable, errors.New("service temporarily unavailable"))
	}
	if !ok {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("too many share requests, slow down"))
	}
	return nil
}

func shareSourceError(err error) error {
	if rpc, ok := errors.AsType[*connect.Error](err); ok {
		return rpc
	}
	return internalError(err)
}

// loadShareSource reads the footprints the page would be built from. It runs in
// the caller's transaction, so the records match the revision read with them.
func loadShareSource(ctx context.Context, tx *ent.Tx, u *ent.User, photoIDs []string) (share.Source, error) {
	var src share.Source
	visits, err := tx.CityVisit.Query().Where(cityvisit.UserID(u.ID)).Order(ent.Asc(cityvisit.FieldRegionCode)).All(ctx)
	if err != nil {
		return src, err
	}
	trips := map[string][]share.SourceTrip{}
	if u.SchemaVersion >= schemaTrips {
		rows, err := loadTrips(ctx, tx, u.ID)
		if err != nil {
			return src, err
		}
		for _, t := range rows {
			trips[t.RegionCode] = append(trips[t.RegionCode], share.SourceTrip{ID: t.TripID, Date: t.TravelDate, Note: t.Note})
		}
	} else {
		// Not upgraded yet: each city reads as one legacy trip.
		for _, v := range visits {
			trips[v.RegionCode] = []share.SourceTrip{{ID: legacyTripID(v.RegionCode), Date: v.TravelDate, Note: v.Note}}
		}
	}
	index, err := regions.Default()
	if err != nil {
		return src, err
	}
	seen := map[string]bool{}
	for _, v := range visits {
		ref, err := index.ResolveCity(v.RegionCode, v.RegionName, v.ProvinceCode)
		if err != nil || seen[ref.Code] {
			return src, connect.NewError(connect.CodeFailedPrecondition, errors.New("region upgrade blocked: unmapped or colliding footprints"))
		}
		seen[ref.Code] = true
		src.Cities = append(src.Cities, share.SourceCity{Code: ref.Code, Name: ref.Name, Province: ref.Province, Color: v.Color, Trips: trips[v.RegionCode]})
	}
	marks, err := tx.ProvinceMark.Query().Where(provincemark.UserID(u.ID)).All(ctx)
	if err != nil {
		return src, err
	}
	for _, m := range marks {
		if !index.HasProvince(m.ProvinceCode) {
			return src, connect.NewError(connect.CodeFailedPrecondition, errors.New("region upgrade blocked: unmapped province mark"))
		}
		src.Marks = append(src.Marks, m.ProvinceCode)
	}
	if len(photoIDs) > 0 {
		media, err := tx.MediaObject.Query().Where(mediaobject.UserID(u.ID), mediaobject.IDIn(photoIDs...), mediaobject.StatusEQ(mediaobject.StatusReady),
			mediaobject.ContentType("image/jpeg"), mediaobject.WidthGT(0)).All(ctx)
		if err != nil {
			return src, err
		}
		src.Photos = map[string]share.SourcePhoto{}
		for _, m := range media {
			src.Photos[m.ID] = share.SourcePhoto{Region: regions.CanonicalCode(m.RegionCode), TripID: m.TripID, Width: m.Width, Height: m.Height}
		}
	}
	return src, nil
}

func (s *ShareService) GetShare(ctx context.Context, _ *connect.Request[tripmapv1.GetShareRequest]) (*connect.Response[tripmapv1.GetShareResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	u, err := st.DB.User.Query().Where(user.ID(p.UserID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, errUnauthenticated
		}
		return nil, internalError(err)
	}
	if u.Disabled {
		return nil, errUnauthenticated
	}
	row, err := st.DB.Share.Query().Where(entshare.UserID(p.UserID)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	if ent.IsNotFound(err) {
		row = nil
	}
	return connect.NewResponse(&tripmapv1.GetShareResponse{Share: s.info(row, u.Revision), Enabled: s.cfg.Share.Enabled()}), nil
}

func (s *ShareService) PreviewShare(ctx context.Context, req *connect.Request[tripmapv1.PreviewShareRequest]) (*connect.Response[tripmapv1.PreviewShareResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if !s.cfg.Share.Enabled() {
		return nil, errShareDisabled
	}
	// Only the fields matter for a preview; visibility is a publishing choice.
	candidate := req.Msg.GetSettings()
	if candidate == nil {
		return nil, invalid("settings are required")
	}
	set := share.Settings{Cities: candidate.GetShowCities(), Dates: candidate.GetShowDates(), Notes: candidate.GetShowNotes(), Photos: candidate.GetShowPhotos(), PhotoIDs: slices.Clone(candidate.GetPhotoIds())}
	if err = set.Validate(); err != nil {
		return nil, invalid(err.Error())
	}
	if err = s.allow(ctx, st, "share-preview:"+p.UserID, sharePreviewLimit, sharePreviewWindow); err != nil {
		return nil, err
	}
	// One snapshot, so the revision belongs to the records.
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
	if u.Disabled {
		return nil, errUnauthenticated
	}
	src, err := loadShareSource(ctx, tx, u, set.PhotoIDs)
	if err != nil {
		return nil, shareSourceError(err)
	}
	payload, err := share.Build(s.regions, src, set, time.Now().UTC(), share.NewHandle)
	if err != nil {
		return nil, shareBuildError(err)
	}
	if _, err = share.Encode(payload); err != nil {
		return nil, shareBuildError(err)
	}
	body, err := payload.Content.PublicJSON()
	if err != nil {
		return nil, internalError(err)
	}
	out := &tripmapv1.PreviewShareResponse{ContentJson: string(body), FootprintRevision: u.Revision}
	for handle, id := range payload.Photos {
		out.Photos = append(out.Photos, &tripmapv1.PreviewPhoto{Handle: handle, MediaId: id})
	}
	slices.SortFunc(out.Photos, func(a, b *tripmapv1.PreviewPhoto) int { return compareStrings(a.Handle, b.Handle) })
	return connect.NewResponse(out), nil
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func shareBuildError(err error) error {
	switch {
	case errors.Is(err, share.ErrTooLarge):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("share content is too large; hide notes or publish fewer cities"))
	case errors.Is(err, share.ErrPhotoUnavailable):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("a chosen photo is no longer available; choose the photos again"))
	}
	return internalError(err)
}

// requestHash binds a mutation ID to the exact request. Every field that changes
// what the call does is part of it, so a retry cannot be confused with a request
// made against other content, settings or an older preview.
func saveHash(m *tripmapv1.SaveShareRequest) string {
	raw, _ := json.Marshal(struct {
		Op       string
		Expected int64
		Settings []any
		Photos   []string
		Refresh  bool
		Preview  int64
	}{"save", m.ExpectedVersion, []any{m.GetSettings().GetVisibility(), m.GetSettings().GetShowCities(), m.GetSettings().GetShowDates(), m.GetSettings().GetShowNotes(), m.GetSettings().GetShowPhotos()},
		m.GetSettings().GetPhotoIds(), m.RefreshContent, m.PreviewRevision})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func (s *ShareService) SaveShare(ctx context.Context, req *connect.Request[tripmapv1.SaveShareRequest]) (*connect.Response[tripmapv1.SaveShareResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	m := req.Msg
	if !mutationIDPattern.MatchString(m.MutationId) || m.ExpectedVersion < 0 || m.PreviewRevision < 0 || m.GetSettings() == nil {
		return nil, invalid("mutation_id, a non-negative expected_version and settings are required")
	}
	visibility, set, err := settingsFrom(m.Settings)
	if err != nil {
		return nil, err
	}
	hash := saveHash(m)
	replay := func(receipt *ent.SyncReceipt, current *ent.Share, revision int64) (*connect.Response[tripmapv1.SaveShareResponse], error) {
		if receipt.RequestHash != hash {
			return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("mutation_id was already used for another request"))
		}
		return connect.NewResponse(&tripmapv1.SaveShareResponse{Share: s.info(current, revision), Replayed: true}), nil
	}
	// A retry of an applied request is answered from its receipt, before the
	// disabled check and rate limit; the transaction repeats it under the lock.
	if receipt, err := st.DB.SyncReceipt.Query().Where(syncreceipt.UserID(p.UserID), syncreceipt.MutationIDEQ(m.MutationId)).Only(ctx); err == nil {
		current, revision, rerr := s.current(ctx, st.DB, p.UserID)
		if rerr != nil {
			return nil, rerr
		}
		return replay(receipt, current, revision)
	} else if !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	if !s.cfg.Share.Enabled() {
		return nil, errShareDisabled
	}
	if m.RefreshContent && visibility == entshare.VisibilityPrivate {
		return nil, invalid("a private share has no published content to refresh")
	}
	if err = s.allow(ctx, st, "share-write:"+p.UserID, shareWriteLimit, shareWriteWindow); err != nil {
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
	row, err := tx.Share.Query().Where(entshare.UserID(p.UserID)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	if ent.IsNotFound(err) {
		row = nil
	}
	if receipt, err := tx.SyncReceipt.Query().Where(syncreceipt.UserID(p.UserID), syncreceipt.MutationIDEQ(m.MutationId)).Only(ctx); err == nil {
		return replay(receipt, row, u.Revision)
	} else if !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	version := int64(0)
	if row != nil {
		version = row.Version
	}
	if m.ExpectedVersion != version {
		return nil, shareConflict("share changed since it was read; reload and review it", u.Revision)
	}

	live := row != nil && row.Token != nil && len(row.Payload) > 0
	var (
		token       *string
		payloadData []byte
		publishedAt = time.Now().UTC()
		published   int64
		contentSame bool
	)
	switch {
	case visibility == entshare.VisibilityPrivate:
		// Nothing is public: the settings are kept as a draft only.
	case live && !m.RefreshContent && !rowSettings(row).Adds(set):
		// Hiding content only removes it from the published snapshot; no footprint is read.
		old, derr := share.Decode(row.Payload)
		if derr != nil {
			slog.Error("share payload decode", "error", derr)
			return nil, internalError(derr)
		}
		narrowed, nerr := share.Narrow(old, set)
		if nerr != nil {
			return nil, invalid(nerr.Error())
		}
		if payloadData, err = share.Encode(narrowed); err != nil {
			return nil, shareBuildError(err)
		}
		contentSame = share.SameContent(old, narrowed)
		token, published = row.Token, row.PublishedRevision
		if row.PublishedAt != nil {
			publishedAt = *row.PublishedAt
		}
	default:
		// Publishing content that was not public before. It reads the current
		// footprints, so the owner must have previewed this very revision.
		if !m.RefreshContent {
			return nil, invalid("publishing new content requires refresh_content after a preview")
		}
		if m.PreviewRevision != u.Revision {
			return nil, shareConflict("footprints changed since the preview; review the preview again", u.Revision)
		}
		src, serr := loadShareSource(ctx, tx, u, set.PhotoIDs)
		if serr != nil {
			return nil, shareSourceError(serr)
		}
		built, berr := share.Build(s.regions, src, set, publishedAt, share.NewHandle)
		if berr != nil {
			if errors.Is(berr, share.ErrPhotoUnavailable) {
				return nil, shareConflict("a chosen photo is no longer available; review the preview again", u.Revision)
			}
			return nil, shareBuildError(berr)
		}
		if live {
			// Refreshing a live page keeps its link.
			token = row.Token
			if old, derr := share.Decode(row.Payload); derr == nil {
				contentSame = share.SameContent(old, built)
			}
		}
		if payloadData, err = share.Encode(built); err != nil {
			return nil, shareBuildError(err)
		}
		if token == nil {
			fresh, terr := share.NewToken()
			if terr != nil {
				return nil, internalError(terr)
			}
			token = &fresh
		}
		published = u.Revision
	}

	newVersion := version
	// Settings and content identical to what is stored change nothing: the
	// version stays and the snapshot is not rewritten.
	unchanged := row != nil && row.Visibility == visibility && sameFlags(rowSettings(row), set) && slices.Equal(row.PhotoIds, set.PhotoIDs) &&
		(visibility == entshare.VisibilityPrivate && row.Token == nil && len(row.Payload) == 0 || visibility != entshare.VisibilityPrivate && live && contentSame)
	if !unchanged {
		newVersion = version + 1
		if row == nil {
			create := tx.Share.Create().SetUserID(p.UserID).SetVisibility(visibility).SetShowCities(set.Cities).SetShowDates(set.Dates).
				SetShowNotes(set.Notes).SetShowPhotos(set.Photos).SetPhotoIds(set.PhotoIDs).SetVersion(newVersion)
			if token != nil {
				create.SetToken(*token).SetPayload(payloadData).SetPublishedRevision(published).SetPublishedAt(publishedAt)
			}
			row, err = create.Save(ctx)
		} else {
			update := tx.Share.UpdateOne(row).SetVisibility(visibility).SetShowCities(set.Cities).SetShowDates(set.Dates).
				SetShowNotes(set.Notes).SetShowPhotos(set.Photos).SetPhotoIds(set.PhotoIDs).SetVersion(newVersion)
			if token != nil {
				update.SetToken(*token).SetPayload(payloadData).SetPublishedRevision(published).SetPublishedAt(publishedAt)
			} else {
				update.ClearToken().ClearPayload().SetPublishedRevision(0).ClearPublishedAt()
			}
			row, err = update.Save(ctx)
		}
		if err != nil {
			return nil, internalError(err)
		}
	} else if token != nil && row.PublishedRevision != published {
		row, err = tx.Share.UpdateOne(row).SetPublishedRevision(published).Save(ctx)
		if err != nil {
			return nil, internalError(err)
		}
	}
	if err = tx.SyncReceipt.Create().SetUserID(p.UserID).SetMutationID(m.MutationId).SetRequestHash(hash).SetRevision(newVersion).SetChangedCount(0).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.SaveShareResponse{Share: s.info(row, u.Revision)}), nil
}

func sameFlags(a, b share.Settings) bool {
	return a.Cities == b.Cities && a.Dates == b.Dates && a.Notes == b.Notes && a.Photos == b.Photos
}

// current reads the share row and footprint revision for a replayed answer.
func (s *ShareService) current(ctx context.Context, db *ent.Client, uid string) (*ent.Share, int64, error) {
	u, err := db.User.Query().Where(user.ID(uid)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, 0, errUnauthenticated
		}
		return nil, 0, internalError(err)
	}
	row, err := db.Share.Query().Where(entshare.UserID(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, u.Revision, nil
	}
	if err != nil {
		return nil, 0, internalError(err)
	}
	return row, u.Revision, nil
}

func (s *ShareService) RevokeShare(ctx context.Context, req *connect.Request[tripmapv1.RevokeShareRequest]) (*connect.Response[tripmapv1.RevokeShareResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if !mutationIDPattern.MatchString(req.Msg.MutationId) {
		return nil, invalid("mutation_id is required")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("share-revoke")))
	replay := func(receipt *ent.SyncReceipt, current *ent.Share, revision int64) (*connect.Response[tripmapv1.RevokeShareResponse], error) {
		if receipt.RequestHash != hash {
			return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("mutation_id was already used for another request"))
		}
		return connect.NewResponse(&tripmapv1.RevokeShareResponse{Share: s.info(current, revision), Replayed: true}), nil
	}
	if receipt, err := st.DB.SyncReceipt.Query().Where(syncreceipt.UserID(p.UserID), syncreceipt.MutationIDEQ(req.Msg.MutationId)).Only(ctx); err == nil {
		current, revision, rerr := s.current(ctx, st.DB, p.UserID)
		if rerr != nil {
			return nil, rerr
		}
		return replay(receipt, current, revision)
	} else if !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	if err = s.allow(ctx, st, "share-write:"+p.UserID, shareWriteLimit, shareWriteWindow); err != nil {
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
	row, err := tx.Share.Query().Where(entshare.UserID(p.UserID)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	if ent.IsNotFound(err) {
		row = nil
	}
	if receipt, err := tx.SyncReceipt.Query().Where(syncreceipt.UserID(p.UserID), syncreceipt.MutationIDEQ(req.Msg.MutationId)).Only(ctx); err == nil {
		return replay(receipt, row, u.Revision)
	} else if !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	version := int64(0)
	if row != nil {
		version = row.Version
		if row.Visibility != entshare.VisibilityPrivate || row.Token != nil || len(row.Payload) > 0 {
			version++
			// The flags and the chosen photo IDs stay as a private draft; nothing
			// of the published content or the token survives.
			row, err = tx.Share.UpdateOne(row).SetVisibility(entshare.VisibilityPrivate).ClearToken().ClearPayload().ClearPublishedAt().
				SetPublishedRevision(0).SetVersion(version).Save(ctx)
			if err != nil {
				return nil, internalError(err)
			}
		}
	}
	if err = tx.SyncReceipt.Create().SetUserID(p.UserID).SetMutationID(req.Msg.MutationId).SetRequestHash(hash).SetRevision(version).SetChangedCount(0).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.RevokeShareResponse{Share: s.info(row, u.Revision)}), nil
}
