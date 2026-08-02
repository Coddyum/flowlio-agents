package store

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// codeUniqueViolation, codeCheckViolation et codeForeignKeyViolation : codes SQLSTATE de
	// Postgres. La violation de clé étrangère survient quand le projet visé n'existe pas dans la
	// team — c'est-à-dire un scope invalide, pas une erreur interne.
	codeUniqueViolation     = "23505"
	codeCheckViolation      = "23514"
	codeForeignKeyViolation = "23503"
)

// translate ramène les erreurs de la base aux erreurs domaine du store, pour que le service
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
		case codeUniqueViolation, codeCheckViolation, codeForeignKeyViolation:
			return ErrConflict
		}
	}

	return errors.Join(errors.New("task store: "+op), err)
}
