package application_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/kongken/mono/internal/tripmap/application"
	"github.com/kongken/mono/internal/tripmap/blobstore"
	"github.com/kongken/mono/internal/tripmap/ent/mediaobject"
	"github.com/kongken/mono/internal/tripmap/ent/user"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type mediaMemory struct {
	mu    sync.Mutex
	files map[string][]byte
	fail  bool
}

func (s *mediaMemory) Put(_ context.Context, key string, r io.ReadSeeker, _ int64, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := io.ReadAll(r)
	s.files[key] = data
	return err
}
func (s *mediaMemory) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.files[key]
	if !ok {
		return nil, blobstore.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (s *mediaMemory) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("offline")
	}
	delete(s.files, key)
	return nil
}
func (s *mediaMemory) SignUpload(context.Context, string, int64, string, time.Duration) (string, map[string]string, error) {
	return "https://storage.example/put", map[string]string{"Content-Type": "image/png"}, nil
}
func photo(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 16, 8))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func preparePhoto(data []byte) *tripmapv1.PrepareUploadRequest {
	return &tripmapv1.PrepareUploadRequest{RegionCode: "330100", Filename: "西湖.png", ContentType: "image/png", SizeBytes: int64(len(data)), UploadId: uuid.NewString()}
}

func TestMediaOwnershipValidationReplayAndDeletionFailure(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.register("media_a"), e.register("media_b")
	memory := &mediaMemory{files: map[string][]byte{}}
	e.store.Blobs = memory
	data := photo(t)
	r := preparePhoto(data)
	_, err := e.media.PrepareUpload(ctx, bearer("", r))
	wantCode(t, err, connect.CodeUnauthenticated)
	bad := preparePhoto(data)
	bad.ContentType = "text/html"
	_, err = e.media.PrepareUpload(ctx, bearer(a, bad))
	wantCode(t, err, connect.CodeInvalidArgument)
	bad = preparePhoto(data)
	bad.SizeBytes = 11 * 1024 * 1024
	_, err = e.media.PrepareUpload(ctx, bearer(a, bad))
	wantCode(t, err, connect.CodeInvalidArgument)
	res, err := e.media.PrepareUpload(ctx, bearer(a, r))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.Media.Id
	replay, err := e.media.PrepareUpload(ctx, bearer(a, r))
	if err != nil || replay.Msg.Media.Id != id {
		t.Fatalf("reservation replay %v %v", replay, err)
	}
	r.Filename = "other.png"
	_, err = e.media.PrepareUpload(ctx, bearer(a, r))
	wantCode(t, err, connect.CodeAlreadyExists)
	for _, operation := range []func() error{
		func() error {
			_, err := e.media.CompleteUpload(ctx, bearer(b, &tripmapv1.CompleteUploadRequest{Id: id}))
			return err
		},
		func() error {
			_, err := e.media.ReadMedia(ctx, bearer(b, &tripmapv1.ReadMediaRequest{Id: id}))
			return err
		},
	} {
		wantCode(t, operation(), connect.CodeNotFound)
	}
	if _, err = e.media.DeleteMedia(ctx, bearer(b, &tripmapv1.DeleteMediaRequest{Id: id})); err != nil {
		t.Fatal(err)
	}
	_, err = e.media.CompleteUpload(ctx, bearer(a, &tripmapv1.CompleteUploadRequest{Id: id}))
	wantCode(t, err, connect.CodeFailedPrecondition)
	m := e.store.DB.MediaObject.GetX(ctx, id)
	if err = memory.Put(ctx, m.UploadKey, bytes.NewReader([]byte("bad")), 3, "image/png"); err != nil {
		t.Fatal(err)
	}
	_, err = e.media.CompleteUpload(ctx, bearer(a, &tripmapv1.CompleteUploadRequest{Id: id}))
	wantCode(t, err, connect.CodeInvalidArgument)
	if err = memory.Put(ctx, m.UploadKey, bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
		t.Fatal(err)
	}
	ready, err := e.media.CompleteUpload(ctx, bearer(a, &tripmapv1.CompleteUploadRequest{Id: id}))
	if err != nil || ready.Msg.Media.Width != 16 {
		t.Fatalf("complete %v %v", ready, err)
	}
	if _, err = e.media.CompleteUpload(ctx, bearer(a, &tripmapv1.CompleteUploadRequest{Id: id})); err != nil {
		t.Fatal(err)
	}
	// Replaying a signed PUT may replace staging, never the canonical image.
	if err = memory.Put(ctx, m.UploadKey, bytes.NewReader([]byte("tampered")), 8, "text/html"); err != nil {
		t.Fatal(err)
	}
	read, err := e.media.ReadMedia(ctx, bearer(a, &tripmapv1.ReadMediaRequest{Id: id, Thumbnail: true}))
	if err != nil || read.Msg.ContentType != "image/jpeg" {
		t.Fatal(read, err)
	}
	if _, err = e.foot.DeleteVisit(ctx, bearer(a, &tripmapv1.DeleteVisitRequest{Code: "330100"})); err != nil {
		t.Fatal(err)
	}
	list, err := e.media.ListMedia(ctx, bearer(a, &tripmapv1.ListMediaRequest{RegionCode: "330100"}))
	if err != nil || len(list.Msg.Media) != 1 {
		t.Fatal("unmarked city lost media", list, err)
	}
	memory.fail = true
	_, err = e.media.DeleteMedia(ctx, bearer(a, &tripmapv1.DeleteMediaRequest{Id: id}))
	wantCode(t, err, connect.CodeUnavailable)
	_, err = e.media.ReadMedia(ctx, bearer(a, &tripmapv1.ReadMediaRequest{Id: id}))
	wantCode(t, err, connect.CodeNotFound)
	memory.fail = false
	if err = application.RunMediaMaintenance(ctx, e.store, time.Now().Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(memory.files) != 0 || e.store.DB.MediaCleanup.Query().CountX(ctx) != 0 {
		t.Fatal("cleanup left objects")
	}
	if _, err = e.media.DeleteMedia(ctx, bearer(a, &tripmapv1.DeleteMediaRequest{Id: id})); err != nil {
		t.Fatal("delete replay", err)
	}
}

func TestMediaQuotaAtomicReservationsAndCleanupOutlivesAccount(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	token := e.register("media_quota")
	memory := &mediaMemory{files: map[string][]byte{}}
	e.store.Blobs = memory
	data := photo(t)
	uid := e.store.DB.User.Query().Where(user.UsernameEQ("media_quota")).OnlyX(ctx).ID
	for i := 0; i < 19; i++ {
		e.store.DB.MediaObject.Create().SetUserID(uid).SetRegionCode("330100").SetObjectKey(uuid.NewString()).SetContentType("image/png").SetSizeBytes(1).SaveX(ctx)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Go(func() { _, err := e.media.PrepareUpload(ctx, bearer(token, preparePhoto(data))); results <- err })
	}
	wg.Wait()
	close(results)
	success, limited := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if codeOf(err) == connect.CodeResourceExhausted {
			limited++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || limited != 1 {
		t.Fatalf("quota race %d %d", success, limited)
	}
	staging := e.store.DB.MediaObject.Query().Where(mediaobject.UserID(uid), mediaobject.UploadKeyNEQ("")).OnlyX(ctx).UploadKey
	e.mustCleanupAccount(ctx, token)
	if e.store.DB.MediaCleanup.Query().CountX(ctx) != 1 {
		t.Fatal("late upload cleanup lost on account cascade")
	}
	if err := memory.Put(ctx, staging, bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
		t.Fatal(err)
	}
	if err := application.RunMediaMaintenance(ctx, e.store, time.Now().Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(memory.files) != 0 {
		t.Fatal("late PUT after deletion was not removed")
	}
}

func TestMediaRealS3SignedUploadAndPrivateRead(t *testing.T) {
	endpoint := os.Getenv("TRIPMAP_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("requires isolated S3")
	}
	e := newEnv(t)
	ctx := context.Background()
	token := e.register("media_s3")
	client := s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider(os.Getenv("TRIPMAP_TEST_S3_ACCESS_KEY"), os.Getenv("TRIPMAP_TEST_S3_SECRET_KEY"), "")}, func(o *s3.Options) { o.BaseEndpoint = aws.String(endpoint); o.UsePathStyle = true })
	blobs := blobstore.NewS3(client, os.Getenv("TRIPMAP_TEST_S3_BUCKET"), "media-tests/"+uuid.NewString())
	e.store.Blobs = blobs
	data := photo(t)
	res, err := e.media.PrepareUpload(ctx, bearer(token, preparePhoto(data)))
	if err != nil {
		t.Fatal(err)
	}
	m := e.store.DB.MediaObject.GetX(ctx, res.Msg.Media.Id)
	t.Cleanup(func() {
		for _, key := range []string{m.ObjectKey, m.ThumbnailKey, m.UploadKey} {
			_ = blobs.Delete(context.Background(), key)
		}
	})
	req, err := http.NewRequestWithContext(ctx, "PUT", res.Msg.UploadUrl, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	for key, val := range res.Msg.Headers {
		req.Header.Set(key, val)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		if os.Getenv("TRIPMAP_TEST_S3_ENDPOINT") != "" {
			t.Skip("S3 endpoint configured but unreachable:", err)
		}
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode/100 != 2 {
		t.Fatalf("signed PUT %d %s", response.StatusCode, body)
	}
	if _, err = e.media.CompleteUpload(ctx, bearer(token, &tripmapv1.CompleteUploadRequest{Id: m.ID})); err != nil {
		t.Fatal(err)
	}
	if _, err = e.media.ReadMedia(ctx, bearer(token, &tripmapv1.ReadMediaRequest{Id: m.ID})); err != nil {
		t.Fatal(err)
	}
	if _, err = e.media.DeleteMedia(ctx, bearer(token, &tripmapv1.DeleteMediaRequest{Id: m.ID})); err != nil {
		t.Fatal(err)
	}
	if _, err = blobs.Get(ctx, m.ObjectKey); !errors.Is(err, blobstore.ErrNotFound) {
		t.Fatal("object still accessible", err)
	}
}
