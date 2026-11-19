package application

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/timeline/ent"
	"github.com/kongken/mono/internal/timeline/ent/mediaobject"
	"github.com/kongken/mono/internal/timeline/media"
	"github.com/kongken/mono/internal/timeline/refs"
	timelinev1 "github.com/kongken/mono/pkg/proto/timeline/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// noSignerBlobs implements blobstore.Store but not blobstore.DownloadSigner, the
// configuration gap where an object store cannot hand out a presigned GET.
type noSignerBlobs struct{}

func (noSignerBlobs) Put(context.Context, string, io.ReadSeeker, int64, string) error {
	return nil
}
func (noSignerBlobs) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(nil), nil
}
func (noSignerBlobs) Delete(context.Context, string) error { return nil }

// failingSignerBlobs is a configured object store whose presign call fails, the
// transient failure a client should be told to retry rather than treat as a
// missing attachment.
type failingSignerBlobs struct{ err error }

func (failingSignerBlobs) Put(context.Context, string, io.ReadSeeker, int64, string) error {
	return nil
}
func (failingSignerBlobs) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(nil), nil
}
func (failingSignerBlobs) Delete(context.Context, string) error { return nil }
func (b failingSignerBlobs) SignDownload(context.Context, string, time.Duration) (string, error) {
	return "", b.err
}

func downloadCallCtx() context.Context {
	return rpcauth.WithCall(context.Background(), rpcauth.Call{UserID: "owner"})
}

// --- a one-row object store so DownloadURL's real Ent query can run without a
// live PostgreSQL: the fake driver answers every SELECT with the prepared row.

type mediaRowConnector struct {
	columns []string
	row     []driver.Value
}

func (c mediaRowConnector) Connect(context.Context) (driver.Conn, error) {
	return &mediaRowConn{columns: c.columns, row: c.row}, nil
}
func (mediaRowConnector) Driver() driver.Driver { return mediaRowDriver{} }

type mediaRowDriver struct{}

func (mediaRowDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use mediaRowConnector")
}

type mediaRowConn struct {
	columns []string
	row     []driver.Value
}

func (*mediaRowConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare unsupported")
}
func (*mediaRowConn) Close() error               { return nil }
func (*mediaRowConn) Begin() (driver.Tx, error)  { return nil, errors.New("transactions unsupported") }
func (*mediaRowConn) Ping(context.Context) error { return nil }
func (c *mediaRowConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &mediaRowRows{columns: c.columns, row: c.row}, nil
}

type mediaRowRows struct {
	columns []string
	row     []driver.Value
	done    bool
}

func (r *mediaRowRows) Columns() []string { return r.columns }
func (r *mediaRowRows) Close() error      { return nil }
func (r *mediaRowRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	copy(dest, r.row)
	return nil
}

// newMediaTestClient returns an Ent client over a fake driver that answers the
// media lookup with the given row. It never touches a real database, so the
// download-address path can be exercised in unit tests.
func newMediaTestClient(t *testing.T, row []driver.Value) *ent.Client {
	t.Helper()
	db := sql.OpenDB(mediaRowConnector{columns: mediaobject.Columns, row: row})
	t.Cleanup(func() { _ = db.Close() })
	return ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
}

// readyMediaRow builds a queryable ready MediaObject row keyed by column name,
// so it stays correct if the schema column order changes.
func readyMediaRow(t *testing.T, ref, objectKey string) []driver.Value {
	t.Helper()
	now := time.Now().UTC()
	values := map[string]driver.Value{
		mediaobject.FieldID:             "1",
		mediaobject.FieldPublicRef:      ref,
		mediaobject.FieldUserID:         "owner",
		mediaobject.FieldEntryID:        "1",
		mediaobject.FieldEntryPublicRef: refs.New("ent_"),
		mediaobject.FieldObjectKey:      objectKey,
		mediaobject.FieldUploadKey:      objectKey + ".upload",
		mediaobject.FieldState:          mediaobject.StateReady.String(),
		mediaobject.FieldRevision:       int64(1),
		mediaobject.FieldMutationID:     "mut-1",
		mediaobject.FieldFilename:       "photo.jpg",
		mediaobject.FieldContentType:    "image/jpeg",
		mediaobject.FieldSizeBytes:      int64(5),
		mediaobject.FieldCreatedAt:      now,
		mediaobject.FieldUpdatedAt:      now,
	}
	row := make([]driver.Value, len(mediaobject.Columns))
	for i, col := range mediaobject.Columns {
		row[i] = values[col]
	}
	return row
}

// TestGetMediaDownloadUrlReportsSignerFailureAsUnavailable drives the whole
// service path with a queryable MediaObject and a DownloadSigner that fails:
// GetMediaDownloadUrl -> media.DownloadURL -> signer.SignDownload -> wrapping as
// ErrSigningFailed -> gRPC status. A transient signing failure must be a
// retryable Unavailable, never NotFound.
func TestGetMediaDownloadUrlReportsSignerFailureAsUnavailable(t *testing.T) {
	ref := refs.New("med_")
	client := newMediaTestClient(t, readyMediaRow(t, ref, "timeline/media/object"))
	svc := NewMediaService(&media.Store{DB: client, Blobs: failingSignerBlobs{err: errors.New("presign request timed out")}})

	_, err := svc.GetMediaDownloadUrl(downloadCallCtx(), &timelinev1.GetMediaDownloadUrlRequest{MediaRef: ref})
	if got := status.Code(err); got != codes.Unavailable {
		t.Fatalf("code=%s, want Unavailable (%v)", got, err)
	}
	if status.Code(err) == codes.NotFound {
		t.Fatal("a transient signing failure must not be reported as NotFound")
	}
}

// TestGetMediaDownloadUrlMapsFailuresNotToNotFound guards the rest of the
// download-address failure boundary: only a genuinely absent/foreign/trashed
// record is NotFound, a malformed request is InvalidArgument, and an object
// store that cannot sign is FailedPrecondition, so a client can tell "no such
// attachment" from "this server cannot issue a download address".
func TestGetMediaDownloadUrlMapsFailuresNotToNotFound(t *testing.T) {
	ctx := downloadCallCtx()
	validRef := refs.New("med_")
	queryable := func() *ent.Client { return newMediaTestClient(t, readyMediaRow(t, validRef, "timeline/media/object")) }
	cases := []struct {
		name  string
		store *media.Store
		ref   string
		want  codes.Code
	}{
		{"missing store", nil, validRef, codes.InvalidArgument},
		{"no database", &media.Store{Blobs: noSignerBlobs{}}, validRef, codes.InvalidArgument},
		{"malformed ref", &media.Store{DB: queryable(), Blobs: noSignerBlobs{}}, "med_bad", codes.InvalidArgument},
		{"no download signer", &media.Store{DB: queryable(), Blobs: noSignerBlobs{}}, validRef, codes.FailedPrecondition},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewMediaService(tc.store)
			_, err := svc.GetMediaDownloadUrl(ctx, &timelinev1.GetMediaDownloadUrlRequest{MediaRef: tc.ref})
			if got := status.Code(err); got != tc.want {
				t.Fatalf("code=%s, want %s (%v)", got, tc.want, err)
			}
			if status.Code(err) == codes.NotFound {
				t.Fatalf("a request/service failure must not be reported as NotFound: %v", err)
			}
		})
	}
}

// TestDownloadStatusKeepsServiceFailuresRetryable pins the mapping for the
// failures that cannot be produced through the fake driver: a missing signer is
// the same precondition gap as an unconfigured store, and a transient signer
// failure is retryable Unavailable rather than NotFound.
func TestDownloadStatusKeepsServiceFailuresRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"not found", media.ErrNotFound, codes.NotFound},
		{"invalid", media.ErrInvalid, codes.InvalidArgument},
		{"signing unavailable", media.ErrSigningUnavailable, codes.FailedPrecondition},
		{"signer failure", fmt.Errorf("%w: object store timed out", media.ErrSigningFailed), codes.Unavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := status.Code(downloadStatus(tc.err))
			if got != tc.want {
				t.Fatalf("code=%s, want %s", got, tc.want)
			}
			if tc.name == "signer failure" && got == codes.NotFound {
				t.Fatal("a transient signer failure must not be NotFound")
			}
		})
	}
}
