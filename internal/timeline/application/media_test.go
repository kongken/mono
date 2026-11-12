package application

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/timeline/ent"
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

func downloadCallCtx() context.Context {
	return rpcauth.WithCall(context.Background(), rpcauth.Call{UserID: "owner"})
}

// TestGetMediaDownloadUrlMapsFailuresNotToNotFound guards the download-address
// failure boundary: only a genuinely absent/foreign/trashed record is NotFound.
// A malformed request is InvalidArgument and an object store that cannot sign is
// FailedPrecondition, so a client can tell "no such attachment" from "this
// server cannot issue a download address".
func TestGetMediaDownloadUrlMapsFailuresNotToNotFound(t *testing.T) {
	ctx := downloadCallCtx()
	validRef := refs.New("med_")
	cases := []struct {
		name  string
		store *media.Store
		ref   string
		want  codes.Code
	}{
		{"missing store", nil, validRef, codes.InvalidArgument},
		{"no database", &media.Store{Blobs: noSignerBlobs{}}, validRef, codes.InvalidArgument},
		{"malformed ref", &media.Store{DB: &ent.Client{}, Blobs: noSignerBlobs{}}, "med_bad", codes.InvalidArgument},
		{"no download signer", &media.Store{DB: &ent.Client{}, Blobs: noSignerBlobs{}}, validRef, codes.FailedPrecondition},
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
// failures that cannot be produced without a live database: a transient
// presigner or object-store failure is retryable Unavailable, not NotFound.
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
