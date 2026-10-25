package e2e_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"connectrpc.com/connect"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/kongken/mono/internal/tripmap/blobstore"
	tripmapconfig "github.com/kongken/mono/internal/tripmap/config"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

// realS3 returns an object store on the S3 server of TRIPMAP_TEST_S3_* (the Compose or CI
// service), skipping when none is configured: the other chain tests use an in-memory store.
func realS3(t *testing.T) *blobstore.S3 {
	t.Helper()
	endpoint := os.Getenv("TRIPMAP_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set TRIPMAP_TEST_S3_ENDPOINT (and the other TRIPMAP_TEST_S3_* variables) for a real object store")
	}
	client := s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider(os.Getenv("TRIPMAP_TEST_S3_ACCESS_KEY"), os.Getenv("TRIPMAP_TEST_S3_SECRET_KEY"), ""),
	}, func(o *s3.Options) { o.BaseEndpoint = aws.String(endpoint); o.UsePathStyle = true })
	// A prefix of its own keeps repeated runs apart from the other tests' objects.
	store := blobstore.NewS3(client, os.Getenv("TRIPMAP_TEST_S3_BUCKET"), "e2e-"+uuid.NewString()[:8])
	if err := store.Check(context.Background()); err != nil {
		t.Fatalf("configured S3 endpoint is not ready: %v", err)
	}
	return store
}

// Spec §14 scenarios 19-21 against a real object store: an account with an uploaded photo (and
// its derived thumbnail) and a not yet completed upload is deleted through the whole chain, and
// every object of it is gone from S3 afterwards while another user's photo is untouched.
func TestChainAccountDeletionRemovesPhotosFromRealS3(t *testing.T) {
	store := realS3(t)
	h := NewChainHarness(t, store, func(cfg *tripmapconfig.AppConfig) {
		cfg.Storage = tripmapconfig.StorageConfig{KeyPrefix: "tripmap"}
	})
	ctx := context.Background()

	upload := func(b *Browser, region string, complete bool) (id string, keys []string) {
		t.Helper()
		data := photoPNG(t)
		prep, err := b.PrepareUpload(ctx, &bfftripmapv1.PrepareUploadRequest{
			RegionCode: region, Filename: "photo.png", ContentType: "image/png",
			SizeBytes: int64(len(data)), UploadId: uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}
		id = prep.GetMedia().GetId()
		m := h.TripmapDB.MediaObject.GetX(ctx, id)
		if err = store.Put(ctx, m.UploadKey, bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
			t.Fatal(err)
		}
		if complete {
			if _, err = b.CompleteUpload(ctx, id); err != nil {
				t.Fatalf("complete: %v", err)
			}
			m = h.TripmapDB.MediaObject.GetX(ctx, id)
		}
		// Completing moves the photo out of staging: the live objects are then the photo and
		// its thumbnail; an unfinished upload only has its staging object.
		live := []string{m.ObjectKey, m.ThumbnailKey}
		if !complete {
			live = []string{m.UploadKey}
		}
		for _, k := range live {
			if k != "" {
				keys = append(keys, k)
			}
		}
		return id, keys
	}
	exists := func(key string) bool {
		t.Helper()
		r, err := store.Get(ctx, key)
		if err == nil {
			_ = r.Close()
			return true
		}
		if !errors.Is(err, blobstore.ErrNotFound) {
			t.Fatalf("get %s: %v", key, err)
		}
		return false
	}

	a, other := h.FreshBrowser(), h.FreshBrowser()
	aID, err := a.Register(ctx, "s3_owner", "", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.Register(ctx, "s3_other", "", "Other"); err != nil {
		t.Fatal(err)
	}
	for _, b := range []*Browser{a, other} {
		csrf := b.refreshCSRF(ctx)
		req := newSyncCity(b, csrf, "330100", "杭州市", "m-"+uuid.NewString()[:6])
		if _, err = b.Foot.SyncChanges(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	_, mine := upload(a, "330100", true)
	_, pending := upload(a, "330100", false)
	_, theirs := upload(other, "330100", true)
	if len(mine) == 0 || len(pending) == 0 || len(theirs) == 0 {
		t.Fatalf("test objects were not created: %v %v %v", mine, pending, theirs)
	}
	t.Cleanup(func() {
		for _, k := range theirs {
			_ = store.Delete(context.Background(), k)
		}
	})
	for _, k := range append(append(append([]string{}, mine...), pending...), theirs...) {
		if !exists(k) {
			t.Fatalf("object %s is missing before the deletion", k)
		}
	}

	if err = a.Reauthenticate(ctx, E2EPassword); err != nil {
		t.Fatal(err)
	}
	if err = a.DeleteAccount(ctx, &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "s3-delete-1"}, aID); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	for _, k := range append(append([]string{}, mine...), pending...) {
		if exists(k) {
			t.Errorf("object %s of the deleted account is still in S3", k)
		}
	}
	for _, k := range theirs {
		if !exists(k) {
			t.Errorf("object %s of another account was deleted", k)
		}
	}
	if n := h.TripmapDB.MediaObject.Query().CountX(ctx); n != 1 {
		t.Errorf("%d media rows left, want only the other user's", n)
	}
}

func newSyncCity(b *Browser, csrf, code, name, mutation string) *connect.Request[bfftripmapv1.SyncChangesRequest] {
	req := connect.NewRequest(&bfftripmapv1.SyncChangesRequest{
		MutationId: mutation, BaseRevision: 0,
		Mutations: []*bfftripmapv1.FootprintMutation{{
			Operation: &bfftripmapv1.FootprintMutation_UpsertCity{
				UpsertCity: &bfftripmapv1.Visit{Code: code, Name: name, Province: "330000", Color: "#4b937d"},
			},
		}},
	})
	b.writeHeaders(req, csrf, b.Origin)
	return req
}
