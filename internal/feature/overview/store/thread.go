package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                      | Ligne |
// |---------------------|-------------------------------------------------------------|-------|
// | store.IssueByRef    | Rend une issue de la team, sans être ni auteur ni destinataire | 32  |
// | store.IssueMessages | Rend les N derniers messages, dans l'ordre de lecture         | 65    |
//
// Fin du sommaire.
// =====================================================================
//
// C'EST ICI QU'EST LA CAPACITÉ NOUVELLE, et donc le risque. GetIssueByRef (feature issue) porte
// une clause de visibilité — on est l'auteur ou le destinataire. Ici elle est ABSENTE : un
// superviseur lit une conversation WEB→CORE sans être ni l'un ni l'autre. C'est exactement ce qui
// rend ces deux méthodes interdites à tout principal de portée projet, et c'est le middleware
// AdminOnly du module qui le garantit.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// IssueByRef rend l'issue désignée par la clé de son destinataire et son numéro, à condition
// qu'elle appartienne à la team résolue. Hors team, c'est ErrNotFound — le même que pour une
// référence qui n'existe pas.
func (s *store) IssueByRef(ctx context.Context, teamID uuid.UUID, projectKey string, number int64) (Issue, error) {
	row, err := s.q.OverviewIssueByRef(ctx, database.OverviewIssueByRefParams{
		TeamID:     teamID,
		ProjectKey: projectKey,
		Number:     number,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Issue{}, ErrNotFound
		}
		return Issue{}, fmt.Errorf("overview store: issue %s-%d of team %s: %w", projectKey, number, teamID, err)
	}

	return Issue{
		ID:               row.ID,
		Number:           row.Number,
		State:            string(row.State),
		Title:            row.Title,
		ProjectKey:       row.ProjectKey,
		AuthorProjectKey: row.AuthorProjectKey,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}, nil
}

// IssueMessages rend les N messages les plus RÉCENTS, rendus dans l'ordre de lecture, et le total
// avant la borne. La queue d'un fil est la réponse : c'est elle qu'il faut garder quand il faut
// couper.
//
// teamID n'est pas décoratif ici. issue_messages n'a pas de colonne team_id et sa clé étrangère
// vers projects est SIMPLE : rien au niveau du schéma n'empêche un message d'une autre team de
// pointer ce fil. La query le refuse, et c'est la seule clause de join du dépôt dont le retrait
// est observable.
func (s *store) IssueMessages(ctx context.Context, teamID, issueID uuid.UUID, limit int32) ([]Message, int64, error) {
	rows, err := s.q.OverviewIssueMessages(ctx, database.OverviewIssueMessagesParams{
		TeamID:  teamID,
		IssueID: issueID,
		MaxRows: limit,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("overview store: messages of issue %s: %w", issueID, err)
	}

	out := make([]Message, 0, len(rows))
	var total int64
	for _, r := range rows {
		total = r.Total
		out = append(out, Message{
			AuthorKey: r.AuthorKey,
			BodyMd:    r.BodyMd,
			CreatedAt: r.CreatedAt,
		})
	}
	return out, total, nil
}
