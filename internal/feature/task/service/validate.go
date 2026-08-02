package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                      | Ligne |
// |------------------|-------------------------------------------------------------|-------|
// | validateStatus   | Vérifie qu'un statut fait partie du vocabulaire du produit    | 49    |
// | validatePriority | Vérifie qu'une priorité fait partie du vocabulaire            | 58    |
// | validateTitle    | Vérifie qu'un titre n'est ni vide ni démesuré                 | 68    |
// | validateBody     | Vérifie qu'un corps markdown ne dépasse pas la borne          | 80    |
// | validateScope    | Refuse un scope de tenancy incomplet                          | 91    |
// | clampLimit       | Ramène une limite de listing dans les bornes                  | 101   |
// | translateStore   | Traduit une erreur de store en erreur domaine                 | 113   |
//
// Fin du sommaire.
// =====================================================================
//
// Les mêmes règles existent en ENUM et en CHECK dans la migration : la base est la garantie,
// cette validation est le message d'erreur utile.

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Coddyum/flowlio-ia/internal/feature/task/store"
	"github.com/google/uuid"
)

// Bornes de taille. Le corps est large — une tâche porte une consigne complète pour un agent —
// mais il reste borné : un backlog n'est pas un espace de stockage.
const (
	maxTitleLen = 200
	maxBodyLen  = 64 << 10

	defaultLimit = 50
	maxLimit     = 200
)

// Vocabulaire du domaine. Il doit rester identique aux enums task_status et task_priority de la
// migration 000003 : si les deux divergent, la base refuse l'écriture.
var (
	statuses   = []string{"todo", "in_progress", "blocked", "done"}
	priorities = []string{"low", "normal", "high", "urgent"}
)

// validateStatus vérifie qu'un statut fait partie du vocabulaire du produit.
func validateStatus(status string) error {
	if !slices.Contains(statuses, status) {
		return fmt.Errorf("%w: statut %q (attendu: %s)",
			ErrInvalidInput, status, strings.Join(statuses, ", "))
	}
	return nil
}

// validatePriority vérifie qu'une priorité fait partie du vocabulaire du produit.
func validatePriority(priority string) error {
	if !slices.Contains(priorities, priority) {
		return fmt.Errorf("%w: priorité %q (attendu: %s)",
			ErrInvalidInput, priority, strings.Join(priorities, ", "))
	}
	return nil
}

// validateTitle vérifie qu'un titre est renseigné et de taille raisonnable. Le titre est ce
// qu'un agent lit dans une liste : il doit tenir sur une ligne.
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

// validateBody vérifie qu'un texte markdown reste dans la borne.
func validateBody(field, body string) error {
	if len(body) > maxBodyLen {
		return fmt.Errorf("%w: %s de %d octets, maximum %d",
			ErrInvalidInput, field, len(body), maxBodyLen)
	}
	return nil
}

// validateScope refuse un scope de tenancy incomplet. Sans cette barrière, un identifiant nul
// passé par erreur produirait une query qui ne filtre plus rien de significatif : c'est un
// défaut de programmation, mais sa conséquence serait une fuite entre projets.
func validateScope(teamID, projectID uuid.UUID) error {
	if teamID == uuid.Nil || projectID == uuid.Nil {
		return fmt.Errorf("%w: scope de projet incomplet", ErrInvalidInput)
	}
	return nil
}

// clampLimit ramène une limite de listing dans les bornes. Une limite absente ou absurde donne
// la valeur par défaut plutôt qu'une erreur : un agent qui liste son backlog ne doit pas avoir à
// deviner une borne.
func clampLimit(limit int) int32 {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return int32(limit)
}

// translateStore ramène les erreurs du store aux erreurs domaine du service, en conservant la
// cause pour le log.
func translateStore(err error, op string) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("%w: %s", ErrNotFound, op)
	case errors.Is(err, store.ErrConflict):
		return fmt.Errorf("%w: %s", ErrConflict, op)
	default:
		return fmt.Errorf("task service: %s: %w", op, err)
	}
}
