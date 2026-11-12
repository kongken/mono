// Attachment storage against real PostgreSQL and a real object store.
//
// Every other Timeline media test uses a fake blob store, so the one thing they
// cannot see is the part that actually broke in review: the browser uploads to a
// presigned URL and the signature covers the Content-Type, which is why
// PrepareUpload has to hand the headers back and the page has to replay them
// verbatim. These scenarios drive the real signer, a real HTTP PUT and a real
// bucket, so a dropped header or a wrong expiry fails here instead of in the
// browser. The download scenario drives the real presigned GET that backs the
// explicit download-address contract, so an address that cannot be fetched from
// the bucket fails here rather than in an MCP client.
//
// Needs TIMELINE_TEST_POSTGRES_DSN and TIMELINE_TEST_S3_* (CI's "Timeline
// migrations, receipts and attachments" job sets all of them and fails when a
// scenario is skipped); without them the scenarios skip, like the migration tests.
package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/kongken/mono/internal/timeline/ent"
	"github.com/kongken/mono/internal/timeline/ent/mediaobject"
	"github.com/kongken/mono/internal/timeline/media"
	"github.com/kongken/mono/internal/tripmap/blobstore"
)

// aPNG is a real 1x1 image: enough bytes for http.DetectContentType to agree with
// the declared type, which is what Complete checks after the upload.
const aPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFAAH/q842iQAAAABJRU5ErkJggg=="

type mediaFixture struct {
	ctx    context.Context
	store  *media.Store
	client *ent.Client
	user   string
	blobs  *blobstore.S3
}

// newMediaFixture gives one throwaway schema, one entropy-suffixed bucket prefix
// and an entry owned by a single user. The schema is created from the Ent
// definition rather than the SQL files: those have their own tests, and a media
// scenario should fail on upload behaviour, not on migration bookkeeping.
func newMediaFixture(t *testing.T) *mediaFixture {
	t.Helper()
	dsn := os.Getenv("TIMELINE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TIMELINE_TEST_POSTGRES_DSN for live PostgreSQL checks (make test-integration-timeline)")
	}
	endpoint := os.Getenv("TIMELINE_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set TIMELINE_TEST_S3_ENDPOINT for live object-store checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "timeline_media_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`)
	})
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	scoped := entsql.OpenDB(dialect.Postgres, stdlib.OpenDB(*cfg))
	t.Cleanup(func() { _ = scoped.Close() })
	client := ent.NewClient(ent.Driver(scoped))
	t.Cleanup(func() { _ = client.Close() })
	if err = client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}

	bucket := os.Getenv("TIMELINE_TEST_S3_BUCKET")
	if bucket == "" {
		bucket = "timeline"
	}
	s3c := awss3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider(os.Getenv("TIMELINE_TEST_S3_ACCESS_KEY"), os.Getenv("TIMELINE_TEST_S3_SECRET_KEY"), "")}, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	// An empty prefix: the media service signs "timeline/media/<id>" itself.
	blobs := blobstore.NewS3(s3c, bucket, "")
	if err = blobs.Check(ctx); err != nil {
		t.Fatalf("object store at %s is configured but unusable (create the %q bucket first): %v", endpoint, bucket, err)
	}
	return &mediaFixture{ctx: ctx, store: &media.Store{DB: client, Blobs: blobs}, client: client, user: "media-user", blobs: blobs}
}

func (f *mediaFixture) newEntry(t *testing.T) *ent.Entry {
	t.Helper()
	e, err := f.client.Entry.Create().
		SetUserID(f.user).
		SetTitle("附件记录").
		SetOccurredDate("2026-11-10").
		SetTimezone("Asia/Shanghai").
		SetOccurredAt(time.Now().UTC()).
		Save(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// upload runs the real sequence the page runs: Prepare, signed PUT, Complete.
func (f *mediaFixture) upload(t *testing.T, entryRef string, mutationID string, data []byte, contentType string) *media.Media {
	t.Helper()
	prepared, err := media.Prepare(f.ctx, f.store, f.user, media.Upload{
		EntryRef: entryRef, Filename: "probe.png", ContentType: contentType,
		SizeBytes: int64(len(data)), MutationID: mutationID,
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	put(t, prepared, data, "")
	completed, err := media.Complete(f.ctx, f.store, f.user, prepared.Ref, mutationID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	return completed
}

// put sends the browser's request with exactly the headers the signature covers.
// overrideType, when set, replaces Content-Type: that is the mistake the page used
// to make by setting its own type instead of replaying the signed one.
func put(t *testing.T, prepared *media.Media, data []byte, overrideType string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, prepared.UploadURL, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range prepared.Headers {
		req.Header.Set(name, value)
	}
	if overrideType != "" {
		req.Header.Set("Content-Type", overrideType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("presigned PUT: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("presigned PUT: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return resp
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(aPNG)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestPresignedUploadRoundTripThroughTheBucket is the acceptance for the upload
// path: the signed URL accepts the bytes the page sends, Complete sees them in the
// bucket, and the stored object is byte-identical to what the user picked.
func TestPresignedUploadRoundTripThroughTheBucket(t *testing.T) {
	f := newMediaFixture(t)
	e := f.newEntry(t)
	object := f.upload(t, e.PublicRef, "mutation-upload-1", pngBytes(t), "image/png")

	if object.Status != "ready" || object.Ref == "" {
		t.Fatalf("completed media is not ready with an opaque ref: %+v", object)
	}
	row, err := f.client.MediaObject.Query().Where(mediaobject.PublicRefEQ(object.Ref)).Only(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := f.blobs.Get(f.ctx, row.ObjectKey)
	if err != nil {
		t.Fatalf("uploaded object is not in the bucket under %q: %v", row.ObjectKey, err)
	}
	stored, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, pngBytes(t)) {
		t.Fatalf("stored object differs from the upload: %d vs %d bytes", len(stored), len(pngBytes(t)))
	}
}

// TestAttachmentDownloadAddressFetchesTheStoredObject is the acceptance for the
// explicit download-address contract: DownloadURL returns a short-lived presigned
// address that actually serves the stored bytes, and the same address is refused
// for another account.
func TestAttachmentDownloadAddressFetchesTheStoredObject(t *testing.T) {
	f := newMediaFixture(t)
	e := f.newEntry(t)
	object := f.upload(t, e.PublicRef, "mutation-download-1", pngBytes(t), "image/png")

	address, err := media.DownloadURL(f.ctx, f.store, f.user, object.Ref, 0)
	if err != nil {
		t.Fatalf("download address: %v", err)
	}
	if address.Filename != "probe.png" || address.ContentType != "image/png" {
		t.Fatalf("download address lost the attachment metadata: %+v", address)
	}
	if remaining := time.Until(address.ExpiresAt); remaining <= 0 || remaining > media.DownloadURLTTL+time.Minute {
		t.Fatalf("expiry %s is outside the short-lived window", remaining)
	}
	req, err := http.NewRequestWithContext(f.ctx, http.MethodGet, address.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("presigned GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("presigned GET: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if !bytes.Equal(body, pngBytes(t)) {
		t.Fatalf("downloaded object differs from the upload: %d vs %d bytes", len(body), len(pngBytes(t)))
	}
	if _, err := media.DownloadURL(f.ctx, f.store, "someone-else", object.Ref, 0); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("another account's attachment: err=%v, want not found", err)
	}
}

// TestPrepareUploadHandsBackTheHeadersTheSignatureCovers guards the defect that
// reached review: the signature covers Content-Type, so a client that sets its own
// is refused. If Prepare ever stops returning the headers, this scenario fails.
func TestPrepareUploadHandsBackTheHeadersTheSignatureCovers(t *testing.T) {
	f := newMediaFixture(t)
	e := f.newEntry(t)
	prepared, err := media.Prepare(f.ctx, f.store, f.user, media.Upload{
		EntryRef: e.PublicRef, Filename: "probe.png", ContentType: "image/png",
		SizeBytes: int64(len(pngBytes(t))), MutationID: "mutation-headers-1",
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if prepared.Headers["Content-Type"] != "image/png" {
		t.Fatalf("PrepareUpload must return the signed Content-Type, got %v", prepared.Headers)
	}
	if prepared.UploadURL == "" || !prepared.ExpiresAt.After(time.Now()) {
		t.Fatalf("prepared upload has no usable URL or expiry: %+v", prepared)
	}
	if _, ok := prepared.Headers["Host"]; ok {
		t.Fatalf("Host must not be handed to the browser: %v", prepared.Headers)
	}
	if _, ok := prepared.Headers["Content-Length"]; ok {
		t.Fatalf("Content-Length must not be handed to the browser: %v", prepared.Headers)
	}
	if status, body := putExpectRefusal(t, prepared, pngBytes(t), "image/jpeg"); status < 300 {
		t.Fatalf("a PUT that sets its own Content-Type was accepted (%d): the signature covers the type, so the page must replay it: %s", status, body)
	}
	if _, err = media.Complete(f.ctx, f.store, f.user, prepared.Ref, "mutation-headers-1"); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("Complete must not accept a record the bucket never received, got %v", err)
	}
}

// putExpectRefusal sends the same request with a different Content-Type, the
// mistake the page used to make when it set the type itself instead of replaying
// the signed one. A refusal is the expected result, so it is returned, not failed.
func putExpectRefusal(t *testing.T, prepared *media.Media, data []byte, contentType string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, prepared.UploadURL, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range prepared.Headers {
		req.Header.Set(name, value)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("presigned PUT: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, strings.TrimSpace(string(body))
}

// TestDeleteQueuesTheObjectForCleanup keeps deletion honest against the real
// store: the row becomes a tombstone, every key is queued, and replaying the same
// mutation returns the recorded receipt instead of deleting twice.
func TestDeleteQueuesTheObjectForCleanup(t *testing.T) {
	f := newMediaFixture(t)
	e := f.newEntry(t)
	object := f.upload(t, e.PublicRef, "mutation-delete-0", pngBytes(t), "image/png")
	row, err := f.client.MediaObject.Query().Where(mediaobject.PublicRefEQ(object.Ref)).Only(f.ctx)
	if err != nil {
		t.Fatal(err)
	}

	rev := row.Revision
	receipt, err := media.Delete(f.ctx, f.store, f.user, object.Ref, "mutation-delete-1", rev)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	row, err = f.client.MediaObject.Query().Where(mediaobject.PublicRefEQ(object.Ref)).Only(f.ctx)
	if err != nil || row.State != mediaobject.StateDeleting {
		t.Fatalf("deleted media must stay as a tombstone: %v %+v", err, row)
	}
	var queued []string
	if queued, err = f.cleanupKeys(); err != nil {
		t.Fatal(err)
	}
	if !contains(queued, row.ObjectKey) {
		t.Fatalf("the uploaded object is not queued for cleanup: %v", queued)
	}
	// Pending media carry object_key and upload_key with the same value, so the
	// second enqueue must be ignored rather than rejected or duplicated.
	if n := count(queued, row.ObjectKey); n != 1 {
		t.Fatalf("the object is queued %d times, want exactly one row: %v", n, queued)
	}
	replayed, err := media.Delete(f.ctx, f.store, f.user, object.Ref, "mutation-delete-1", rev)
	if err != nil || replayed.ID != receipt.ID || replayed.MediaRef == nil || *replayed.MediaRef != object.Ref {
		t.Fatalf("replaying the delete must return the recorded receipt: %v %v", replayed, err)
	}
	if _, err = media.Delete(f.ctx, f.store, f.user, object.Ref, "mutation-delete-2", rev+5); !errors.Is(err, media.ErrInvalid) {
		t.Fatalf("a delete at an unknown revision must be refused, got %v", err)
	}
}

// TestPrepareRefusesTheTwentyFirstAttachment is the per-record limit the ADR
// states (20 files). It lives on the row count, not on the client, so only a real
// database can show the 21st Prepare is refused.
func TestPrepareRefusesTheTwentyFirstAttachment(t *testing.T) {
	f := newMediaFixture(t)
	e := f.newEntry(t)
	prepare := func(n int) (*media.Media, error) {
		return media.Prepare(f.ctx, f.store, f.user, media.Upload{
			EntryRef: e.PublicRef, Filename: "probe.png", ContentType: "image/png",
			SizeBytes: int64(len(pngBytes(t))), MutationID: "mutation-limit-" + strconv.Itoa(n),
		})
	}
	for i := 0; i < 20; i++ {
		if _, err := prepare(i); err != nil {
			t.Fatalf("attachment %d of 20 was refused: %v", i+1, err)
		}
	}
	if _, err := prepare(20); !errors.Is(err, media.ErrInvalid) {
		t.Fatalf("a 21st attachment must be refused, got %v", err)
	}
}

func (f *mediaFixture) cleanupKeys() ([]string, error) {
	rows, err := f.client.CleanupObject.Query().All(f.ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, row.ObjectKey)
	}
	return keys, nil
}

func count(values []string, want string) int {
	found := 0
	for _, value := range values {
		if value == want {
			found++
		}
	}
	return found
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
