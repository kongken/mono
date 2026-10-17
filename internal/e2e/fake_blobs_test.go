package e2e_test

import (
	"context"
	"errors"
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
func (noopBlobs) Check(context.Context) error          { return nil }

// faultInjectingBlobs records Delete calls and can fail until cleared.
type faultInjectingBlobs struct {
	fail bool
	keys []string
}

func (s *faultInjectingBlobs) Put(context.Context, string, io.ReadSeeker, int64, string) error {
	return nil
}
func (s *faultInjectingBlobs) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, blobstore.ErrNotFound
}
func (s *faultInjectingBlobs) Delete(_ context.Context, key string) error {
	s.keys = append(s.keys, key)
	if s.fail {
		return errors.New("S3 unavailable")
	}
	return nil
}
func (s *faultInjectingBlobs) Check(context.Context) error { return nil }
