package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
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

// OAuthState is what a single authorization attempt remembers server-side.
type OAuthState struct {
	// Provider is the registry name the attempt was started for.
	Provider string
	// CodeVerifier is the PKCE code verifier for providers that require one.
	// It never leaves the server; only its S256 challenge is sent out.
	CodeVerifier string
}

type storedState struct {
	Provider     string `json:"p"`
	CodeVerifier string `json:"v,omitempty"`
}

// Create stores a new state for provider and returns it. codeVerifier is kept
// alongside the state and returned by Consume; pass "" for providers without
// PKCE.
func (s *StateStore) Create(ctx context.Context, provider, codeVerifier string) (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	state := base64.RawURLEncoding.EncodeToString(buf)
	value, err := json.Marshal(storedState{Provider: provider, CodeVerifier: codeVerifier})
	if err != nil {
		return "", err
	}
	if err := s.rdb.Set(ctx, s.prefix+"oauth:"+state, value, OAuthStateTTL).Err(); err != nil {
		return "", err
	}
	return state, nil
}

// Consume atomically reads and deletes the state.
func (s *StateStore) Consume(ctx context.Context, state string) (OAuthState, error) {
	raw, err := s.rdb.GetDel(ctx, s.prefix+"oauth:"+state).Result()
	if errors.Is(err, redis.Nil) {
		return OAuthState{}, ErrStateNotFound
	}
	if err != nil {
		return OAuthState{}, err
	}
	if !strings.HasPrefix(raw, "{") { // written before PKCE support: the bare provider name
		return OAuthState{Provider: raw}, nil
	}
	var v storedState
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return OAuthState{}, ErrStateNotFound
	}
	return OAuthState{Provider: v.Provider, CodeVerifier: v.CodeVerifier}, nil
}
