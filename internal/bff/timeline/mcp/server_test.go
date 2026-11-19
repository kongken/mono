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
	var download, rawBytes *tool
	for _, tool := range ts {
		switch tool.name {
		case "timeline_get_attachment_download":
			download = tool
		case "timeline_read_attachment":
			rawBytes = tool
		}
	}
	// #110: MCP returns attachment metadata and a short-lived download address,
	// never raw bytes and never a permanent public URL.
	if download == nil {
		t.Fatal("timeline_get_attachment_download tool is missing")
	}
	if download.mediaCall == nil || download.readOnly == false {
		t.Fatalf("download tool must be a read-only media call: %+v", download)
	}
	if rawBytes != nil {
		t.Fatal("MCP must not expose a raw attachment byte tool")
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
