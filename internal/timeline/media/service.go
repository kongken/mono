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
	"github.com/kongken/mono/internal/timeline/ent/receipt"
	"github.com/kongken/mono/internal/timeline/refs"
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
	Revision                                     int
	CreatedAt                                    time.Time
	UploadURL                                    string
	Headers                                      map[string]string
	ExpiresAt                                    time.Time
}

func validate(u Upload) error {
	if !refs.Valid(u.EntryRef, "ent_") || strings.TrimSpace(u.Filename) == "" || u.SizeBytes <= 0 || u.SizeBytes > 10*1024*1024 || u.MutationID == "" {
		return ErrInvalid
	}
	if u.ContentType != "image/jpeg" && u.ContentType != "image/png" && u.ContentType != "image/webp" && u.ContentType != "application/pdf" {
		return ErrInvalid
	}
	return nil
}
func toMedia(m *ent.MediaObject) *Media {
	return &Media{Ref: m.PublicRef, EntryRef: m.EntryPublicRef, Filename: m.Filename, ContentType: m.ContentType, SizeBytes: m.SizeBytes, Status: m.State.String(), Revision: m.Revision, CreatedAt: m.CreatedAt}
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
	if existing, qerr := tx.MediaObject.Query().Where(mediaobject.UserID(userID), mediaobject.MutationIDEQ(u.MutationID)).Only(ctx); qerr == nil {
		if existing.EntryID != e.ID || existing.Filename != strings.TrimSpace(u.Filename) || existing.ContentType != u.ContentType || existing.SizeBytes != u.SizeBytes {
			return nil, ErrInvalid
		}
		if existing.State != mediaobject.StatePending {
			return toMedia(existing), nil
		}
		exp := time.Now().UTC().Add(10 * time.Minute)
		url, headers, signErr := signer.SignUpload(ctx, existing.UploadKey, existing.SizeBytes, existing.ContentType, 10*time.Minute)
		if signErr != nil {
			return nil, signErr
		}
		existing, err = existing.Update().SetUploadExpiresAt(exp).Save(ctx)
		if err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		out := toMedia(existing)
		out.UploadURL, out.Headers, out.ExpiresAt = url, headers, exp
		return out, nil
	} else if !ent.IsNotFound(qerr) {
		return nil, qerr
	}
	if n, _ := tx.MediaObject.Query().Where(mediaobject.UserID(userID), mediaobject.EntryID(e.ID), mediaobject.StateNEQ(mediaobject.StateDeleting)).Count(ctx); n >= 20 {
		return nil, ErrInvalid
	}
	key := "timeline/media/" + uuid.NewString()
	exp := time.Now().UTC().Add(10 * time.Minute)
	m, err := tx.MediaObject.Create().SetPublicRef(refs.New("med_")).SetUserID(userID).SetEntryID(e.ID).SetEntryPublicRef(e.PublicRef).SetMutationID(u.MutationID).SetFilename(strings.TrimSpace(u.Filename)).SetContentType(u.ContentType).SetSizeBytes(u.SizeBytes).SetUploadKey(key).SetObjectKey(key).SetState(mediaobject.StatePending).SetUploadExpiresAt(exp).Save(ctx)
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
	out := toMedia(m)
	out.UploadURL, out.Headers, out.ExpiresAt = url, headers, exp
	return out, nil
}

func Complete(ctx context.Context, st *Store, userID, mediaRef, mutationID string) (*Media, error) {
	if st == nil || st.DB == nil || st.Blobs == nil || userID == "" || !refs.Valid(mediaRef, "med_") || mutationID == "" {
		return nil, ErrInvalid
	}
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	m, err := tx.MediaObject.Query().Where(mediaobject.UserID(userID), mediaobject.PublicRefEQ(mediaRef)).Only(ctx)
	if err != nil {
		return nil, ErrNotFound
	}
	if m.State == mediaobject.StateReady {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return toMedia(m), nil
	}
	if m.State != mediaobject.StatePending || m.UploadExpiresAt == nil || !m.UploadExpiresAt.After(time.Now().UTC()) {
		return nil, ErrInvalid
	}
	r, err := st.Blobs.Get(ctx, m.UploadKey)
	if err != nil {
		return nil, ErrNotFound
	}
	b, readErr := io.ReadAll(io.LimitReader(r, m.SizeBytes+1))
	r.Close()
	if readErr != nil || int64(len(b)) != m.SizeBytes || http.DetectContentType(b) != m.ContentType {
		return nil, ErrInvalid
	}
	m, err = m.Update().SetState(mediaobject.StateReady).SetRevision(m.Revision + 1).Save(ctx)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return toMedia(m), nil
}

func List(ctx context.Context, st *Store, userID, entryRef string) ([]*Media, error) {
	if st == nil || st.DB == nil || userID == "" || !refs.Valid(entryRef, "ent_") {
		return nil, ErrInvalid
	}
	rows, err := st.DB.MediaObject.Query().Where(mediaobject.UserID(userID), mediaobject.EntryPublicRefEQ(entryRef), mediaobject.StateEQ(mediaobject.StateReady)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Media, 0, len(rows))
	for _, row := range rows {
		out = append(out, toMedia(row))
	}
	return out, nil
}

func Read(ctx context.Context, st *Store, userID, mediaRef string) (*Media, io.ReadCloser, error) {
	if st == nil || st.DB == nil || st.Blobs == nil || userID == "" || !refs.Valid(mediaRef, "med_") {
		return nil, nil, ErrInvalid
	}
	m, err := st.DB.MediaObject.Query().Where(mediaobject.UserID(userID), mediaobject.PublicRefEQ(mediaRef), mediaobject.StateEQ(mediaobject.StateReady)).Only(ctx)
	if err != nil {
		return nil, nil, ErrNotFound
	}
	r, err := st.Blobs.Get(ctx, m.ObjectKey)
	if err != nil {
		return nil, nil, ErrNotFound
	}
	return toMedia(m), r, nil
}

func Delete(ctx context.Context, st *Store, userID, mediaRef, mutationID string, expectedRevision int) (*ent.Receipt, error) {
	if st == nil || st.DB == nil || userID == "" || !refs.Valid(mediaRef, "med_") || mutationID == "" || expectedRevision <= 0 {
		return nil, ErrInvalid
	}
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	h := sha256.Sum256([]byte(fmt.Sprintf("media_delete:%s:%d", mediaRef, expectedRevision)))
	hash := hex.EncodeToString(h[:])
	if old, qerr := tx.Receipt.Query().Where(receipt.UserIDEQ(userID), receipt.MutationIDEQ(mutationID)).Only(ctx); qerr == nil {
		if old.Operation != "media_delete" || old.RequestHash != hash || old.MediaRef == nil || *old.MediaRef != mediaRef {
			return nil, ErrInvalid
		}
		return old, nil
	}
	m, err := tx.MediaObject.Query().Where(mediaobject.UserID(userID), mediaobject.PublicRefEQ(mediaRef)).Only(ctx)
	if err != nil {
		return nil, ErrNotFound
	}
	if m.Revision != expectedRevision {
		return nil, ErrInvalid
	}
	m, err = m.Update().SetState(mediaobject.StateDeleting).SetRevision(m.Revision + 1).Save(ctx)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{m.ObjectKey, m.UploadKey, m.ThumbnailKey} {
		if key != "" {
			if err = accountcleanup.QueueObject(ctx, tx, key); err != nil {
				return nil, err
			}
		}
	}
	snapshot := []byte(fmt.Sprintf(`{"media_ref":%q,"entry_ref":%q}`, m.PublicRef, m.EntryPublicRef))
	r, err := tx.Receipt.Create().SetUserID(userID).SetMutationID(mutationID).SetTargetKind("media").SetOperation("media_delete").SetRequestHash(hash).SetEntryRef(m.EntryPublicRef).SetMediaRef(m.PublicRef).SetResultRevision(m.Revision).SetResultState("deleting").SetResultSnapshot(snapshot).Save(ctx)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return r, nil
}
