package auth_test

import (
	"context"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/auth"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/bff/usersrv"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

// callCounter is a Users that records what reached "usersrv". Everything but the two mail
// requests is unused by these tests and panics if called.
type callCounter struct {
	auth.Users
	mu    sync.Mutex
	calls []usersrv.Return
}

func (c *callCounter) RequestRegistration(_ context.Context, _ string, ret usersrv.Return) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, ret)
	return nil
}

func (c *callCounter) RequestPasswordReset(ctx context.Context, email string, ret usersrv.Return) error {
	return c.RequestRegistration(ctx, email, ret)
}

func (c *callCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

type ask struct {
	name string
	do   func(*auth.Handler, string, string) error
}

var asks = []ask{
	{"registration", func(h *auth.Handler, app, path string) error {
		_, err := h.RequestRegistration(context.Background(), connect.NewRequest(&bffv1.RequestRegistrationRequest{Email: "a@example.com", AppId: app, ReturnPath: path}))
		return err
	}},
	{"reset", func(h *auth.Handler, app, path string) error {
		_, err := h.RequestPasswordReset(context.Background(), connect.NewRequest(&bffv1.RequestPasswordResetRequest{Email: "a@example.com", AppId: app, ReturnPath: path}))
		return err
	}},
}

// Without a navigation registry nothing can vouch for a return context, so a request that
// carries one is refused before usersrv is called (no challenge, no mail can follow), and
// one without a context is served as usual.
func TestMailRequestWithContextButNoRegistryIsRefusedBeforeUsersrv(t *testing.T) {
	for _, a := range asks {
		for _, tc := range []struct{ app, path string }{
			{"metro", ""},
			{"metro", "/city/shanghai"},
			{"", "/city/shanghai"},
			{"unknown", "/"},
		} {
			users := &callCounter{}
			h := auth.NewHandler(auth.NewService(nil, session.CookieConfig{}, nil, nil, users, nil), 0)
			err := a.do(h, tc.app, tc.path)
			if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Errorf("%s %q %q: %v", a.name, tc.app, tc.path, err)
			}
			if users.count() != 0 {
				t.Errorf("%s %q %q: usersrv was called %d times", a.name, tc.app, tc.path, users.count())
			}
		}
		users := &callCounter{}
		h := auth.NewHandler(auth.NewService(nil, session.CookieConfig{}, nil, nil, users, nil), 0)
		if err := a.do(h, "", ""); err != nil {
			t.Errorf("%s without context: %v", a.name, err)
		}
		if users.count() != 1 || users.calls[0] != (usersrv.Return{}) {
			t.Errorf("%s without context: calls %v", a.name, users.calls)
		}
	}
}

// With a registry, a refused context is refused before usersrv too, and an allowed one is
// handed on in canonical form.
func TestMailRequestRegistryRefusalNeverReachesUsersrv(t *testing.T) {
	for _, a := range asks {
		users := &callCounter{}
		h := auth.NewHandler(auth.NewService(nil, session.CookieConfig{}, nil, nil, users, nil), 0).WithNavigation(registry(t))
		for _, bad := range []struct{ app, path string }{{"unknown", "/"}, {"metro", "/admin"}, {"metro", "//evil.example"}, {"", "/me"}} {
			if err := a.do(h, bad.app, bad.path); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Errorf("%s %v: %v", a.name, bad, err)
			}
		}
		if users.count() != 0 {
			t.Errorf("%s: a refused context reached usersrv", a.name)
		}
		if err := a.do(h, "metro", "/city/shanghai"); err != nil {
			t.Fatal(err)
		}
		if users.count() != 1 || users.calls[0] != (usersrv.Return{AppID: "metro", Path: "/city/shanghai"}) {
			t.Errorf("%s: calls %v", a.name, users.calls)
		}
	}
}
