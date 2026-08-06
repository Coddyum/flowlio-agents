package store

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// Postgres SQLSTATE codes. A unique violation here means the slug is already taken in this
	// project; a check violation means the slug, title or body broke one of the table's shapes.
	codeUniqueViolation = "23505"
	codeCheckViolation  = "23514"
)

// translate brings database errors back to the store's domain errors, so the service reasons about
// business cases and never about SQLSTATE codes.
//
// A missing row is ErrNotFound whether the slug does not exist or belongs to another project: the
// two must stay indistinguishable, or a sibling's registry could be probed one slug at a time.
func translate(err error, op string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case codeUniqueViolation, codeCheckViolation:
			return ErrConflict
		}
	}

	return errors.Join(errors.New("memory store: "+op), err)
}
