package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément      | Résumé                                                          | Ligne |
// |--------------|-----------------------------------------------------------------|-------|
// | requireTeam  | Refuse un scope vide avant tout accès au store                    | 56    |
// | requireRef   | Refuse une référence malformée avant tout accès au store          | 68    |
// | isProjectKey | Dit si une chaîne a la forme d'une clé de projet                  | 82    |
//
// Fin du sommaire.
// =====================================================================
//
// LES BORNES SONT DES CONSTANTES DE SERVICE, JAMAIS DES PARAMÈTRES. Un `?limit=` rendrait la
// charge de la requête dépendante de l'appelant sur une surface qui lit une team entière ; et
// `truncated` deviendrait un nombre que le client s'est infligé lui-même, donc une information
// sans valeur.
//
// `projects[]` n'est jamais borné, quelle que soit la constante : un repo qui disparaît de
// l'écran du superviseur est le seul défaut irrécupérable de cette surface — il ne peut pas
// chercher ce qu'il ne voit pas.

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// maxDebts borne la file de dettes. Cinquante lignes tiennent dans un écran de terminal déroulé
// une fois ; au-delà, ce n'est plus une file d'attente, c'est un rapport.
const maxDebts = 50

// maxMessages borne un fil d'issue. Deux cents messages sur une seule question n'est pas un cas
// nominal : c'est le signe que la conversation aurait dû devenir une tâche.
const maxMessages = 200

// maxNotes borne les notes d'une tâche. Cinquante notes, c'est déjà plusieurs jours de travail
// documenté.
const maxNotes = 50

// staleAfter est l'âge au-delà duquel une tâche `in_progress` est réputée dormante.
//
// Vingt-quatre heures et pas moins : un agent peut être relancé le lendemain matin sans que la
// session soit morte. Le seuil vit ICI et non dans la query — l'horloge appartient au service, le
// scope à la query, et le test d'intégration devient déterministe.
const staleAfter = 24 * time.Hour

// requireTeam refuse un scope vide AVANT tout accès au store.
//
// Défense en profondeur : le handler résout déjà la team, et un uuid.Nil ne matcherait aucune
// ligne. Mais un scope qui vaut « zéro » ne doit pas atteindre une couche dont toutes les queries
// tiennent leur sûreté de ce paramètre — le jour où une query change, ce garde est la seule chose
// qui reste.
func requireTeam(teamID uuid.UUID) error {
	if teamID == uuid.Nil {
		return errors.Join(ErrInvalidInput, errors.New("team manquante"))
	}
	return nil
}

// requireRef refuse une référence malformée : clé hors forme, ou numéro non strictement positif.
// Les numéros commencent à 1 dans tout le produit ; 0 et les négatifs sont des URL bricolées.
//
// Le refus est un ErrInvalidInput et non un ErrNotFound, et ce n'est pas un oracle : la forme
// d'une clé ne dit rien de ce qui existe dans la team.
func requireRef(projectKey string, number int64) error {
	if !isProjectKey(projectKey) {
		return errors.Join(ErrInvalidInput, fmt.Errorf("clé de projet invalide: %q", projectKey))
	}
	if number <= 0 {
		return errors.Join(ErrInvalidInput, fmt.Errorf("numéro invalide: %d", number))
	}
	return nil
}

// isProjectKey dit si une chaîne a la forme d'une clé de projet : 2 à 8 caractères, majuscules
// ASCII et chiffres — exactement ce que le workspace accepte à la création. Écrit à la main
// plutôt qu'en expression régulière : un regexp compilé demanderait un var de paquet, et cette
// boucle est plus courte que la ligne qui l'aurait déclaré.
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
