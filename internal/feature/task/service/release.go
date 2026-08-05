package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                | Résumé                                                    | Ligne |
// |------------------------|-----------------------------------------------------------|-------|
// | releasesOnPatch        | Dit si un patch peut libérer des arêtes                     | 50    |
// | service.releaseBlocker | Libère ce qu'une tâche débloque en avançant, et notifie     | 66    |
// | service.announceFreed  | Ramène à `todo` ce qui peut l'être, et journalise           | 80    |
//
// Fin du sommaire.
// =====================================================================
//
// LE CŒUR DE LA FEATURE, et le point où elle cesse d'être un lien décoratif.
//
// Trois choses arrivent quand une bloquante avance, dans cet ordre, et TOUJOURS dans la
// transaction de l'écriture qui les déclenche :
//
//  1. les arêtes qu'elle satisfait sont marquées libérées ;
//  2. chaque tâche ainsi libérée repasse `todo` — mais SEULEMENT si toutes ses arêtes sont
//     libérées ET qu'au moins une l'avait bloquée. Cette règle vit dans la query ClearTaskBlock,
//     pour qu'aucun appelant ne puisse en oublier une branche ;
//  3. un `task.unblocked` est journalisé, sujet = la tâche DÉBLOQUÉE, pas la bloquante. C'est ce
//     que check_inbox rendra au projet.
//
// L'étape 3 a lieu même quand l'étape 2 n'a rien changé : une tâche que l'agent avait bloquée
// lui-même pour une autre raison doit apprendre que son obstacle est levé, sans qu'on décide de
// son statut à sa place. Notifier et décider sont deux gestes distincts, et un seul est automatisé.
//
// Hors transaction, le défaut serait exactement celui que cette carte existe pour supprimer : la
// bloquante commitée `done`, et la bloquée qui l'ignore pour toujours.

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
	"github.com/google/uuid"
)

// eventTaskUnblocked est le `kind` journalisé. Le format `domaine.fait` est imposé par une
// contrainte CHECK de la table events.
const eventTaskUnblocked = "task.unblocked"

// releasesOnPatch dit si un patch est susceptible de libérer des arêtes, et donc s'il doit passer
// par une transaction.
//
// Le but est de ne PAS payer une transaction sur le patch nominal — changer un titre, une
// priorité. `todo` et `blocked` ne libèrent rien : ce ne sont pas des progrès, et une arête ne
// peut pas les attendre (contrainte task_dependencies_until_is_progress).
func releasesOnPatch(patch store.TaskPatch) bool {
	if patch.Archive {
		return true
	}
	if patch.Status == nil {
		return false
	}
	return *patch.Status == statusInProgress || *patch.Status == statusDone
}

// releaseBlocker libère les arêtes que blocker vient de satisfaire, puis annonce le résultat.
//
// force vient de l'archivage : une bloquante archivée n'atteindra jamais son statut de libération,
// donc ses arêtes se libèrent quelle que soit leur condition. Sans ça, archiver une bloquante
// fabriquerait des tâches que plus rien ne peut débloquer — des mortes-vivantes, et le seul défaut
// de ce dispositif qu'aucun appel ultérieur ne rattraperait.
func (s *service) releaseBlocker(ctx context.Context, tx store.Store, blocker store.Task, force bool) error {
	freed, err := tx.ReleaseBlockerEdges(ctx, blocker.ProjectID, blocker.ID, blocker.Status, force)
	if err != nil {
		return translateStore(err, "release blocker edges")
	}
	return s.announceFreed(ctx, tx, blocker.TeamID, blocker.ProjectID, freed)
}

// announceFreed ramène à `todo` ce qui peut l'être, et journalise un `task.unblocked` par tâche
// libérée.
//
// La déduplication n'est pas de la prudence : une bloquante peut porter DEUX arêtes vers la même
// bloquée — une par `until_status` — et les libérer ensemble. Sans l'ensemble des tâches vues, la
// même tâche recevrait deux événements pour un seul déblocage, et l'inbox le montrerait deux fois.
func (s *service) announceFreed(ctx context.Context, tx store.Store, teamID, projectID uuid.UUID, freed []uuid.UUID) error {
	seen := make(map[uuid.UUID]bool, len(freed))
	for _, taskID := range freed {
		if seen[taskID] {
			continue
		}
		seen[taskID] = true

		if _, err := tx.ClearBlock(ctx, teamID, projectID, taskID); err != nil {
			return translateStore(err, "clear block")
		}

		// L'acteur est le projet lui-même : une dépendance ne traverse jamais un repo (D42), donc
		// il n'existe pas de cas où l'auteur du déblocage soit un tiers.
		if err := tx.AppendEvent(ctx, store.Event{
			TeamID:         teamID,
			ProjectID:      projectID,
			ActorProjectID: projectID,
			Kind:           eventTaskUnblocked,
			SubjectID:      taskID,
		}); err != nil {
			return translateStore(err, "append unblocked event")
		}
	}
	return nil
}
