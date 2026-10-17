package e2e_test

import (
	"context"
	"io"

	"github.com/kongken/mono/internal/tripmap/blobstore"
)

// noopBlobs satisfies tripmap health probes and footprint reads without S3.
type noopBlobs struct{}

func (noopBlobs) Put(context.Context, string, io.ReadSeeker, int64, string) error { return nil }
func (noopBlobs) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, blobstore.ErrNotFound
}
func (noopBlobs) Delete(context.Context, string) error { return nil }
func (noopBlobs) Check(context.Context) error            { return nil }
