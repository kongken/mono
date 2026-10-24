// Package ent contains the BFF PostgreSQL client. Edit schema/ and run make ent-bff.
package ent

//go:generate go tool ent generate --feature sql/upsert,sql/lock,sql/execquery ./schema
