package database

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL integrity-violation SQLSTATE codes.
//
// Repositories translate these into their own domain errors so that no caller
// outside this package has to import pgconn or know a SQLSTATE. Matching on the
// code and the constraint name (rather than on the error text) keeps the
// translation stable across driver and server versions.
const (
	codeUniqueViolation     = "23505"
	codeForeignKeyViolation = "23503"
	codeCheckViolation      = "23514"
	codeNotNullViolation    = "23502"
)

// IsUniqueViolation reports whether err is a unique-constraint violation.
//
// When constraint is non-empty the violated constraint must also match, which
// is what lets a repository tell "this email is taken" apart from "this user
// already has a WhatsApp number". PostgreSQL reports the index name for a
// unique index, so `users_email_unique` is a valid constraint argument even
// though it is declared as CREATE UNIQUE INDEX rather than as a table
// constraint.
func IsUniqueViolation(err error, constraint string) bool {
	return isViolation(err, codeUniqueViolation, constraint)
}

// IsForeignKeyViolation reports whether err is a foreign-key violation, for
// example inserting a row for a user id that no longer exists.
func IsForeignKeyViolation(err error, constraint string) bool {
	return isViolation(err, codeForeignKeyViolation, constraint)
}

// IsCheckViolation reports whether err is a CHECK-constraint violation.
//
// A CHECK violation normally means application-side validation was skipped or
// disagrees with the schema, so callers usually surface it as an internal error
// rather than as a user-facing message.
func IsCheckViolation(err error, constraint string) bool {
	return isViolation(err, codeCheckViolation, constraint)
}

// IsNotNullViolation reports whether err is a NOT NULL violation. When
// constraint is non-empty it is matched against the offending column name.
func IsNotNullViolation(err error, column string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != codeNotNullViolation {
		return false
	}
	return column == "" || pgErr.ColumnName == column
}

func isViolation(err error, code, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}
