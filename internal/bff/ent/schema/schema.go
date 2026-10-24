// Package schema holds the BFF's own tables: browser sessions, management tokens
// and account deletion workflows. Accounts, passwords and roles belong to usersrv;
// nothing here references a user table, and user IDs are plain strings.
package schema

import "time"

func now() time.Time { return time.Now().UTC() }
