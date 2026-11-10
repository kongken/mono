package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/timeline/accountcleanup"
	"github.com/kongken/mono/internal/timeline/ent"
	"github.com/kongken/mono/internal/timeline/ent/entry"
	"github.com/kongken/mono/internal/timeline/ent/mediaobject"
	"github.com/kongken/mono/internal/timeline/ent/timelinemutation"
	"github.com/kongken/mono/internal/tripmap/blobstore"
)

var ErrNotFound = errors.New("media not found")
var ErrInvalid = errors.New("invalid media request")

type Store struct {
	DB    *ent.Client
	Blobs blobstore.Store
}
type Upload struct {
	EntryRef, Filename, ContentType string
	SizeBytes                       int64
	MutationID                      string
}
type Media struct {
	Ref, EntryRef, Filename, ContentType, Status string
	SizeBytes                                    int64
	CreatedAt                                    time.Time
	UploadURL                                    string
	UploadHeaders                                map[string]string
	UploadExpiresAt                              time.Time
}

func validate(u Upload) error {
	if !strings.HasPrefix(u.EntryRef, "ent_") || len(u.EntryRef) != 30 || u.Filename == "" || u.SizeBytes <= 0 || u.SizeBytes > 10*1024*1024 || u.MutationID == "" {
		return ErrInvalid
	}
	if u.ContentType != "image/jpeg" && u.ContentType != "image/png" && u.ContentType != "image/webp" && u.ContentType != "application/pdf" {
		return ErrInvalid
	}
	return nil
}
func Prepare(ctx context.Context, st *Store, userID string, u Upload) (*Media, error) {
	if st == nil || st.DB == nil || st.Blobs == nil || userID == "" || validate(u) != nil {
		return nil, ErrInvalid
	}
	signer, ok := st.Blobs.(blobstore.UploadSigner)
	if !ok {
		return nil, errors.New("upload signing unavailable")
	}
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	e, err := tx.Entry.Query().Where(entry.UserID(userID), entry.PublicRefEQ(u.EntryRef)).Only(ctx)
	if err != nil {
		return nil, ErrNotFound
	}
	if existing, queryErr := tx.MediaObject.Query().Where(mediaobject.UserID(userID), mediaobject.EntryID(e.ID), mediaobject.MutationIDEQ(u.MutationID)).Only(ctx); queryErr == nil {
		url, headers, signErr := signer.SignUpload(ctx, existing.UploadKey, existing.SizeBytes, existing.ContentType, 10*time.Minute)
		if signErr != nil {
			return nil, signErr
		}
		expires := time.Now().UTC().Add(10 * time.Minute)
		if _, updateErr := existing.Update().SetUploadExpiresAt(expires).Save(ctx); updateErr != nil {
			return nil, updateErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, commitErr
		}
		return &Media{Ref: existing.PublicRef, EntryRef: existing.EntryPublicRef, Filename: existing.Filename, ContentType: existing.ContentType, SizeBytes: existing.SizeBytes, Status: existing.Status, CreatedAt: existing.CreatedAt, UploadURL: url, UploadHeaders: headers, UploadExpiresAt: expires}, nil
	}
	if n, _ := tx.MediaObject.Query().Where(mediaobject.UserID(userID), mediaobject.EntryID(e.ID), mediaobject.StatusNEQ("deleting")).Count(ctx); n >= 20 {
		return nil, ErrInvalid
	}
	id := uuid.NewString()
	key := "timeline/media/" + id
	exp := time.Now().UTC().Add(10 * time.Minute)
	m, err := tx.MediaObject.Create().SetUserID(userID).SetEntryID(e.ID).SetEntryPublicRef(e.PublicRef).SetFilename(u.Filename).SetContentType(u.ContentType).SetSizeBytes(u.SizeBytes).SetUploadKey(key).SetObjectKey(key).SetStatus("pending").SetMutationID(u.MutationID).SetUploadExpiresAt(exp).Save(ctx)
	if err != nil {
		return nil, err
	}
	url, headers, err := signer.SignUpload(ctx, key, u.SizeBytes, u.ContentType, 10*time.Minute)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &Media{Ref: m.PublicRef, EntryRef: m.EntryPublicRef, Filename: m.Filename, ContentType: m.ContentType, SizeBytes: m.SizeBytes, Status: m.Status, CreatedAt: m.CreatedAt, UploadURL: url, UploadHeaders: headers, UploadExpiresAt: exp}, nil
}
func Complete(ctx context.Context, st *Store, userID, mediaRef, mutationID string) (*Media, error) {
	if st == nil || st.DB == nil || st.Blobs == nil || userID == "" || mediaRef == "" || mutationID == "" {
		return nil, ErrInvalid
	}
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	m, err := tx.MediaObject.Query().Where(mediaobject.UserID(userID), mediaobject.PublicRefEQ(mediaRef), mediaobject.MutationIDEQ(mutationID)).Only(ctx)
	if err != nil {
		return nil, ErrNotFound
	}
	if m.Status == "ready" {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return &Media{Ref: m.PublicRef, EntryRef: m.EntryPublicRef, Filename: m.Filename, ContentType: m.ContentType, SizeBytes: m.SizeBytes, Status: m.Status, CreatedAt: m.CreatedAt}, nil
	}
	if m.Status != "pending" || m.UploadExpiresAt == nil || !m.UploadExpiresAt.After(time.Now().UTC()) {
		return nil, ErrInvalid
	}
	r, err := st.Blobs.Get(ctx, m.UploadKey)
	if err != nil {
		return nil, ErrNotFound
	}
	data, err := io.ReadAll(io.LimitReader(r, m.SizeBytes+1))
	r.Close()
	if err != nil || int64(len(data)) != m.SizeBytes || http.DetectContentType(data) != m.ContentType {
		return nil, ErrInvalid
	}
	m, err = m.Update().SetStatus("ready").Save(ctx)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &Media{Ref: m.PublicRef, EntryRef: m.EntryPublicRef, Filename: m.Filename, ContentType: m.ContentType, SizeBytes: m.SizeBytes, Status: m.Status, CreatedAt: m.CreatedAt}, nil
}
func validOpaqueRef(value, prefix string) bool {
	if len(value) != len(prefix)+26 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, c := range value[len(prefix):] {
		if !strings.ContainsRune("0123456789abcdefghjkmnpqrstvwxyz", c) {
			return false
		}
	}
	return true
}

func validDeleteReceipt(old *ent.TimelineMutation, hash, mediaRef string) bool {
	if old == nil || old.RequestHash != hash || old.TargetKind != "media" || old.Operation != "media_delete" || old.MediaRef == nil || *old.MediaRef != mediaRef || old.EntryRef == nil || old.ResultState != "deleting" || old.SnapshotVersion != 1 {
		return false
	}
	entryRef := *old.EntryRef
	snapshotMedia, mediaOK := old.ResultSnapshot["media_ref"].(string)
	snapshotEntry, entryOK := old.ResultSnapshot["entry_ref"].(string)
	return mediaOK && entryOK && validOpaqueRef(mediaRef, "med_") && validOpaqueRef(entryRef, "ent_") && snapshotMedia == mediaRef && snapshotEntry == entryRef
}

func Delete(ctx context.Context, st *Store, userID, mediaRef, mutationID string, expectedRevision int) error {
	if st == nil || st.DB == nil || userID == "" || mediaRef == "" || mutationID == "" || expectedRevision <= 0 {
		return ErrInvalid
	}
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	h := sha256.Sum256([]byte(fmt.Sprintf("delete:%s:%d", mediaRef, expectedRevision)))
	hash := hex.EncodeToString(h[:])
	if old, e := tx.TimelineMutation.Query().Where(timelinemutation.UserID(userID), timelinemutation.MutationIDEQ(mutationID)).Only(ctx); e == nil {
		if !validDeleteReceipt(old, hash, mediaRef) {
			return ErrInvalid
		}
		return nil
	}
	m, err := tx.MediaObject.Query().Where(mediaobject.UserID(userID), mediaobject.PublicRefEQ(mediaRef)).Only(ctx)
	if err != nil {
		return ErrNotFound
	}
	if expectedRevision != 1 {
		return ErrInvalid
	}
	_, err = m.Update().SetStatus("deleting").Save(ctx)
	if err != nil {
		return err
	}
	for _, key := range []string{m.ObjectKey, m.UploadKey, m.ThumbnailKey} {
		if key != "" {
			if err = tx.CleanupObject.Create().SetObjectKey(key).OnConflict().Ignore().Exec(ctx); err != nil {
				return err
			}
		}
	}
	_, err = tx.TimelineMutation.Create().SetUserID(userID).SetMutationID(mutationID).SetTargetKind("media").SetOperation("media_delete").SetEntryRef(m.EntryPublicRef).SetMediaRef(m.PublicRef).SetRequestHash(hash).SetResultRevision(1).SetResultState("deleting").SetResultSnapshot(map[string]any{"media_ref": m.PublicRef, "entry_ref": m.EntryPublicRef, "object_key": m.ObjectKey, "upload_key": m.UploadKey, "thumbnail_key": m.ThumbnailKey, "filename": m.Filename, "content_type": m.ContentType, "size_bytes": m.SizeBytes}).Save(ctx)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func Replay(ctx context.Context, db *ent.Client, userID, mutationID, mediaRef string) (map[string]any, error) {
	return accountcleanup.ReplayMediaTombstone(ctx, db, userID, mutationID, mediaRef)
}
func (m Media) Valid() bool { return strings.HasPrefix(m.Ref, "med_") }
