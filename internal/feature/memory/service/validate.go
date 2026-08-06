package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                        | Ligne |
// |----------------|---------------------------------------------------------------|-------|
// | validateScope  | Refuses a call that carries no tenancy scope                    | 40    |
// | validateSlug   | Refuses a slug that could not be cited                          | 53    |
// | validateKind   | Refuses a kind outside the settled vocabulary                   | 66    |
// | validateText   | Refuses a blank or oversized title or body                      | 77    |
// | boundLimit     | Brings a requested bound back inside the accepted range         | 92    |
// | translateStore | Brings store errors back to the service's domain errors         | 104   |
//
// Fin du sommaire.
// =====================================================================

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/memory/store"
	"github.com/google/uuid"
)

// slugPattern is the literal twin of the `memories_slug_shape` CHECK. Both exist: the database
// refuses the shape, and the service names WHY, because "constraint violation" tells the author of
// a bad slug nothing about what a good one looks like.
var slugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

const maxTitle = 200

// maxBody bounds one entry. Not a security bound — the quota is that — but a shape one: an entry
// past this is a document, and a document belongs in the repository, cited by an entry.
const maxBody = 64 << 10

// validateScope refuses a call carrying no tenancy scope. The store re-checks it in every query;
// neither trusts its caller.
func validateScope(teamID, projectID uuid.UUID) error {
	if teamID == uuid.Nil || projectID == uuid.Nil {
		return fmt.Errorf("%w: missing tenancy scope", ErrInvalidInput)
	}
	return nil
}

// validateSlug refuses a slug that could not be cited from somewhere else.
//
// A slug travels into commit messages, card descriptions and other entries: it has to survive
// being typed by hand and pasted into a URL. That is the whole reason for the character set, and
// the reason the comma is excluded is load-bearing elsewhere — `supersedes` is aggregated into a
// comma-joined column, and a slug containing one would split wrong.
func validateSlug(slug string) error {
	if slug == "" {
		return fmt.Errorf("%w: an entry needs a slug to be cited by", ErrInvalidInput)
	}
	if !slugPattern.MatchString(slug) {
		return fmt.Errorf("%w: slug %q must start with a letter or digit and hold only letters, "+
			"digits, dashes and underscores (64 characters at most)", ErrInvalidInput, slug)
	}
	return nil
}

// validateKind refuses a kind outside the settled vocabulary. Three, and no more: the register
// that demanded a dedicated write in the reference system is the one that stayed empty for months.
func validateKind(kind string) error {
	for _, known := range Kinds {
		if kind == known {
			return nil
		}
	}
	return fmt.Errorf("%w: unknown kind %q (expected one of %s)",
		ErrInvalidInput, kind, strings.Join(Kinds, ", "))
}

// validateText refuses a blank or oversized title or body.
func validateText(field, value string, max int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is empty", ErrInvalidInput, field)
	}
	if len(value) > max {
		return fmt.Errorf("%w: %s is %d bytes, maximum %d", ErrInvalidInput, field, len(value), max)
	}
	return nil
}

// boundLimit brings a requested bound back inside the accepted range.
//
// A bound past the maximum is CLAMPED rather than refused: an agent asking for too much wants
// everything, and answering with an error rather than with the maximum costs it a round trip to
// learn a number it could have been given.
func boundLimit(limit int32) int32 {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

// translateStore brings store errors back to the service's domain errors, keeping the cause for
// the log.
func translateStore(err error, op string) error {
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("%w: %s", ErrNotFound, op)
	case errors.Is(err, store.ErrConflict):
		return fmt.Errorf("%w: %s", ErrConflict, op)
	case errors.Is(err, store.ErrQuotaExceeded):
		return fmt.Errorf("%w: %s", ErrQuotaExceeded, op)
	default:
		return fmt.Errorf("memory service: %s: %w", op, err)
	}
}
