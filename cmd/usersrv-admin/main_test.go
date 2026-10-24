package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"database/sql"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/securityaudit"
	"github.com/kongken/mono/internal/usersrv/testutil"
)

type cli struct {
	t   *testing.T
	dsn string
	db  *ent.Client
}

func newCLI(t *testing.T) *cli {
	t.Helper()
	dsn := testutil.SchemaDSN(t)
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return &cli{t: t, dsn: dsn, db: ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, sqlDB)))}
}

func (c *cli) run(args ...string) (int, string, string) {
	c.t.Helper()
	var out, errb bytes.Buffer
	env := map[string]string{"USERSRV_DATABASE_URL": c.dsn}
	code := run(context.Background(), args, &out, &errb, func(k string) string { return env[k] })
	return code, out.String(), errb.String()
}

func (c *cli) user(email string) uuid.UUID {
	c.t.Helper()
	svc := account.NewService(c.db, testutil.Hasher(c.t))
	id, err := svc.CreateVerifiedUser(context.Background(), account.NewUser{Email: email, Password: "correct horse battery"})
	if err != nil {
		c.t.Fatal(err)
	}
	return id
}

func TestGrantRevokeDisableEnableAndShow(t *testing.T) {
	c := newCLI(t)
	id := c.user("admin@example.com")
	common := []string{"-operator", "alice", "-reason", "ticket 7"}

	code, out, errOut := c.run(append([]string{"grant-role", "-email", " Admin@Example.com ", "-role", "metro-admin"}, common...)...)
	if code != 0 || !strings.Contains(out, "changed") || !strings.Contains(out, "auth_version=2") || !strings.Contains(out, id.String()) {
		t.Fatalf("grant: %d %q %q", code, out, errOut)
	}
	if strings.Contains(out+errOut, "admin@example.com") {
		t.Fatalf("the CLI echoes the email: %q %q", out, errOut)
	}
	code, out, _ = c.run("show", "-user-id", id.String())
	if code != 0 || !strings.Contains(out, "roles=[metro-admin]") || !strings.Contains(out, "status=active") {
		t.Fatalf("show: %d %q", code, out)
	}
	if code, out, _ = c.run(append([]string{"grant-role", "-user-id", id.String(), "-role", "metro-admin"}, common...)...); code != 0 || !strings.Contains(out, "unchanged") || !strings.Contains(out, "auth_version=2") {
		t.Fatalf("grant twice: %d %q", code, out)
	}
	if code, out, _ = c.run(append([]string{"disable", "-user-id", id.String()}, common...)...); code != 0 || !strings.Contains(out, "status=disabled") || !strings.Contains(out, "auth_version=3") {
		t.Fatalf("disable: %d %q", code, out)
	}
	// A disabled account cannot be granted a role, even one it already holds; revoking still works.
	if code, _, errOut = c.run(append([]string{"grant-role", "-user-id", id.String(), "-role", "metro-admin"}, common...)...); code != 1 || !strings.Contains(errOut, "not active") {
		t.Fatalf("grant on a disabled holder: %d %q", code, errOut)
	}
	other := c.user("other@example.com")
	if _, err := account.NewService(c.db, nil).SetDisabled(context.Background(), account.Operator{Name: "x", Reason: "y"}, other, true); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut = c.run(append([]string{"grant-role", "-user-id", other.String(), "-role", "metro-admin"}, common...)...); code != 1 || !strings.Contains(errOut, "not active") {
		t.Fatalf("grant on disabled: %d %q", code, errOut)
	}
	if code, out, _ = c.run(append([]string{"revoke-role", "-user-id", id.String(), "-role", "metro-admin"}, common...)...); code != 0 || !strings.Contains(out, "roles=[]") || !strings.Contains(out, "auth_version=4") {
		t.Fatalf("revoke: %d %q", code, out)
	}
	if code, out, _ = c.run(append([]string{"enable", "-user-id", id.String()}, common...)...); code != 0 || !strings.Contains(out, "status=active") || !strings.Contains(out, "auth_version=5") {
		t.Fatalf("enable: %d %q", code, out)
	}

	// Operator, reason and result are recorded for every attempt, refusals included.
	rows := c.db.SecurityAudit.Query().Where(securityaudit.ActorEQ("alice")).AllX(context.Background())
	results := map[string]int{}
	for _, r := range rows {
		if r.Reason != "ticket 7" || !strings.HasPrefix(r.RequestID, "cli-") {
			t.Errorf("audit row lost context: %+v", r)
		}
		results[r.Result]++
	}
	if results["ok"] != 4 || results["unchanged"] != 1 || results["refused_not_active"] != 2 {
		t.Fatalf("audit results: %v", results)
	}
}

func TestFailuresAndUsageErrors(t *testing.T) {
	c := newCLI(t)
	c.user("exists@example.com")
	common := []string{"-operator", "alice", "-reason", "r"}
	if code, _, errOut := c.run(append([]string{"grant-role", "-email", "nobody@example.com", "-role", "metro-admin"}, common...)...); code != 1 || !strings.Contains(errOut, "no such account") || strings.Contains(errOut, "nobody@") {
		t.Fatalf("unknown account: %d %q", code, errOut)
	}
	if code, _, errOut := c.run(append([]string{"grant-role", "-email", "exists@example.com", "-role", "superuser"}, common...)...); code != 1 || !strings.Contains(errOut, "role") {
		t.Fatalf("unknown role: %d %q", code, errOut)
	}
	if code, _, errOut := c.run("grant-role", "-email", "exists@example.com", "-role", "metro-admin", "-operator", "has space", "-reason", "r"); code != 1 || !strings.Contains(errOut, "operator") {
		t.Fatalf("bad operator: %d %q", code, errOut)
	}
	if code, _, errOut := c.run("grant-role", "-user-id", "nope", "-role", "metro-admin", "-operator", "a", "-reason", "r"); code != 1 || !strings.Contains(errOut, "user-id") {
		t.Fatalf("bad user id: %d %q", code, errOut)
	}
	for name, args := range map[string][]string{
		"no command":      {},
		"unknown command": {"promote", "-email", "a@b.co"},
		"no target":       {"show"},
		"both targets":    {"show", "-email", "a@b.co", "-user-id", uuid.NewString()},
		"no role":         {"grant-role", "-email", "a@b.co", "-operator", "a", "-reason", "r"},
		"no operator":     {"disable", "-email", "a@b.co", "-reason", "r"},
		"no reason":       {"disable", "-email", "a@b.co", "-operator", "a"},
		"unknown flag":    {"show", "-email", "a@b.co", "-x"},
	} {
		if code, _, _ := c.run(args...); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
	// None of the failed attempts changed anything.
	if n, _ := c.db.UserRole.Query().Count(context.Background()); n != 0 {
		t.Fatalf("%d roles after failures", n)
	}
}

func TestDatabaseSourcesAreExclusiveAndExplicit(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if _, err := databaseURL("", env(nil)); err == nil {
		t.Fatal("no database source accepted")
	}
	if got, err := databaseURL("", env(map[string]string{"USERSRV_DATABASE_URL": "postgres://x/y"})); err != nil || got != "postgres://x/y" {
		t.Fatalf("env: %q %v", got, err)
	}
	if _, err := databaseURL("file.yaml", env(map[string]string{"USERSRV_DATABASE_URL": "postgres://x/y"})); err == nil {
		t.Fatal("both sources accepted")
	}
	file := filepath.Join(t.TempDir(), "usersrv.yaml")
	if err := os.WriteFile(file, []byte("db_store: main\nstore:\n  db:\n    main:\n      driver: postgres\n      host: db.internal\n      port: 6543\n      user: u\n      password: 'p@ss/word'\n      db_name: usersrv\n      ssl_mode: require\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := databaseURL(file, env(nil))
	if err != nil || got != "postgres://u:p%40ss%2Fword@db.internal:6543/usersrv?sslmode=require" {
		t.Fatalf("yaml: %q %v", got, err)
	}
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	_ = os.WriteFile(bad, []byte("db_store: missing\nstore:\n  db: {}\n"), 0o600)
	if _, err = databaseURL(bad, env(nil)); err == nil {
		t.Fatal("unknown db_store accepted")
	}
	example, err := databaseURL("../../config.usersrv.example.yaml", env(nil))
	if err != nil || !strings.Contains(example, "/usersrv") {
		t.Fatalf("example config: %q %v", example, err)
	}
}
