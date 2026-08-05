package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément      | Résumé                                                          | Ligne |
// |--------------|-----------------------------------------------------------------|-------|
// | requireTeam  | Rejects an empty scope before any access to the store             | 53    |
// | requireRef   | Rejects a malformed reference before any access to the store      | 65    |
// | isProjectKey | Says whether a string has the shape of a project key              | 79    |
//
// Fin du sommaire.
// =====================================================================
//
// THE BOUNDS ARE SERVICE CONSTANTS, NEVER PARAMETERS. A `?limit=` would make the cost of the
// request depend on the caller on a surface that reads a whole team; and `truncated` would become
// a number the client inflicted on itself, hence a piece of information with no value.
//
// `projects[]` is never bounded, whatever the constant: a repo disappearing from the supervisor's
// screen is the one unrecoverable flaw of this surface — they cannot look for what they cannot
// see.

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// maxDebts bounds the debt queue. Fifty lines fit in a terminal screen scrolled once; past that
// it is no longer a queue, it is a report.
const maxDebts = 50

// maxMessages bounds an issue thread. Two hundred messages on a single question is not a nominal
// case: it is the sign the conversation should have become a task.
const maxMessages = 200

// maxNotes bounds a task's notes. Fifty notes is already several days of documented work.
const maxNotes = 50

// staleAfter is the age past which an `in_progress` task is deemed dormant.
//
// Twenty-four hours and not less: an agent can be restarted the next morning without the session
// being dead. The threshold lives HERE and not in the query — the clock belongs to the service,
// the scope to the query, and the integration test becomes deterministic.
const staleAfter = 24 * time.Hour

// requireTeam rejects an empty scope BEFORE any access to the store.
//
// Defence in depth: the handler already resolves the team, and a uuid.Nil would match no row. But
// a scope worth "zero" must not reach a layer every query of which draws its safety from that
// parameter — the day a query changes, this guard is the only thing left.
func requireTeam(teamID uuid.UUID) error {
	if teamID == uuid.Nil {
		return errors.Join(ErrInvalidInput, errors.New("missing team"))
	}
	return nil
}

// requireRef rejects a malformed reference: out-of-shape key, or a number that is not strictly
// positive. Numbers start at 1 across the whole product; 0 and negatives are hand-made URLs.
//
// The refusal is an ErrInvalidInput and not an ErrNotFound, and that is no oracle: the shape of a
// key says nothing about what exists in the team.
func requireRef(projectKey string, number int64) error {
	if !isProjectKey(projectKey) {
		return errors.Join(ErrInvalidInput, fmt.Errorf("invalid project key: %q", projectKey))
	}
	if number <= 0 {
		return errors.Join(ErrInvalidInput, fmt.Errorf("invalid number: %d", number))
	}
	return nil
}

// isProjectKey says whether a string has the shape of a project key: 2 to 8 characters, ASCII
// uppercase and digits — exactly what the workspace accepts at creation. Written by hand rather
// than as a regular expression: a compiled regexp would require a package-level var, and this
// loop is shorter than the line that would have declared it.
func isProjectKey(s string) bool {
	if len(s) < 2 || len(s) > 8 {
		return false
	}
	for _, c := range s {
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
