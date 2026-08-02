package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                | Résumé                                                  | Ligne |
// |------------------------|---------------------------------------------------------|-------|
// | store.ProjectKey       | Résout la clé du projet du token                          | 28    |
// | store.Cursor           | Lit le curseur du token et la tête du journal             | 44    |
// | store.IncomingOpen     | Les questions entrantes en attente de réponse             | 59    |
// | store.OutgoingAnswered | Mes questions qui ont reçu une réponse                    | 87    |
// | store.InProgressTasks  | Mes tâches en cours, signe d'une session interrompue      | 115   |
// | store.Advance          | Avance le curseur du token sans le faire reculer          | 142   |
// | translate              | Ramène une erreur de base à une erreur domaine            | 150   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Coddyum/flowlio-ia/internal/database"
	"github.com/google/uuid"
)

// ProjectKey résout la clé du projet du token, pour composer les références lisibles.
func (s *store) ProjectKey(ctx context.Context, teamID, projectID uuid.UUID) (string, error) {
	key, err := s.q.InboxProjectKey(ctx, database.InboxProjectKeyParams{
		ID:     projectID,
		TeamID: teamID,
	})
	if err != nil {
		return "", translate(err, "project key")
	}
	return key, nil
}

// Cursor lit la position du token et la tête du journal de la team.
//
// Un token qui n'a jamais appelé check_inbox n'a pas de ligne de curseur : la query renvoie 0
// plutôt qu'une absence, et tout apparaît donc comme nouveau. C'est exact, et c'est ce qui rend
// une rotation de token indolore.
func (s *store) Cursor(ctx context.Context, sc Scope) (Cursor, error) {
	row, err := s.q.InboxCursor(ctx, database.InboxCursorParams{
		TokenID: sc.TokenID,
		TeamID:  sc.TeamID,
	})
	if err != nil {
		return Cursor{}, translate(err, "cursor")
	}
	return Cursor{LastEventID: row.LastEventID, HeadEventID: row.HeadEventID}, nil
}

// IncomingOpen liste les questions dont je suis le destinataire et qui attendent une réponse.
//
// Dans ce seau, le dernier message est toujours celui de l'auteur : ma propre réponse ferait
// passer l'issue en `answered` et la sortirait d'ici.
func (s *store) IncomingOpen(ctx context.Context, sc Scope, lastEventID int64) ([]IssueLine, error) {
	rows, err := s.q.ListIncomingOpenIssues(ctx, database.ListIncomingOpenIssuesParams{
		TeamID:      sc.TeamID,
		ProjectID:   sc.ProjectID,
		LastEventID: lastEventID,
		MaxRows:     sc.Limit,
	})
	if err != nil {
		return nil, translate(err, "incoming open")
	}

	lines := make([]IssueLine, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, IssueLine{
			Number:    row.Number,
			Title:     row.Title,
			PeerKey:   row.PeerKey,
			Excerpt:   row.Excerpt,
			Truncated: row.Truncated,
			New:       row.IsNew,
			UpdatedAt: row.UpdatedAt,
		})
	}
	return lines, nil
}

// OutgoingAnswered liste mes questions qui ont reçu une réponse : j'étais bloqué, je ne le suis
// plus. PeerKey est ici la clé du destinataire, celle que porte la référence.
func (s *store) OutgoingAnswered(ctx context.Context, sc Scope, lastEventID int64) ([]IssueLine, error) {
	rows, err := s.q.ListOutgoingAnsweredIssues(ctx, database.ListOutgoingAnsweredIssuesParams{
		TeamID:      sc.TeamID,
		ProjectID:   sc.ProjectID,
		LastEventID: lastEventID,
		MaxRows:     sc.Limit,
	})
	if err != nil {
		return nil, translate(err, "outgoing answered")
	}

	lines := make([]IssueLine, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, IssueLine{
			Number:    row.Number,
			Title:     row.Title,
			PeerKey:   row.PeerKey,
			Excerpt:   row.Excerpt,
			Truncated: row.Truncated,
			New:       row.IsNew,
			UpdatedAt: row.UpdatedAt,
		})
	}
	return lines, nil
}

// InProgressTasks liste mes tâches en cours : une tâche restée là signale une session
// interrompue, à reprendre avant d'en ouvrir une nouvelle.
func (s *store) InProgressTasks(ctx context.Context, sc Scope) ([]TaskLine, error) {
	rows, err := s.q.ListInProgressTasks(ctx, database.ListInProgressTasksParams{
		TeamID:    sc.TeamID,
		ProjectID: sc.ProjectID,
		MaxRows:   sc.Limit,
	})
	if err != nil {
		return nil, translate(err, "in progress tasks")
	}

	lines := make([]TaskLine, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, TaskLine{
			Number:    row.Number,
			Title:     row.Title,
			Priority:  string(row.Priority),
			UpdatedAt: row.UpdatedAt,
		})
	}
	return lines, nil
}

// Advance avance le curseur du token.
//
// La query prend le maximum entre l'ancienne et la nouvelle valeur : deux check_inbox concurrents
// du même token ne peuvent donc pas faire reculer la position. Aucune transaction n'est
// nécessaire — le pire cas est un drapeau « nouveau » perdu.
func (s *store) Advance(ctx context.Context, tokenID uuid.UUID, headEventID int64) error {
	return translate(s.q.AdvanceInboxCursor(ctx, database.AdvanceInboxCursorParams{
		TokenID:     tokenID,
		LastEventID: headEventID,
	}), "advance cursor")
}

// translate ramène les erreurs de la base aux erreurs domaine du store.
func translate(err error, op string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return errors.Join(errors.New("inbox store: "+op), err)
}
