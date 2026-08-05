package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                   | Résumé                                                | Ligne |
// |---------------------------|-------------------------------------------------------|-------|
// | store.CreateDependency    | Ouvre une arête de blocage entre deux tâches            | 37    |
// | store.ReleaseBlockerEdges | Libère les arêtes qu'une tâche vient de débloquer       | 57    |
// | store.ReleaseEdge         | Libère une arête nommée                                 | 72    |
// | store.ClearBlock          | Ramène une tâche bloquée par arête à `todo`             | 89    |
// | store.ActiveEdges         | Rend le graphe de blocage actif du projet               | 103   |
// | toDependency              | Projette une ligne générée en type domaine              | 117   |
//
// Les deux boucles de projection de ReleasedEdge se ressemblent sans pouvoir être factorisées :
// sqlc engendre un type de ligne DISTINCT par query, et une seule ligne de code les sépare.
//
// Fin du sommaire.
// =====================================================================

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// CreateDependency ouvre une arête « in.TaskID est bloquée par in.BlockerTaskID ».
//
// Le project_id de l'arête est lu depuis la tâche bloquée et non fourni : c'est ce qui fait entrer
// les deux extrémités dans la même clé étrangère composite. Un blocker d'un autre projet fait donc
// échouer la contrainte en base — la dépendance inter-repos est inexprimable, pas seulement
// refusée par le service (D42).
//
// Une tâche bloquée archivée, ou d'un autre projet, est hors de portée de la query et remonte
// ErrNotFound. Une arête active déjà ouverte sur le même couple remonte ErrConflict : rejouer
// block_task ne fabrique pas un second blocage à libérer.
func (s *store) CreateDependency(ctx context.Context, in NewDependency) (Dependency, error) {
	row, err := s.q.CreateTaskDependency(ctx, database.CreateTaskDependencyParams{
		TaskID:        in.TaskID,
		BlockerTaskID: in.BlockerTaskID,
		TeamID:        in.TeamID,
		ProjectID:     in.ProjectID,
		UntilStatus:   database.TaskStatus(in.UntilStatus),
		SetBlocked:    in.SetBlocked,
	})
	if err != nil {
		return Dependency{}, translate(err, "create dependency")
	}
	return toDependency(row), nil
}

// ReleaseBlockerEdges libère les arêtes que blockerTaskID vient de débloquer en atteignant
// blockerStatus, et rend celles qui l'ont été.
//
// force ignore la condition de libération : une bloquante archivée n'atteindra jamais rien, et
// laisser ses arêtes en place fabriquerait des tâches que plus rien ne peut débloquer.
func (s *store) ReleaseBlockerEdges(ctx context.Context, projectID, blockerTaskID uuid.UUID, blockerStatus string, force bool) ([]uuid.UUID, error) {
	freed, err := s.q.ReleaseDependenciesOfBlocker(ctx, database.ReleaseDependenciesOfBlockerParams{
		BlockerTaskID: blockerTaskID,
		ProjectID:     projectID,
		BlockerStatus: database.TaskStatus(blockerStatus),
		Force:         force,
	})
	if err != nil {
		return nil, translate(err, "release blocker edges")
	}
	return freed, nil
}

// ReleaseEdge libère une arête nommée et rend la tâche libérée. Une arête absente ou déjà libérée
// rend une liste vide et non une erreur : pour l'appelant, les deux sont le même non-événement.
func (s *store) ReleaseEdge(ctx context.Context, projectID, taskID, blockerTaskID uuid.UUID) ([]uuid.UUID, error) {
	freed, err := s.q.ReleaseDependencyPair(ctx, database.ReleaseDependencyPairParams{
		TaskID:        taskID,
		BlockerTaskID: blockerTaskID,
		ProjectID:     projectID,
	})
	if err != nil {
		return nil, translate(err, "release edge")
	}
	return freed, nil
}

// ClearBlock ramène la tâche de `blocked` à `todo`. Les trois conditions — statut encore bloqué,
// plus aucune arête active, au moins une arête ayant posé le blocage — vivent dans la query.
//
// false signifie « rien à changer » et non un échec : c'est le cas nominal d'une tâche bloquée
// par un agent pour une autre raison, qu'on notifie sans décider à sa place.
func (s *store) ClearBlock(ctx context.Context, teamID, projectID, taskID uuid.UUID) (bool, error) {
	rows, err := s.q.ClearTaskBlock(ctx, database.ClearTaskBlockParams{
		TaskID:    taskID,
		TeamID:    teamID,
		ProjectID: projectID,
	})
	if err != nil {
		return false, translate(err, "clear block")
	}
	return len(rows) > 0, nil
}

// ActiveEdges rend le graphe de blocage non libéré du projet, pour le parcours de détection de
// cycle. Seules les deux extrémités traversent le réseau : c'est tout ce dont le parcours a besoin.
func (s *store) ActiveEdges(ctx context.Context, projectID uuid.UUID) ([]Edge, error) {
	rows, err := s.q.ListActiveDependencyEdges(ctx, projectID)
	if err != nil {
		return nil, translate(err, "active edges")
	}

	edges := make([]Edge, 0, len(rows))
	for _, row := range rows {
		edges = append(edges, Edge{TaskID: row.TaskID, BlockerTaskID: row.BlockerTaskID})
	}
	return edges, nil
}

// toDependency projette une ligne générée en type domaine.
func toDependency(row database.TaskDependency) Dependency {
	return Dependency{
		ID:            row.ID,
		ProjectID:     row.ProjectID,
		TaskID:        row.TaskID,
		BlockerTaskID: row.BlockerTaskID,
		UntilStatus:   string(row.UntilStatus),
		SetBlocked:    row.SetBlocked,
		CreatedAt:     row.CreatedAt,
		ReleasedAt:    fromNullTime(row.ReleasedAt),
	}
}
