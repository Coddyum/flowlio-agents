package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                         | Ligne |
// |------------------|----------------------------------------------------------------|-------|
// | store.IssueDebts | Rend les issues en vol de la team, la plus vieille d'abord      | 28    |
// | store.TaskDebts  | Rend les tâches bloquées ou dormantes, la plus vieille d'abord  | 59    |
//
// Fin du sommaire.
// =====================================================================
//
// LES DEUX MÉTHODES RENDENT LE TOTAL AVANT LA BORNE. Le total vient de `count(*) OVER ()`, donc
// d'un comptage fait par la même query, sur le même instantané : un second aller-retour pourrait
// compter un état différent de celui qu'il annonce.

import (
	"context"
	"fmt"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// IssueDebts rend les issues en vol de la team, la PLUS VIEILLE d'abord, et le total avant la
// borne. Zéro ligne rend un total de zéro : le total n'existe que porté par une ligne.
func (s *store) IssueDebts(ctx context.Context, teamID uuid.UUID, limit int32) ([]IssueDebt, int64, error) {
	rows, err := s.q.OverviewIssueDebts(ctx, database.OverviewIssueDebtsParams{
		TeamID:  teamID,
		MaxRows: limit,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("overview store: issue debts of team %s: %w", teamID, err)
	}

	out := make([]IssueDebt, 0, len(rows))
	var total int64
	for _, r := range rows {
		total = r.Total
		out = append(out, IssueDebt{
			Number:           r.Number,
			State:            string(r.State),
			Title:            r.Title,
			ProjectKey:       r.ProjectKey,
			AuthorProjectKey: r.AuthorProjectKey,
			UpdatedAt:        r.UpdatedAt,
		})
	}
	return out, total, nil
}

// TaskDebts rend les tâches sur lesquelles un humain peut agir : toutes les bloquées, et les
// tâches en cours dont le dernier mouvement précède staleBefore.
//
// staleBefore est calculé par le service, jamais ici : l'horloge appartient au service, le scope
// à la query. C'est ce qui rend le test d'intégration déterministe et le seuil réglable sans
// migration.
func (s *store) TaskDebts(ctx context.Context, teamID uuid.UUID, staleBefore time.Time, limit int32) ([]TaskDebt, int64, error) {
	rows, err := s.q.OverviewTaskDebts(ctx, database.OverviewTaskDebtsParams{
		TeamID:      teamID,
		StaleBefore: staleBefore,
		MaxRows:     limit,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("overview store: task debts of team %s: %w", teamID, err)
	}

	out := make([]TaskDebt, 0, len(rows))
	var total int64
	for _, r := range rows {
		total = r.Total
		debt := TaskDebt{
			Number:          r.Number,
			Status:          string(r.Status),
			Priority:        string(r.Priority),
			Title:           r.Title,
			ProjectKey:      r.ProjectKey,
			LastMove:        r.LastMove,
			HasOpenQuestion: r.HasOpenQuestion,
		}
		if r.Deadline.Valid {
			deadline := r.Deadline.Time
			debt.Deadline = &deadline
		}
		out = append(out, debt)
	}
	return out, total, nil
}
