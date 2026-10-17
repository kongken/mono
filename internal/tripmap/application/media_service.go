package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/kongken/mono/internal/tripmap/blobstore"
	"github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/ent/cityvisit"
	"github.com/kongken/mono/internal/tripmap/ent/mediaobject"
	"github.com/kongken/mono/internal/tripmap/ent/user"
	"github.com/kongken/mono/internal/tripmap/regions"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type MediaService struct {
	src     StoreSource
	regions *regions.Index
}

func NewMediaService(src StoreSource, idx *regions.Index) *MediaService {
	return &MediaService{src: src, regions: idx}
}

func mediaInfo(m *ent.MediaObject) *tripmapv1.MediaInfo {
	return &tripmapv1.MediaInfo{Id: m.ID, RegionCode: m.RegionCode, Filename: m.Filename, ContentType: m.ContentType, SizeBytes: m.SizeBytes, Width: int32(m.Width), Height: int32(m.Height), Status: string(m.Status), CreatedAt: timestamppb.New(m.CreatedAt), TripId: m.TripID}
}

func mediaStore(ctx context.Context, src StoreSource) (*Store, Caller, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, p, err
	}
	st, err := storeFrom(src)
	if err != nil {
		return nil, p, err
	}
	if st.Blobs == nil {
		return nil, p, connect.NewError(connect.CodeUnavailable, errors.New("media storage unavailable"))
	}
	return st, p, nil
}

// Serialize media changes with footprint writes and account deletion. Recheck
// disabled after taking the lock, as authentication may have preceded deletion.
func mediaTx(ctx context.Context, st *Store, uid string) (*ent.Tx, error) {
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	if _, err := tx.User.Query().Where(user.ID(uid)).ForUpdate().Only(ctx); err != nil {
		_ = tx.Rollback()
		if !ent.IsNotFound(err) {
			return nil, internalError(err)
		}
		return nil, errUnauthenticated
	}
	return tx, nil
}

func mediaOwned(ctx context.Context, db *ent.Client, uid, id string) (*ent.MediaObject, error) {
	m, err := db.MediaObject.Query().Where(mediaobject.ID(id), mediaobject.UserID(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("media not found"))
	}
	if err != nil {
		return nil, internalError(err)
	}
	return m, nil
}

func (s *MediaService) PrepareUpload(ctx context.Context, req *connect.Request[tripmapv1.PrepareUploadRequest]) (*connect.Response[tripmapv1.PrepareUploadResponse], error) {
	r := req.Msg
	if _, _, ok := s.regions.City(r.RegionCode); !ok {
		return nil, invalid("unknown region code")
	}
	if r.TripId != "" && !tripIDPattern.MatchString(r.TripId) {
		return nil, invalid("invalid trip id")
	}
	if !mediaType(r.ContentType) || r.SizeBytes <= 0 || r.SizeBytes > mediaMaxBytes {
		return nil, invalid("JPEG, PNG, WebP or PDF up to 10 MB required")
	}
	if r.Filename == "" || len(r.Filename) > 180 || !utf8.ValidString(r.Filename) || strings.ContainsAny(r.Filename, "/\\\r\n\x00") {
		return nil, invalid("invalid filename")
	}
	if _, err := uuid.Parse(r.UploadId); err != nil {
		return nil, invalid("upload_id must be a UUID")
	}
	st, p, err := mediaStore(ctx, s.src)
	if err != nil {
		return nil, err
	}
	signer, ok := st.Blobs.(blobstore.UploadSigner)
	if !ok {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("upload signing unavailable"))
	}
	allowed, err := NewLimiter(st.Redis, st.prefix()).Allow(ctx, "media:"+p.UserID, 60, time.Hour)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("media rate limiter unavailable"))
	}
	if !allowed {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("media upload limit reached"))
	}
	payload, _ := proto.MarshalOptions{Deterministic: true}.Marshal(r)
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	tx, err := mediaTx(ctx, st, p.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	m, err := tx.MediaObject.Query().Where(mediaobject.UserID(p.UserID), mediaobject.UploadID(r.UploadId)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	if m != nil {
		if m.RequestHash != hash {
			return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("upload ID already used for another file"))
		}
		if m.Status == mediaobject.StatusDeleting {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("upload was deleted"))
		}
		if m.Status == mediaobject.StatusReady {
			return connect.NewResponse(&tripmapv1.PrepareUploadResponse{Media: mediaInfo(m)}), nil
		}
		if m.UploadExpiresAt == nil || !m.UploadExpiresAt.After(time.Now()) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("upload expired; start a new upload"))
		}
	} else {
		if err := requireCurrentRegionIDs(ctx, tx.CityVisit.Query().Where(cityvisit.UserID(p.UserID)), s.regions); err != nil {
			return nil, err
		}
		// Retired orphan-media IDs must be migrated before accepting new files
		// into their canonical destination's separate quota bucket.
		rows, err := tx.MediaObject.Query().Where(mediaobject.UserID(p.UserID), mediaobject.StatusNEQ(mediaobject.StatusDeleting)).Select(mediaobject.FieldRegionCode).All(ctx)
		if err != nil {
			return nil, internalError(err)
		}
		for _, row := range rows {
			code, ok := s.regions.ResolveCode(row.RegionCode)
			if !ok || code != row.RegionCode {
				return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("region upgrade required before writes: migrate old media regions first"))
			}
		}
		if r.TripId != "" {
			if err = AuthorizeTripMediaWrite(ctx, tx, p.UserID, r.RegionCode, r.TripId); err != nil {
				return nil, err
			}
		}
		count, err := tx.MediaObject.Query().Where(mediaobject.UserID(p.UserID), mediaobject.RegionCode(r.RegionCode), mediaobject.StatusNEQ(mediaobject.StatusDeleting)).Count(ctx)
		if err != nil {
			return nil, internalError(err)
		}
		if count >= mediaPerCity {
			return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("maximum 20 files per city (including pending uploads)"))
		}
		id := uuid.NewString()
		raw := "media/staging/" + uuid.NewString()
		expires := time.Now().UTC().Add(10 * time.Minute)
		m, err = tx.MediaObject.Create().SetID(id).SetUserID(p.UserID).SetRegionCode(r.RegionCode).SetTripID(r.TripId).SetFilename(r.Filename).SetUploadID(r.UploadId).SetRequestHash(hash).SetUploadKey(raw).SetObjectKey("media/files/" + id).SetThumbnailKey("media/thumbnails/" + id).SetContentType(r.ContentType).SetSizeBytes(r.SizeBytes).SetUploadExpiresAt(expires).Save(ctx)
		if err != nil {
			return nil, internalError(err)
		}
		// This queue survives account deletion and covers signed PUT replays.
		if err = tx.MediaCleanup.Create().SetObjectKey(raw).SetDeleteAfter(time.Now().UTC().Add(24 * time.Hour)).Exec(ctx); err != nil {
			return nil, internalError(err)
		}
	}
	url, headers, err := signer.SignUpload(ctx, m.UploadKey, m.SizeBytes, m.ContentType, time.Until(*m.UploadExpiresAt))
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("cannot sign upload"))
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.PrepareUploadResponse{Media: mediaInfo(m), UploadUrl: url, Headers: headers, ExpiresAt: timestamppb.New(*m.UploadExpiresAt)}), nil
}

func (s *MediaService) CompleteUpload(ctx context.Context, req *connect.Request[tripmapv1.CompleteUploadRequest]) (*connect.Response[tripmapv1.CompleteUploadResponse], error) {
	st, p, err := mediaStore(ctx, s.src)
	if err != nil {
		return nil, err
	}
	// Complete retries are cheap once ready. Reserve processing only for pending.
	select {
	case mediaProcessors <- struct{}{}:
		defer func() { <-mediaProcessors }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := mediaTx(ctx, st, p.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	m, err := mediaOwned(ctx, tx.Client(), p.UserID, req.Msg.Id)
	if err != nil {
		return nil, err
	}
	if m.Status == mediaobject.StatusReady {
		return connect.NewResponse(&tripmapv1.CompleteUploadResponse{Media: mediaInfo(m)}), nil
	}
	if m.Status != mediaobject.StatusPending || time.Since(m.CreatedAt) >= 24*time.Hour {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("upload expired or deleted"))
	}
	r, err := st.Blobs.Get(ctx, m.UploadKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("upload missing; upload the file before completing"))
	}
	data, err := boundedMedia(r)
	_ = r.Close()
	if err != nil {
		return nil, invalid("uploaded file unreadable or exceeds 10 MB")
	}
	if int64(len(data)) != m.SizeBytes {
		return nil, invalid("uploaded size differs from reservation")
	}
	full, thumb, w, h, err := processMedia(data, m.ContentType)
	if err != nil {
		return nil, invalid(err.Error())
	}
	mime := m.ContentType
	filename := m.Filename
	if mime != "application/pdf" {
		mime = "image/jpeg"
		filename = strings.TrimSuffix(filename, filepath.Ext(filename)) + ".jpg"
	}
	if err = st.Blobs.Put(ctx, m.ObjectKey, bytes.NewReader(full), int64(len(full)), mime); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("media processing failed; retry"))
	}
	if len(thumb) > 0 {
		if err = st.Blobs.Put(ctx, m.ThumbnailKey, bytes.NewReader(thumb), int64(len(thumb)), "image/jpeg"); err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("thumbnail processing failed; retry"))
		}
	}
	m, err = tx.MediaObject.UpdateOne(m).SetStatus(mediaobject.StatusReady).SetContentType(mime).SetFilename(filename).SetSizeBytes(int64(len(full))).SetWidth(w).SetHeight(h).Save(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.CompleteUploadResponse{Media: mediaInfo(m)}), nil
}

func (s *MediaService) ListMedia(ctx context.Context, req *connect.Request[tripmapv1.ListMediaRequest]) (*connect.Response[tripmapv1.ListMediaResponse], error) {
	st, p, err := mediaStore(ctx, s.src)
	if err != nil {
		return nil, err
	}
	q := st.DB.MediaObject.Query().Where(mediaobject.UserID(p.UserID), mediaobject.StatusNEQ(mediaobject.StatusDeleting))
	if req.Msg.RegionCode != "" {
		if _, _, ok := s.regions.City(req.Msg.RegionCode); !ok {
			return nil, invalid("unknown region code")
		}
		q = q.Where(mediaobject.RegionCode(req.Msg.RegionCode))
	}
	if req.Msg.TripId != "" {
		if !tripIDPattern.MatchString(req.Msg.TripId) {
			return nil, invalid("invalid trip id")
		}
		q = q.Where(mediaobject.TripID(req.Msg.TripId))
	}
	rows, err := q.Order(ent.Desc(mediaobject.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	res := &tripmapv1.ListMediaResponse{}
	for _, m := range rows {
		res.Media = append(res.Media, mediaInfo(m))
	}
	return connect.NewResponse(res), nil
}

func (s *MediaService) ReadMedia(ctx context.Context, req *connect.Request[tripmapv1.ReadMediaRequest]) (*connect.Response[tripmapv1.ReadMediaResponse], error) {
	st, p, err := mediaStore(ctx, s.src)
	if err != nil {
		return nil, err
	}
	m, err := mediaOwned(ctx, st.DB, p.UserID, req.Msg.Id)
	if err != nil {
		return nil, err
	}
	if m.Status != mediaobject.StatusReady {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("media not ready"))
	}
	key, mime := m.ObjectKey, m.ContentType
	if req.Msg.Thumbnail {
		if m.Width == 0 {
			return nil, invalid("attachment has no thumbnail")
		}
		key, mime = m.ThumbnailKey, "image/jpeg"
	}
	r, err := st.Blobs.Get(ctx, key)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("media unavailable"))
	}
	defer r.Close()
	data, err := boundedMedia(r)
	if err != nil {
		return nil, internalError(err)
	}
	res := connect.NewResponse(&tripmapv1.ReadMediaResponse{Data: data, ContentType: mime, Filename: m.Filename})
	res.Header().Set("Cache-Control", "no-store")
	return res, nil
}

func (s *MediaService) DeleteMedia(ctx context.Context, req *connect.Request[tripmapv1.DeleteMediaRequest]) (*connect.Response[tripmapv1.DeleteMediaResponse], error) {
	st, p, err := mediaStore(ctx, s.src)
	if err != nil {
		return nil, err
	}
	tx, err := mediaTx(ctx, st, p.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	m, err := tx.MediaObject.Query().Where(mediaobject.UserID(p.UserID), mediaobject.ID(req.Msg.Id)).Only(ctx)
	if ent.IsNotFound(err) {
		return connect.NewResponse(&tripmapv1.DeleteMediaResponse{}), nil
	}
	if err != nil {
		return nil, internalError(err)
	}
	if err = tx.MediaObject.UpdateOne(m).SetStatus(mediaobject.StatusDeleting).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	// Access is already revoked, even if S3 is unavailable. Retain metadata for
	// the maintenance job and explicit retry until every key is removed.
	if err = DeleteMediaKeys(ctx, st, m); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("access revoked; object cleanup pending, retry deletion"))
	}
	return connect.NewResponse(&tripmapv1.DeleteMediaResponse{}), nil
}

// DeleteMediaKeys removes every stored key for a media row.
func DeleteMediaKeys(ctx context.Context, st *Store, m *ent.MediaObject) error {
	for _, key := range []string{m.ObjectKey, m.ThumbnailKey, m.UploadKey} {
		if key != "" {
			if err := st.Blobs.Delete(ctx, key); err != nil {
				return err
			}
		}
	}
	return nil
}

