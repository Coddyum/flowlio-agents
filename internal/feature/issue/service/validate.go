package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                      | Ligne |
// |------------------|-------------------------------------------------------------|-------|
// | validateTitle    | Checks a title is neither empty nor oversized                 | 53    |
// | validateBody     | Checks a message body is present and bounded                  | 68    |
// | validateRole     | Checks a role belongs to the vocabulary                       | 80    |
// | validateState    | Checks a state belongs to the vocabulary                      | 89    |
// | validateScope    | Rejects an incomplete tenancy scope                           | 99    |
// | clampLimit       | Brings a listing limit back within bounds                     | 107   |
// | translateStore   | Turns a store error into a domain error                       | 122   |
//
// Fin du sommaire.
// =====================================================================
//
// The very same rules exist as ENUM and CHECK in migration 000004: the database is the guarantee,
// this validation is the useful error message.

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/issue/store"
	"github.com/google/uuid"
)

// MaxBodyLen is the largest markdown body an issue or a message may carry.
//
// Exported for the same reason as its counterpart in the task feature: the handler has to DERIVE
// its transport bound from what the service accepts, instead of picking it alongside. A field bound
// a request cannot reach is not a bound.
const MaxBodyLen = 64 << 10

const (
	maxTitleLen = 200
	maxBodyLen  = MaxBodyLen

	defaultLimit = 20
	maxLimit     = 100
)

var (
	roles  = []string{"incoming", "outgoing"}
	states = []string{"open", "answered", "closed"}
)

// validateTitle checks an issue's title. It is the only thing the recipient repo will see in its
// inbox: it has to fit on one line.
func validateTitle(title string) error {
	if title == "" {
		return fmt.Errorf("%w: empty title", ErrInvalidInput)
	}
	if len([]rune(title)) > maxTitleLen {
		return fmt.Errorf("%w: title of %d characters, maximum %d",
			ErrInvalidInput, len([]rune(title)), maxTitleLen)
	}
	return nil
}

// validateBody checks a message carries something.
//
// An empty body is refused even to close an issue: a closing with no reason leaves the
// correspondent facing a shut question without knowing why.
func validateBody(body string) error {
	if body == "" {
		return fmt.Errorf("%w: empty message", ErrInvalidInput)
	}
	if len(body) > maxBodyLen {
		return fmt.Errorf("%w: message of %d bytes, maximum %d",
			ErrInvalidInput, len(body), maxBodyLen)
	}
	return nil
}

// validateRole checks the requested role. Empty means "both directions".
func validateRole(role string) error {
	if role == "" || slices.Contains(roles, role) {
		return nil
	}
	return fmt.Errorf("%w: role %q (expected: %s)",
		ErrInvalidInput, role, strings.Join(roles, ", "))
}

// validateState checks the requested state. Empty means "every state".
func validateState(state string) error {
	if state == "" || slices.Contains(states, state) {
		return nil
	}
	return fmt.Errorf("%w: state %q (expected: %s)",
		ErrInvalidInput, state, strings.Join(states, ", "))
}

// validateScope rejects an incomplete tenancy scope: a query filtered on a nil UUID protects
// nothing any more.
func validateScope(teamID, projectID uuid.UUID) error {
	if teamID == uuid.Nil || projectID == uuid.Nil {
		return fmt.Errorf("%w: incomplete project scope", ErrInvalidInput)
	}
	return nil
}

// clampLimit brings a listing limit back within bounds.
func clampLimit(limit int) int32 {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return int32(limit)
}

// translateStore brings store errors back to the domain errors.
//
// ErrCorrupted is NOT translated into a domain error: a number served twice is a server failure,
// not a caller's fault. It travels up untouched and the handler turns it into a 500 — answering
// 409 would make an agent that did nothing wrong retry forever.
func translateStore(err error, op string) error {
	// Success crosses this function: calls shaped like
	// `return translateStore(tx.AppendEvent(...), "…")` are the nominal path. Without that case,
	// fmt.Errorf would wrap nil and manufacture an error where there is none.
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("%w: %s", ErrNotFound, op)
	case errors.Is(err, store.ErrCorrupted):
		return fmt.Errorf("issue service: %s: %w", op, err)
	case errors.Is(err, store.ErrConflict):
		return fmt.Errorf("%w: %s", ErrConflict, op)
	default:
		return fmt.Errorf("issue service: %s: %w", op, err)
	}
}
