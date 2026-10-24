// Command usersrv-admin is the operator tool for account roles and status. It is
// the only way to grant or revoke metro-admin and to disable or enable an
// account: there is no browser or RPC path to these operations, and no user
// becomes an administrator because of who registered first.
//
//	usersrv-admin [-config config.usersrv.yaml] <command> [flags]
//
//	grant-role   -email E|-user-id U -role metro-admin -operator NAME -reason TEXT
//	revoke-role  -email E|-user-id U -role metro-admin -operator NAME -reason TEXT
//	disable      -email E|-user-id U -operator NAME -reason TEXT
//	enable       -email E|-user-id U -operator NAME -reason TEXT
//	show         -email E|-user-id U
//
// The database comes from the USERSRV_DATABASE_URL environment variable (a
// PostgreSQL URL; preferred, because command lines are visible to other
// users), or from -config, which reads the store.db entry named by db_store in
// the service's YAML file. Every attempt, including refused ones, is recorded
// in security_audit with the operator, the reason and the result. Operators
// must not put an email address in -reason. Every change of state bumps auth_version, so
// the account's older sessions and management tokens fail their next check.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/identity"
	"gopkg.in/yaml.v3"
)

func main() {
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv)
	stop()
	os.Exit(code)
}

const usage = `usage: usersrv-admin [-config file] <command> [flags]

commands:
  grant-role   -email E|-user-id U -role metro-admin -operator NAME -reason TEXT
  revoke-role  -email E|-user-id U -role metro-admin -operator NAME -reason TEXT
  disable      -email E|-user-id U -operator NAME -reason TEXT
  enable       -email E|-user-id U -operator NAME -reason TEXT
  show         -email E|-user-id U

database: USERSRV_DATABASE_URL, or -config with the service YAML (store.db + db_store)
`

// run executes one command and returns the exit code: 0 done (including "already
// in that state"), 1 refused or failed, 2 usage error.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	global := flag.NewFlagSet("usersrv-admin", flag.ContinueOnError)
	global.SetOutput(stderr)
	configFile := global.String("config", "", "service YAML file with store.db and db_store")
	if err := global.Parse(args); err != nil {
		return 2
	}
	rest := global.Args()
	if len(rest) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	command, rest := rest[0], rest[1:]
	switch command {
	case "grant-role", "revoke-role", "disable", "enable", "show":
	default:
		fmt.Fprintf(stderr, "unknown command %q\n%s", command, usage)
		return 2
	}

	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	email := fs.String("email", "", "login email of the account")
	userID := fs.String("user-id", "", "user UUID of the account")
	role := fs.String("role", "", "role name (metro-admin)")
	operator := fs.String("operator", "", "who runs this change (name or ticket)")
	reason := fs.String("reason", "", "why (no email addresses)")
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if (*email == "") == (*userID == "") {
		fmt.Fprintln(stderr, "give exactly one of -email or -user-id")
		return 2
	}
	needsRole := command == "grant-role" || command == "revoke-role"
	if needsRole && *role == "" {
		fmt.Fprintln(stderr, "-role is required")
		return 2
	}
	if command != "show" && (*operator == "" || *reason == "") {
		fmt.Fprintln(stderr, "-operator and -reason are required for changes")
		return 2
	}

	dsn, err := databaseURL(*configFile, getenv)
	if err != nil {
		fmt.Fprintln(stderr, "database:", err)
		return 2
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		fmt.Fprintln(stderr, "database:", redact(err))
		return 1
	}
	defer sqlDB.Close()
	db := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, sqlDB)))
	defer db.Close()
	// The admin operations never hash a password, so no hasher is needed.
	svc := account.NewService(db, nil)

	id, err := resolve(ctx, svc, *email, *userID)
	if err != nil {
		return fail(stderr, command, err)
	}
	op := account.Operator{Name: *operator, Reason: *reason, RequestID: "cli-" + rpcauth.NewRequestID()[:16]}
	var res account.ChangeResult
	switch command {
	case "show":
		st, err := svc.GetAuthState(ctx, id)
		if err != nil {
			return fail(stderr, command, err)
		}
		fmt.Fprintf(stdout, "user_id=%s status=%s auth_version=%d roles=%v\n", id, st.Status, st.AuthVersion, st.Roles)
		return 0
	case "grant-role":
		res, err = svc.GrantRole(ctx, op, id, *role)
	case "revoke-role":
		res, err = svc.RevokeRole(ctx, op, id, *role)
	case "disable":
		res, err = svc.SetDisabled(ctx, op, id, true)
	case "enable":
		res, err = svc.SetDisabled(ctx, op, id, false)
	}
	if err != nil {
		return fail(stderr, command, err)
	}
	fmt.Fprintf(stdout, "%s user_id=%s %s request_id=%s\n", command, id, res.Describe(), op.RequestID)
	return 0
}

func resolve(ctx context.Context, svc *account.Service, email, userID string) (uuid.UUID, error) {
	if userID != "" {
		id, err := uuid.Parse(userID)
		if err != nil || id == uuid.Nil {
			return uuid.Nil, &identity.ValidationError{Field: "user-id", Reason: "not a UUID"}
		}
		return id, nil
	}
	return svc.ResolveEmail(ctx, email)
}

// fail prints a short reason without echoing the email or any database detail.
func fail(stderr io.Writer, command string, err error) int {
	switch {
	case errors.Is(err, account.ErrNotFound):
		fmt.Fprintf(stderr, "%s: no such account\n", command)
	case errors.Is(err, account.ErrNotActive):
		fmt.Fprintf(stderr, "%s: refused, the account is not active (disabled or being deleted)\n", command)
	case account.IsUnavailable(err):
		fmt.Fprintf(stderr, "%s: the database is unavailable\n", command)
	default:
		if v, ok := identity.IsValidation(err); ok {
			fmt.Fprintf(stderr, "%s: %s\n", command, v)
			return 1
		}
		fmt.Fprintf(stderr, "%s: failed: %v\n", command, redact(err))
	}
	return 1
}

// redact keeps connection strings (and so passwords) out of error text.
func redact(err error) string {
	if err == nil {
		return ""
	}
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return uerr.Err.Error()
	}
	return err.Error()
}

// databaseURL returns the DSN from USERSRV_DATABASE_URL or from the service YAML.
func databaseURL(configFile string, getenv func(string) string) (string, error) {
	if v := getenv("USERSRV_DATABASE_URL"); v != "" {
		if configFile != "" {
			return "", errors.New("set USERSRV_DATABASE_URL or -config, not both")
		}
		return v, nil
	}
	if configFile == "" {
		return "", errors.New("set USERSRV_DATABASE_URL or pass -config")
	}
	raw, err := os.ReadFile(configFile)
	if err != nil {
		return "", err
	}
	var cfg struct {
		DBStore string `yaml:"db_store"`
		Store   struct {
			DB map[string]struct {
				Driver   string `yaml:"driver"`
				Host     string `yaml:"host"`
				Port     int    `yaml:"port"`
				User     string `yaml:"user"`
				Password string `yaml:"password"`
				DBName   string `yaml:"db_name"`
				SSLMode  string `yaml:"ssl_mode"`
			} `yaml:"db"`
		} `yaml:"store"`
	}
	if err = yaml.Unmarshal(raw, &cfg); err != nil {
		return "", errors.New("cannot parse the config file")
	}
	s, ok := cfg.Store.DB[cfg.DBStore]
	if !ok || cfg.DBStore == "" {
		return "", errors.New("db_store does not refer to a store.db entry")
	}
	if s.Driver != "" && s.Driver != "postgres" {
		return "", errors.New("the store.db entry must use PostgreSQL")
	}
	port := s.Port
	if port == 0 {
		port = 5432
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword(s.User, s.Password), Host: net.JoinHostPort(s.Host, strconv.Itoa(port)), Path: "/" + s.DBName}
	if s.SSLMode != "" {
		u.RawQuery = "sslmode=" + url.QueryEscape(s.SSLMode)
	}
	return u.String(), nil
}
