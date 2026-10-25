package tripmap

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/bff/usersrv"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"github.com/redis/go-redis/v9"
)

// procedurePrefix selects the procedures that need tripmap's projection of the
// account.
const procedurePrefix = "/bff.tripmap.v1."

// projectionTTL bounds how long "already projected" is trusted, so a tripmap
// database that was restored or reset is repopulated without waiting for the
// next profile change.
const projectionTTL = 24 * time.Hour

// Accounts reads the profile of the signed-in account from usersrv.
type Accounts interface {
	Account(ctx context.Context, userID string) (usersrv.Account, error)
}

// Projector keeps tripmap's display projection of an account (ID, nickname and
// avatar, nothing else) up to date on demand. Before a signed-in tripmap request it
// compares the account's profile_version, which the ingress learned from usersrv a
// moment ago, with the last version it projected; only when they differ does it read
// the profile and write it to tripmap. The version cache lives in Redis and is only
// an optimisation: when it cannot be read or written the projection is simply ensured
// again, and the request is not refused for it.
type Projector struct {
	rdb      redis.Cmdable
	prefix   string
	accounts Accounts
	profiles tripmapv1.ProfileServiceClient
}

func NewProjector(rdb redis.Cmdable, keyPrefix string, accounts Accounts, profiles tripmapv1.ProfileServiceClient) *Projector {
	return &Projector{rdb: rdb, prefix: keyPrefix, accounts: accounts, profiles: profiles}
}

func (p *Projector) key(userID string) string { return p.prefix + "proj:tripmap:" + userID }

// Ensure is an ingress.Policy.Projection hook. It acts only on tripmap procedures.
func (p *Projector) Ensure(ctx context.Context, procedure string, bs session.BrowserSession) error {
	if !strings.HasPrefix(procedure, procedurePrefix) {
		return nil
	}
	version := strconv.FormatInt(bs.ProfileVersion, 10)
	if p.rdb != nil {
		if got, err := p.rdb.Get(ctx, p.key(bs.UserID)).Result(); err == nil && got == version {
			return nil
		} else if err != nil && !errors.Is(err, redis.Nil) {
			slog.Warn("projection cache read", "error", err)
		}
	}
	acct, err := p.accounts.Account(ctx, bs.UserID)
	if err != nil {
		if errors.Is(err, usersrv.ErrNotFound) {
			return connect.NewError(connect.CodeUnauthenticated, errors.New("sign in required"))
		}
		return err
	}
	if _, err = p.profiles.EnsureProfile(ctx, &tripmapv1.EnsureProfileRequest{
		UserId: bs.UserID, DisplayName: acct.Profile.DisplayName, AvatarUrl: acct.Profile.AvatarURL,
	}); err != nil {
		slog.Error("ensure tripmap profile", "error", err)
		return connect.NewError(connect.CodeUnavailable, errors.New("service temporarily unavailable"))
	}
	if p.rdb != nil {
		// The version just projected is the profile's own; a change since the
		// request's check only makes the next request project again.
		if err := p.rdb.Set(ctx, p.key(bs.UserID), strconv.FormatInt(acct.Profile.ProfileVersion, 10), projectionTTL).Err(); err != nil {
			slog.Warn("projection cache write", "error", err)
		}
	}
	return nil
}
