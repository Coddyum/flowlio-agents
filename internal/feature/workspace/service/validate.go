package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                       | Ligne |
// |----------------|--------------------------------------------------------------|-------|
// | validateSlug   | Vérifie le format d'un slug de team                            | 35    |
// | validateKey    | Vérifie le format d'une clé de projet                          | 45    |
// | validateName   | Vérifie qu'un nom n'est ni vide ni démesuré                    | 54    |
// | translateStore | Traduit une erreur de store en erreur domaine                  | 67    |
//
// Fin du sommaire.
// =====================================================================
//
// Les mêmes règles existent en CHECK dans la migration : la base est la garantie, cette
// validation est le message d'erreur utile.

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Coddyum/flowlio-ia/internal/feature/workspace/store"
)

const maxNameLen = 200

var (
	slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)
	keyPattern  = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)
)

// validateSlug vérifie le slug d'une team : minuscules, chiffres et tirets, 1 à 40 caractères.
func validateSlug(slug string) error {
	if !slugPattern.MatchString(slug) {
		return fmt.Errorf("%w: slug %q (minuscules, chiffres et tirets, 1 à 40 caractères)",
			ErrInvalidInput, slug)
	}
	return nil
}

// validateKey vérifie la clé d'un projet : majuscules et chiffres, 2 à 10 caractères, comme
// FRNT ou CORE. Elle apparaît dans chaque identifiant lisible, donc elle doit rester courte.
func validateKey(key string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("%w: clé %q (majuscules et chiffres, 2 à 10 caractères, ex: FRNT)",
			ErrInvalidInput, key)
	}
	return nil
}

// validateName vérifie qu'un nom lisible est renseigné et de taille raisonnable.
func validateName(field, name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("%w: %s est vide", ErrInvalidInput, field)
	}
	if len(trimmed) > maxNameLen {
		return fmt.Errorf("%w: %s dépasse %d caractères", ErrInvalidInput, field, maxNameLen)
	}
	return nil
}

// translateStore ramène les erreurs du store aux erreurs domaine du service, en conservant la
// cause pour le log.
func translateStore(err error, op string) error {
	// Le succès traverse cette fonction sans dommage : sans ce cas, fmt.Errorf envelopperait nil
	// et fabriquerait une erreur là où il n'y en a pas.
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
