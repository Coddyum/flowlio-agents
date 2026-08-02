package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                      | Ligne |
// |------------------|-------------------------------------------------------------|-------|
// | validateStatus   | Vérifie qu'un statut fait partie du vocabulaire du produit    | 55    |
// | validatePriority | Vérifie qu'une priorité fait partie du vocabulaire            | 64    |
// | validateTitle    | Vérifie qu'un titre n'est ni vide ni démesuré                 | 74    |
// | validateBody     | Vérifie qu'un corps markdown ne dépasse pas la borne          | 86    |
// | validateDeadline | Refuse une échéance dont l'année n'est pas sérialisable       | 105   |
// | validateScope    | Refuse un scope de tenancy incomplet                          | 118   |
// | clampLimit       | Ramène une limite de listing dans les bornes                  | 128   |
// | translateStore   | Traduit une erreur de store en erreur domaine                 | 140   |
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
	"time"

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

	// maxDeadlineYear est la dernière année qu'une échéance peut porter. La borne vient de
	// time.Time, qui refuse d'encoder en JSON une année hors [0, 9999].
	maxDeadlineYear = 9999
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

// validateDeadline refuse une échéance dont l'année sort de l'intervalle sérialisable en JSON.
//
// time.Time refuse d'encoder une année hors [0, 9999], et l'encodage a lieu APRÈS l'écriture en
// base : sans cette barrière, une tâche créée avec `9999-12-31T23:30:00-05:00` s'insère très
// bien, puis rend illisible le listing du projet entier — y compris les tâches saines créées
// ensuite, puisqu'elles voyagent dans le même tableau JSON.
//
// L'année est vérifiée en UTC ET en heure locale : Time.MarshalJSON évalue l'année dans la
// Location de la valeur, et pgx relit une colonne timestamptz dans le fuseau du serveur. Une
// valeur d'année 9999 en UTC peut donc être en 10000 après relecture sur un serveur à fuseau
// positif — contrôler seulement l'UTC laisserait passer exactement ce cas.
func validateDeadline(deadline *time.Time) error {
	if deadline == nil {
		return nil
	}
	if year := max(deadline.UTC().Year(), deadline.Local().Year()); year > maxDeadlineYear {
		return fmt.Errorf("%w: échéance en l'an %d, maximum %d", ErrInvalidInput, year, maxDeadlineYear)
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
		return fmt.Errorf("task service: %s: %w", op, err)
	}
}
