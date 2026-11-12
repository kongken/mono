package blobstore

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestSignDownloadWithoutPresignClientIsConfigurationGap keeps the download
// precondition diagnosable: an S3 store built from a client that cannot presign
// reports the configuration gap, not a missing object or a transient failure.
// A nil client is never used, so this needs no object store.
func TestSignDownloadWithoutPresignClientIsConfigurationGap(t *testing.T) {
	s := NewS3(nil, "bucket", "")
	_, err := s.SignDownload(context.Background(), "timeline/media/object", 5*time.Minute)
	if !errors.Is(err, ErrSigningUnavailable) {
		t.Fatalf("err=%v, want ErrSigningUnavailable", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing presign client must not look like a missing object: %v", err)
	}
}
