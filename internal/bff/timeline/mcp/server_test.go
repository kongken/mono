package mcp

import (
	"context"
	"github.com/kongken/mono/internal/bff/accesstoken"
	"github.com/kongken/mono/internal/bff/usersrv"
	"testing"
)

type accountState struct {
	state usersrv.AuthState
	err   error
}

func (a accountState) AuthState(context.Context, string) (usersrv.AuthState, error) {
	return a.state, a.err
}

func TestToolsUseTimelineScopesAndExpectedRevision(t *testing.T) {
	ts := tools()
	if len(ts) != 12 {
		t.Fatalf("tools=%d", len(ts))
	}
	for _, tool := range ts {
		p := accesstoken.Principal{Scopes: []string{accesstoken.ScopeTimelineRead}}
		if tool.scope == accesstoken.ScopeTimelineWrite {
			if tool.allowedFor(p) {
				t.Errorf("write tool %s visible to read token", tool.name)
			}
		} else if !tool.allowedFor(p) {
			t.Errorf("read tool %s hidden from read token", tool.name)
		}
	}
	for _, name := range []string{"timeline_update", "timeline_trash", "timeline_restore"} {
		for _, tool := range ts {
			if tool.name == name && !tool.requireRevision {
				t.Errorf("%s does not require revision", name)
			}
		}
	}
}

func TestEffectiveRolesAcceptsActiveTimelineAccountAndRejectsInactiveAccount(t *testing.T) {
	s := &Server{cfg: Config{Accounts: accountState{state: usersrv.AuthState{Status: usersrv.StatusActive, AuthVersion: 3}}}}
	p := accesstoken.Principal{UserID: "u", AuthVersion: 3}
	if roles, err := s.effectiveRoles(context.Background(), p); err != nil || len(roles) != 0 {
		t.Fatalf("active account rejected: roles=%v err=%v", roles, err)
	}
	s.cfg.Accounts = accountState{state: usersrv.AuthState{Status: usersrv.StatusDisabled, AuthVersion: 3}}
	if _, err := s.effectiveRoles(context.Background(), p); err == nil || err.Code != "UNAUTHENTICATED" {
		t.Fatalf("inactive account result=%v", err)
	}
}
