package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément       | Résumé                                                         | Ligne |
// |---------------|----------------------------------------------------------------|-------|
// | service.Check | Renvoie l'état actionnable du projet et avance le curseur        | 38    |
// | toIssueLines  | Projette des issues du store en lignes d'inbox                   | 101   |
// | toTaskLines   | Projette des tâches du store en lignes d'inbox                   | 122   |
// | overflow      | Compte ce qui n'a pas tenu dans un seau                          | 136   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/feature/inbox/store"
	"github.com/google/uuid"
)

// bucketSize borne chaque seau. Un agent qui démarre doit pouvoir tout lire : au-delà, la
// réponse cesse d'être un point de départ et devient un problème de plus.
//
// Une ligne de plus que la borne est demandée au store pour savoir s'il en reste, sans avoir à
// compter séparément — un COUNT par seau serait trois requêtes de plus pour un chiffre indicatif.
const bucketSize = 10

// Check renvoie l'état actionnable du projet, puis avance le curseur du token.
//
// La tête du journal est lue AVANT les seaux : un événement écrit pendant l'appel restera donc
// « nouveau » au prochain tour, plutôt que d'être dépassé sans avoir jamais été montré.
//
// Le curseur est avancé APRÈS que la réponse est constituée, et son échec n'est pas fatal : au
// pire un événement déjà lu reste marqué « nouveau » une fois de plus. Refuser la réponse parce
// qu'un drapeau n'a pas pu être mis à jour serait échanger une gêne contre une panne.
func (s *service) Check(ctx context.Context, in CheckInput) (Inbox, error) {
	if in.TeamID == uuid.Nil || in.ProjectID == uuid.Nil || in.TokenID == uuid.Nil {
		return Inbox{}, fmt.Errorf("%w: scope de projet incomplet", ErrInvalidInput)
	}

	scope := store.Scope{
		TokenID:   in.TokenID,
		TeamID:    in.TeamID,
		ProjectID: in.ProjectID,
		Limit:     bucketSize + 1,
	}

	projectKey, err := s.store.ProjectKey(ctx, in.TeamID, in.ProjectID)
	if err != nil {
		return Inbox{}, fmt.Errorf("inbox service: project key: %w", err)
	}

	cursor, err := s.store.Cursor(ctx, scope)
	if err != nil {
		return Inbox{}, fmt.Errorf("inbox service: cursor: %w", err)
	}

	incoming, err := s.store.IncomingOpen(ctx, scope, cursor.LastEventID)
	if err != nil {
		return Inbox{}, fmt.Errorf("inbox service: incoming: %w", err)
	}
	answered, err := s.store.OutgoingAnswered(ctx, scope, cursor.LastEventID)
	if err != nil {
		return Inbox{}, fmt.Errorf("inbox service: answered: %w", err)
	}
	tasks, err := s.store.InProgressTasks(ctx, scope)
	if err != nil {
		return Inbox{}, fmt.Errorf("inbox service: tasks: %w", err)
	}

	inbox := Inbox{
		Project: projectKey,
		// Une issue entrante porte MA clé : c'est mon projet qui la possède et qui lui a donné
		// son numéro. Une issue sortante porte celle du destinataire, qui est le pair.
		NeedsAnswer: toIssueLines(incoming, projectKey, true),
		Answered:    toIssueLines(answered, projectKey, false),
		InProgress:  toTaskLines(tasks, projectKey),
	}

	if more := (More{
		NeedsAnswer: overflow(len(incoming)),
		Answered:    overflow(len(answered)),
		InProgress:  overflow(len(tasks)),
	}); more != (More{}) {
		inbox.More = &more
	}

	if err := s.store.Advance(ctx, in.TokenID, cursor.HeadEventID); err != nil {
		// Best effort : la réponse est juste, seul le confort du prochain appel est dégradé.
		return inbox, nil
	}
	return inbox, nil
}

// toIssueLines projette des issues du store en lignes d'inbox.
//
// mine indique que la référence porte MA clé de projet — cas des issues entrantes, dont je suis
// le destinataire. Pour les sortantes, la référence porte celle du pair.
func toIssueLines(rows []store.IssueLine, projectKey string, mine bool) []IssueLine {
	lines := make([]IssueLine, 0, min(len(rows), bucketSize))
	for _, row := range rows[:min(len(rows), bucketSize)] {
		refKey := row.PeerKey
		if mine {
			refKey = projectKey
		}
		lines = append(lines, IssueLine{
			Ref:       fmt.Sprintf("%s-%d", refKey, row.Number),
			Title:     row.Title,
			Peer:      row.PeerKey,
			Excerpt:   row.Excerpt,
			Truncated: row.Truncated,
			New:       row.New,
			UpdatedAt: row.UpdatedAt,
		})
	}
	return lines
}

// toTaskLines projette des tâches du store en lignes d'inbox.
func toTaskLines(rows []store.TaskLine, projectKey string) []TaskLine {
	lines := make([]TaskLine, 0, min(len(rows), bucketSize))
	for _, row := range rows[:min(len(rows), bucketSize)] {
		lines = append(lines, TaskLine{
			Ref:      fmt.Sprintf("%s-%d", projectKey, row.Number),
			Title:    row.Title,
			Priority: row.Priority,
		})
	}
	return lines
}

// overflow compte ce qui n'a pas tenu dans un seau. Le store en renvoie un de plus que la borne :
// la présence de cette ligne suffit à dire « il en reste », sans compter davantage.
func overflow(fetched int) int {
	if fetched > bucketSize {
		return fetched - bucketSize
	}
	return 0
}
