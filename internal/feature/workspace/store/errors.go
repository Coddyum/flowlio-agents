package store

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// codeUniqueViolation and codeCheckViolation: Postgres SQLSTATE codes.
	codeUniqueViolation = "23505"
	codeCheckViolation  = "23514"
)

// translate brings database errors back to ErrNotFound / ErrConflict, so that the service reasons
// about business cases and not about SQLSTATE codes.
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

	return errors.Join(errors.New("workspace store: "+op), err)
}
