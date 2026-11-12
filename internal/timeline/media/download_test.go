package media

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/kongken/mono/internal/timeline/ent"
)

// plainBlobs implements the object store but not the download signer.
type plainBlobs struct{}

func (plainBlobs) Put(context.Context, string, io.ReadSeeker, int64, string) error { return nil }
func (plainBlobs) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("bytes")), nil
}
func (plainBlobs) Delete(context.Context, string) error { return nil }

func TestDownloadURLRejectsInvalidInputs(t *testing.T) {
	ctx := context.Background()
	for name, store := range map[string]*Store{
		"nil store":        nil,
		"empty store":      {},
		"missing database": {Blobs: plainBlobs{}},
	} {
		if _, err := DownloadURL(ctx, store, "user", "med_0123456789abcdefghjkmnpqrs", 0); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: err=%v, want invalid", name, err)
		}
	}
}

// TestDownloadURLDistinguishesMissingSigner pins the sentinel the application
// layer maps to a failed precondition: an object store that cannot sign is a
// configuration gap, not a missing attachment. The capability is checked before
// the database is queried, so this needs no live PostgreSQL.
func TestDownloadURLDistinguishesMissingSigner(t *testing.T) {
	store := &Store{DB: &ent.Client{}, Blobs: plainBlobs{}}
	_, err := DownloadURL(context.Background(), store, "user", "med_0123456789abcdefghjkmnpqrs", 0)
	if !errors.Is(err, ErrSigningUnavailable) {
		t.Fatalf("err=%v, want ErrSigningUnavailable", err)
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalid) {
		t.Fatalf("a missing signer must not be reported as not found or invalid: %v", err)
	}
}
