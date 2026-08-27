// Package migrations embeds the SQL migration files so that the binary is
// self-contained: no separate migration CLI has to be installed to bring a
// database up to date (see backend/README.md "Database migrations").
package migrations

import "embed"

// FS holds every *.sql migration in this directory, named
// "<version>_<name>.sql" (for example 001_initial_schema.sql).
//
//go:embed *.sql
var FS embed.FS
