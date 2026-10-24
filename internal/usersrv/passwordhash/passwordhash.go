// Package passwordhash hashes and checks passwords with Argon2id. Every hash is
// a self-describing encoded string with its own random salt, and the number of
// hashes computed at the same time in one process is bounded, so a burst of
// logins cannot exhaust memory.
package passwordhash

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// Defaults of spec 4.2: 64 MiB, 3 iterations, 1 lane, 16-byte salt, 32-byte key,
// at most 2 hashes at once with a short bounded wait.
const (
	DefaultMemoryKiB     = 64 * 1024
	DefaultIterations    = 3
	DefaultParallelism   = 1
	DefaultSaltBytes     = 16
	DefaultKeyBytes      = 32
	DefaultMaxConcurrent = 2
	DefaultWaitTimeout   = 2 * time.Second
)

// ErrBusy is returned when no hashing slot became free within the wait limit or
// the request ended first. The caller answers UNAVAILABLE: nothing was decided
// about the password.
var ErrBusy = errors.New("passwordhash: too many concurrent hashes")

// ErrInvalidHash is returned for a stored value that is not a supported hash.
var ErrInvalidHash = errors.New("passwordhash: unsupported hash encoding")

// Config sets the cost parameters and the concurrency budget.
type Config struct {
	MemoryKiB     uint32
	Iterations    uint32
	Parallelism   uint8
	SaltBytes     int
	KeyBytes      uint32
	MaxConcurrent int
	// WaitTimeout bounds the wait for a slot; the request context bounds it too.
	WaitTimeout time.Duration
}

// DefaultConfig returns the specified defaults.
func DefaultConfig() Config {
	return Config{
		MemoryKiB: DefaultMemoryKiB, Iterations: DefaultIterations, Parallelism: DefaultParallelism,
		SaltBytes: DefaultSaltBytes, KeyBytes: DefaultKeyBytes,
		MaxConcurrent: DefaultMaxConcurrent, WaitTimeout: DefaultWaitTimeout,
	}
}

// Hasher computes and verifies hashes under one Config.
type Hasher struct {
	cfg   Config
	slots chan struct{}
	dummy string
}

// New validates cfg and prepares the hasher, including the equal-cost dummy
// hash that unknown addresses are checked against.
func New(cfg Config) (*Hasher, error) {
	if cfg.SaltBytes == 0 {
		cfg.SaltBytes = DefaultSaltBytes
	}
	if cfg.KeyBytes == 0 {
		cfg.KeyBytes = DefaultKeyBytes
	}
	if cfg.MaxConcurrent == 0 {
		cfg.MaxConcurrent = DefaultMaxConcurrent
	}
	if cfg.WaitTimeout == 0 {
		cfg.WaitTimeout = DefaultWaitTimeout
	}
	switch {
	case cfg.MemoryKiB < 8 || cfg.MemoryKiB > maxMemoryKiB:
		return nil, errors.New("passwordhash: memory out of range")
	case cfg.Iterations < 1 || cfg.Iterations > maxIterations:
		return nil, errors.New("passwordhash: iterations out of range")
	case cfg.Parallelism < 1 || cfg.Parallelism > maxParallelism:
		return nil, errors.New("passwordhash: parallelism out of range")
	case cfg.SaltBytes < 16 || cfg.SaltBytes > maxSaltBytes:
		return nil, errors.New("passwordhash: salt must be 16-64 bytes")
	case cfg.KeyBytes < 16 || cfg.KeyBytes > maxKeyBytes:
		return nil, errors.New("passwordhash: key length must be 16-64 bytes")
	case cfg.MaxConcurrent < 1 || cfg.MaxConcurrent > 64:
		return nil, errors.New("passwordhash: max concurrent out of range")
	case cfg.WaitTimeout < 0:
		return nil, errors.New("passwordhash: negative wait timeout")
	}
	h := &Hasher{cfg: cfg, slots: make(chan struct{}, cfg.MaxConcurrent)}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	dummy, err := h.compute(base64.RawStdEncoding.EncodeToString(random))
	if err != nil {
		return nil, err
	}
	h.dummy = dummy
	return h, nil
}

// Upper bounds on what a stored hash may ask a verifier to spend.
const (
	maxMemoryKiB   = 1 << 20 // 1 GiB
	maxIterations  = 20
	maxParallelism = 16
	maxSaltBytes   = 64
	maxKeyBytes    = 64
)

func (h *Hasher) acquire(ctx context.Context) error {
	timer := time.NewTimer(h.cfg.WaitTimeout)
	defer timer.Stop()
	select {
	case h.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ErrBusy
	case <-timer.C:
		return ErrBusy
	}
}

func (h *Hasher) release() { <-h.slots }

// compute hashes without taking a slot; callers hold one.
func (h *Hasher) compute(password string) (string, error) {
	salt := make([]byte, h.cfg.SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, h.cfg.Iterations, h.cfg.MemoryKiB, h.cfg.Parallelism, h.cfg.KeyBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, h.cfg.MemoryKiB, h.cfg.Iterations, h.cfg.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// Hash returns the encoded Argon2id hash of password with a fresh salt. The
// caller has already applied the password policy; Hash only guards against
// absurd input.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	if len(password) > 1024 {
		return "", errors.New("passwordhash: password too long")
	}
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	return h.compute(password)
}

// Verify reports whether password matches the encoded hash, in constant time
// for the comparison itself. A malformed or unsupported encoding returns
// ErrInvalidHash and burns no slot.
func (h *Hasher) Verify(ctx context.Context, encoded, password string) (bool, error) {
	if len(password) > 1024 {
		return false, nil
	}
	p, err := parse(encoded)
	if err != nil {
		return false, err
	}
	if err := h.acquire(ctx); err != nil {
		return false, err
	}
	defer h.release()
	got := argon2.IDKey([]byte(password), p.salt, p.iterations, p.memory, p.parallelism, uint32(len(p.key)))
	return subtle.ConstantTimeCompare(got, p.key) == 1, nil
}

// VerifyDummy spends the cost of one verification against a throwaway hash. It
// stands in for the check of an address that has no account, so an unknown
// address does not get an obviously faster path. It cannot hide the response
// time completely and is not claimed to.
func (h *Hasher) VerifyDummy(ctx context.Context, password string) error {
	_, err := h.Verify(ctx, h.dummy, password)
	return err
}

type params struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	salt, key   []byte
}

func parse(encoded string) (params, error) {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, key]
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return params{}, ErrInvalidHash
	}
	var p params
	fields := strings.Split(parts[3], ",")
	if len(fields) != 3 {
		return params{}, ErrInvalidHash
	}
	for i, prefix := range []string{"m=", "t=", "p="} {
		v, ok := strings.CutPrefix(fields[i], prefix)
		if !ok {
			return params{}, ErrInvalidHash
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return params{}, ErrInvalidHash
		}
		switch i {
		case 0:
			p.memory = uint32(n)
		case 1:
			p.iterations = uint32(n)
		case 2:
			if n > 255 {
				return params{}, ErrInvalidHash
			}
			p.parallelism = uint8(n)
		}
	}
	var err error
	if p.salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return params{}, ErrInvalidHash
	}
	if p.key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil {
		return params{}, ErrInvalidHash
	}
	if p.memory < 8 || p.memory > maxMemoryKiB || p.iterations < 1 || p.iterations > maxIterations ||
		p.parallelism < 1 || p.parallelism > maxParallelism ||
		len(p.salt) < 8 || len(p.salt) > maxSaltBytes || len(p.key) < 16 || len(p.key) > maxKeyBytes {
		return params{}, ErrInvalidHash
	}
	return p, nil
}
