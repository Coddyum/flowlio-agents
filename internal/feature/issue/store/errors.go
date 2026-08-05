package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément   | Résumé                                                             | Ligne |
// |-----------|--------------------------------------------------------------------|-------|
// | translate | Brings a Postgres error back to a store domain error                | 37    |
// | isState   | Tells whether a string is a known issue state                       | 64    |
//
// Fin du sommaire.
// =====================================================================

import (
	"database/sql"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	codeUniqueViolation     = "23505"
	codeCheckViolation      = "23514"
	codeForeignKeyViolation = "23503"

	// constraintIssueNumber protects the (project_id, number) uniqueness. Violating it does not
	// mean "caller conflict" but "corrupted project counter": see translate.
	constraintIssueNumber = "issues_number_unique_per_project"
)

// translate brings database errors back to the store's domain errors.
//
// An issues_number_unique_per_project violation is handled apart: it means the project counter
// served the same number twice, hence that the data is inconsistent. Returning it as a "conflict"
// would answer 409 to an agent that did nothing wrong and would have it retry forever; this is a
// server failure and must show as one.
func translate(err error, op string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.ConstraintName == constraintIssueNumber {
			return errors.Join(ErrCorrupted, errors.New("issue store: "+op), err)
		}
		switch pgErr.Code {
		case codeUniqueViolation, codeCheckViolation, codeForeignKeyViolation:
			return ErrConflict
		}
	}

	return errors.Join(errors.New("issue store: "+op), err)
}

// states is the vocabulary of issue states, identical to the migration's issue_state enum.
var states = []string{"open", "answered", "closed"}

// isState tells whether a string is a known state. The store checks it before sending the value to
// the database: an unknown state would produce a Postgres cast error, not an empty filter.
func isState(state string) bool {
	return slices.Contains(states, state)
}
