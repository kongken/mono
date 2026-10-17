package application

import (
	"errors"

	"connectrpc.com/connect"
	"github.com/redis/go-redis/v9"
	"github.com/kongken/mono/internal/tripmap/blobstore"
	"github.com/kongken/mono/internal/tripmap/ent"
)

// Store is the set of backing services business RPCs need.
type Store struct {
	DB    *ent.Client
	Redis redis.Cmdable
	Blobs blobstore.Store
	// KeyPrefix namespaces Redis keys; empty means the default "tripmap:".
	KeyPrefix string
}

const redisPrefix = "tripmap:"

// Limiter is the fixed-window rate limiter over this store's Redis and key prefix.
func (st *Store) Limiter() *Limiter { return NewLimiter(st.Redis, st.prefix()) }

func (st *Store) prefix() string {
	if st.KeyPrefix != "" {
		return st.KeyPrefix
	}
	return redisPrefix
}

// StoreSource returns the Store once bootstrap has finished, or nil before.
// Services are registered before dependencies are connected, so they resolve
// the Store per request.
type StoreSource interface{ Store() *Store }

func storeFrom(src StoreSource) (*Store, error) {
	if st := src.Store(); st != nil {
		return st, nil
	}
	return nil, connect.NewError(connect.CodeUnavailable, errors.New("service is starting"))
}

func internalError(err error) error {
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}
