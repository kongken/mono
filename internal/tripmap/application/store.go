package application

import (
	"errors"

	"connectrpc.com/connect"
	"github.com/redis/go-redis/v9"
	"go.orx.me/apps/trip-map/internal/tripmap/blobstore"
	"go.orx.me/apps/trip-map/internal/tripmap/ent"
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
