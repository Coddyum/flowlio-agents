package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                           | Ligne |
// |----------------|------------------------------------------------------------------|-------|
// | store.Projects | Rend une ligne de compteurs par repo, sans jamais en omettre un   | 23    |
// | store.LastSeen | Rend le pouls des repos dont un token a déjà servi                | 46    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Projects rend une ligne par repo de la team, TOUJOURS, y compris pour un repo qui n'a rien en
// vol. Aucune borne n'est appliquée ici et il ne faut jamais en ajouter : la liste des projets
// est la carte du superviseur.
func (s *store) Projects(ctx context.Context, teamID uuid.UUID) ([]ProjectCounters, error) {
	rows, err := s.q.OverviewProjects(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("overview store: projects of team %s: %w", teamID, err)
	}

	out := make([]ProjectCounters, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProjectCounters{
			Key:            r.Key,
			OwesAnswer:     r.OwesAnswer,
			AwaitingAnswer: r.AwaitingAnswer,
			AnsweredUnread: r.AnsweredUnread,
			TasksRunning:   r.TasksRunning,
			TasksBlocked:   r.TasksBlocked,
		})
	}
	return out, nil
}

// LastSeen rend le pouls des repos dont au moins un token a déjà servi. Un repo absent du
// résultat n'a pas de pouls : c'est au service de fusionner par clé, pas à la query de fabriquer
// un horodatage nul qui se lirait comme une date.
func (s *store) LastSeen(ctx context.Context, teamID uuid.UUID) ([]ProjectPulse, error) {
	rows, err := s.q.OverviewLastSeen(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("overview store: last seen of team %s: %w", teamID, err)
	}

	out := make([]ProjectPulse, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProjectPulse{Key: r.Key, LastSeen: r.LastSeen})
	}
	return out, nil
}
