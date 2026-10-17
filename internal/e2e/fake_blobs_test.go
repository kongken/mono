package e2e_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"time"

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

// chainMediaBlobs is an in-memory object store with upload signing for media e2e.
type chainMediaBlobs struct {
	mu    sync.Mutex
	files map[string][]byte
	fail  bool
}

func newChainMediaBlobs() *chainMediaBlobs {
	return &chainMediaBlobs{files: map[string][]byte{}}
}

func (s *chainMediaBlobs) Put(_ context.Context, key string, r io.ReadSeeker, _ int64, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.files[key] = data
	return nil
}

func (s *chainMediaBlobs) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.files[key]
	if !ok {
		return nil, blobstore.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (s *chainMediaBlobs) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("S3 unavailable")
	}
	delete(s.files, key)
	return nil
}

func (s *chainMediaBlobs) Check(context.Context) error { return nil }

func (s *chainMediaBlobs) SignUpload(_ context.Context, key string, _ int64, _ string, _ time.Duration) (string, map[string]string, error) {
	return "e2e://put/" + key, map[string]string{"Content-Type": "image/png"}, nil
}
