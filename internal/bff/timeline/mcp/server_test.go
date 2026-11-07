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
	if len(ts) != 7 {
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
	for _, name := range []string{"timeline_update", "timeline_trash", "timeline_restore", "timeline_purge"} {
		for _, tool := range ts {
			if tool.name == name && !tool.requireRevision {
				t.Errorf("%s does not require revision", name)
			}
		}
	}
}

func TestEffectiveRolesAcceptsFormalRoleAndRejectsRevokedRole(t *testing.T) {
	s := &Server{cfg: Config{Accounts: accountState{state: usersrv.AuthState{Status: usersrv.StatusActive, AuthVersion: 3, Roles: []string{"metro-admin"}}}}}
	p := accesstoken.Principal{UserID: "u", AuthVersion: 3, Roles: []string{"metro-admin"}}
	if _, err := s.effectiveRoles(context.Background(), p); err != nil {
		t.Fatalf("formal role rejected: %v", err)
	}
	s.cfg.Accounts = accountState{state: usersrv.AuthState{Status: usersrv.StatusActive, AuthVersion: 3, Roles: nil}}
	if _, err := s.effectiveRoles(context.Background(), p); err == nil || err.Code != "PERMISSION_DENIED" {
		t.Fatalf("revoked role result=%v", err)
	}
}
