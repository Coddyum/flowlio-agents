package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                      | Ligne |
// |------------------|-------------------------------------------------------------|-------|
// | validateTitle    | Vérifie qu'un titre n'est ni vide ni démesuré                 | 46    |
// | validateBody     | Vérifie qu'un corps de message est présent et borné           | 61    |
// | validateRole     | Vérifie qu'un rôle fait partie du vocabulaire                 | 73    |
// | validateState    | Vérifie qu'un état fait partie du vocabulaire                 | 82    |
// | validateScope    | Refuse un scope de tenancy incomplet                          | 92    |
// | clampLimit       | Ramène une limite de listing dans les bornes                  | 100   |
// | translateStore   | Traduit une erreur de store en erreur domaine                 | 115   |
//
// Fin du sommaire.
// =====================================================================
//
// Les mêmes règles existent en ENUM et en CHECK dans la migration 000004 : la base est la
// garantie, cette validation est le message d'erreur utile.

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Coddyum/flowlio-ia/internal/feature/issue/store"
	"github.com/google/uuid"
)

const (
	maxTitleLen = 200
	maxBodyLen  = 64 << 10

	defaultLimit = 20
	maxLimit     = 100
)

var (
	roles  = []string{"incoming", "outgoing"}
	states = []string{"open", "answered", "closed"}
)

// validateTitle vérifie le titre d'une issue. C'est la seule chose que verra le repo
// destinataire dans son inbox : il doit tenir sur une ligne.
func validateTitle(title string) error {
	if title == "" {
		return fmt.Errorf("%w: titre vide", ErrInvalidInput)
	}
	if len([]rune(title)) > maxTitleLen {
		return fmt.Errorf("%w: titre de %d caractères, maximum %d",
			ErrInvalidInput, len([]rune(title)), maxTitleLen)
	}
	return nil
}

// validateBody vérifie qu'un message porte quelque chose.
//
// Un corps vide est refusé même pour clore une issue : une clôture sans motif laisse le
// correspondant devant une question fermée sans savoir pourquoi.
func validateBody(body string) error {
	if body == "" {
		return fmt.Errorf("%w: message vide", ErrInvalidInput)
	}
	if len(body) > maxBodyLen {
		return fmt.Errorf("%w: message de %d octets, maximum %d",
			ErrInvalidInput, len(body), maxBodyLen)
	}
	return nil
}

// validateRole vérifie le rôle demandé. Vide signifie « les deux sens ».
func validateRole(role string) error {
	if role == "" || slices.Contains(roles, role) {
		return nil
	}
	return fmt.Errorf("%w: rôle %q (attendu: %s)",
		ErrInvalidInput, role, strings.Join(roles, ", "))
}

// validateState vérifie l'état demandé. Vide signifie « tous les états ».
func validateState(state string) error {
	if state == "" || slices.Contains(states, state) {
		return nil
	}
	return fmt.Errorf("%w: état %q (attendu: %s)",
		ErrInvalidInput, state, strings.Join(states, ", "))
}

// validateScope refuse un scope de tenancy incomplet : une query filtrée sur un UUID nul ne
// protège plus rien.
func validateScope(teamID, projectID uuid.UUID) error {
	if teamID == uuid.Nil || projectID == uuid.Nil {
		return fmt.Errorf("%w: scope de projet incomplet", ErrInvalidInput)
	}
	return nil
}

// clampLimit ramène une limite de listing dans les bornes.
func clampLimit(limit int) int32 {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return int32(limit)
}

// translateStore ramène les erreurs du store aux erreurs domaine.
//
// ErrCorrupted n'est PAS traduit en erreur domaine : un numéro servi deux fois est une panne
// serveur, pas une faute de l'appelant. Il remonte tel quel et le handler en fera un 500 —
// répondre 409 ferait réessayer indéfiniment un agent qui n'a rien fait de mal.
func translateStore(err error, op string) error {
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
