package store

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// codeUniqueViolation et codeCheckViolation : codes SQLSTATE de Postgres.
	codeUniqueViolation = "23505"
	codeCheckViolation  = "23514"
)

// translate ramène les erreurs de la base à ErrNotFound / ErrConflict, pour que le service
// raisonne sur des cas métier et pas sur des codes SQLSTATE.
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
