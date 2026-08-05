package store

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// codeUniqueViolation, codeCheckViolation and codeForeignKeyViolation: Postgres SQLSTATE codes.
	// The foreign key violation happens when the target project does not exist in the team — that
	// is, an invalid scope, not an internal error.
	codeUniqueViolation     = "23505"
	codeCheckViolation      = "23514"
	codeForeignKeyViolation = "23503"

	// constraintTaskNumber protects the (project_id, number) uniqueness. Violating it does not mean
	// the caller called wrong: the number is drawn from the project counter, never supplied.
	constraintTaskNumber = "tasks_number_unique_per_project"
)

// translate brings database errors back to the store's domain errors, so that the service reasons
// about business cases and not about SQLSTATE codes.
//
// A tasks_number_unique_per_project violation is handled apart: it means the project counter
// served the same number twice, hence that the data is inconsistent. Returning it as a "conflict"
// would answer 409 to an agent that did nothing wrong and would have it retry forever; this is a
// server failure and must show as one. Exact mirror of issue/store/errors.go, which already
// carried that distinction.
func translate(err error, op string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.ConstraintName == constraintTaskNumber {
			return errors.Join(ErrCorrupted, errors.New("task store: "+op), err)
		}
		switch pgErr.Code {
		case codeUniqueViolation, codeCheckViolation, codeForeignKeyViolation:
			return ErrConflict
		}
	}

	return errors.Join(errors.New("task store: "+op), err)
}
