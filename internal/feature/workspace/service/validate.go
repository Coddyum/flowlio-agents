package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                       | Ligne |
// |----------------|--------------------------------------------------------------|-------|
// | validateSlug   | Checks the format of a team slug                               | 35    |
// | validateKey    | Checks the format of a project key                             | 45    |
// | validateName   | Checks a name is neither empty nor oversized                   | 54    |
// | translateStore | Turns a store error into a domain error                        | 67    |
//
// Fin du sommaire.
// =====================================================================
//
// The very same rules exist as CHECK in the migration: the database is the guarantee, this
// validation is the useful error message.

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
)

const maxNameLen = 200

var (
	slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)
	keyPattern  = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)
)

// validateSlug checks a team slug: lowercase letters, digits and dashes, 1 to 40 characters.
func validateSlug(slug string) error {
	if !slugPattern.MatchString(slug) {
		return fmt.Errorf("%w: slug %q (lowercase letters, digits and dashes, 1 to 40 characters)",
			ErrInvalidInput, slug)
	}
	return nil
}

// validateKey checks a project key: uppercase letters and digits, 2 to 10 characters, like FRNT or
// CORE. It shows up in every readable identifier, so it has to stay short.
func validateKey(key string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("%w: key %q (uppercase letters and digits, 2 to 10 characters, e.g. FRNT)",
			ErrInvalidInput, key)
	}
	return nil
}

// validateName checks a readable name is filled in and of reasonable size.
func validateName(field, name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("%w: %s is empty", ErrInvalidInput, field)
	}
	if len(trimmed) > maxNameLen {
		return fmt.Errorf("%w: %s exceeds %d characters", ErrInvalidInput, field, maxNameLen)
	}
	return nil
}

// translateStore brings store errors back to the service's domain errors, keeping the cause for
// the log.
func translateStore(err error, op string) error {
	// Success crosses this function unharmed: without that case, fmt.Errorf would wrap nil and
	// manufacture an error where there is none.
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("%w: %s", ErrNotFound, op)
	case errors.Is(err, store.ErrConflict):
		return fmt.Errorf("%w: %s", ErrConflict, op)
	default:
		return fmt.Errorf("workspace service: %s: %w", op, err)
	}
}
