package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// OAuthStateTTL bounds how long an unredeemed authorization attempt stays valid.
const OAuthStateTTL = 10 * time.Minute

// ErrStateNotFound means the state is unknown, expired or already used.
var ErrStateNotFound = errors.New("oauth state not found")

// StateStore keeps single-use OAuth state tokens in Redis.
type StateStore struct {
	rdb    redis.Cmdable
	prefix string
}

func NewStateStore(rdb redis.Cmdable, prefix string) *StateStore {
	return &StateStore{rdb: rdb, prefix: prefix}
}

// Create stores a new state for provider and returns it.
func (s *StateStore) Create(ctx context.Context, provider string) (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	state := base64.RawURLEncoding.EncodeToString(buf)
	if err := s.rdb.Set(ctx, s.prefix+"oauth:"+state, provider, OAuthStateTTL).Err(); err != nil {
		return "", err
	}
	return state, nil
}

// Consume atomically reads and deletes the state, returning its provider.
func (s *StateStore) Consume(ctx context.Context, state string) (string, error) {
	provider, err := s.rdb.GetDel(ctx, s.prefix+"oauth:"+state).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrStateNotFound
	}
	return provider, err
}
