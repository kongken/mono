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

// requireRealS3Env switches the S3 scenarios from "use the real object store when one is
// configured" to "must use it": with E2E_REQUIRE_REAL_S3=1 a missing or incomplete TRIPMAP_TEST_S3_*
// configuration fails the test instead of skipping it or falling back to the in-memory store. CI's
// Backend job sets it, so its S3 evidence never rests on another test's PASS line; a local run
// leaves it unset and keeps the in-memory default.
const requireRealS3Env = "E2E_REQUIRE_REAL_S3"

func realS3Required() bool { return os.Getenv(requireRealS3Env) == "1" }

var s3TestVariables = []string{"TRIPMAP_TEST_S3_ENDPOINT", "TRIPMAP_TEST_S3_ACCESS_KEY", "TRIPMAP_TEST_S3_SECRET_KEY", "TRIPMAP_TEST_S3_BUCKET"}

// realS3Configured reports whether the real object store is configured. In the required mode it
// fails the test when any of the variables is missing.
func realS3Configured(t *testing.T) bool {
	t.Helper()
	if realS3Required() {
		for _, name := range s3TestVariables {
			if os.Getenv(name) == "" {
				t.Fatalf("%s=1 requires a real S3 object store, but %s is not set", requireRealS3Env, name)
			}
		}
	}
	return os.Getenv("TRIPMAP_TEST_S3_ENDPOINT") != ""
}

// realS3 returns an object store on the S3 server of TRIPMAP_TEST_S3_* (the Compose or CI
// service). Without one it skips, or fails in the required mode. Its keys live under a prefix of
// its own, so no other test or run shares an object with it.
func realS3(t *testing.T) *blobstore.S3 {
	t.Helper()
	if !realS3Configured(t) {
		t.Skip("set TRIPMAP_TEST_S3_ENDPOINT (and the other TRIPMAP_TEST_S3_* variables) for a real object store")
	}
	client := s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider(os.Getenv("TRIPMAP_TEST_S3_ACCESS_KEY"), os.Getenv("TRIPMAP_TEST_S3_SECRET_KEY"), ""),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(os.Getenv("TRIPMAP_TEST_S3_ENDPOINT"))
		o.UsePathStyle = true
	})
	prefix := "e2e-" + uuid.NewString()
	store := blobstore.NewS3(client, os.Getenv("TRIPMAP_TEST_S3_BUCKET"), prefix)
	if err := store.Check(context.Background()); err != nil {
		t.Fatalf("configured S3 endpoint is not ready: %v", err)
	}
	t.Logf("object store: real S3 (bucket %s, key prefix %s)", os.Getenv("TRIPMAP_TEST_S3_BUCKET"), prefix)
	return store
}

// objectStoreForChain is the real S3 when it is configured (or required) and an in-memory store
// otherwise, and says which in the log, so a run's evidence shows what it ran on.
func objectStoreForChain(t *testing.T) blobstore.Store {
	t.Helper()
	if realS3Configured(t) {
		return realS3(t)
	}
	t.Log("object store: in-memory (no TRIPMAP_TEST_S3_ENDPOINT)")
	return newChainMediaBlobs()
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
		// Every object this test made goes away with it, whatever the outcome.
		t.Cleanup(func() {
			for _, k := range append([]string{m.UploadKey}, keys...) {
				_ = store.Delete(context.Background(), k)
			}
		})
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
