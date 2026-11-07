package accountcleanup

import (
	"context"
	"errors"
	"testing"
)

func TestReplayMediaTombstoneRejectsUnscopedRequests(t *testing.T) {
	for name, args := range map[string]struct{ user, mutation, media string }{
		"missing owner":     {mutation: "cleanup:media:m1", media: "med_ref"},
		"missing mutation":  {user: "user-1", media: "med_ref"},
		"missing media ref": {user: "user-1", mutation: "cleanup:media:m1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ReplayMediaTombstone(context.Background(), nil, args.user, args.mutation, args.media); !errors.Is(err, ErrTombstoneNotFound) {
				t.Fatalf("error = %v, want ErrTombstoneNotFound", err)
			}
		})
	}
}
