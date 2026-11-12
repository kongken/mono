package media

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
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
