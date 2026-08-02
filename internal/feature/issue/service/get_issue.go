package service

import (
	"context"

	"github.com/Coddyum/flowlio-ia/internal/feature/issue/store"
)

// maxThreadMessages borne le fil renvoyé. Un échange long ne doit pas entrer d'un bloc dans le
// contexte d'un agent ; les derniers messages sont ceux qui portent l'état de la discussion.
const maxThreadMessages = 10

// GetIssue renvoie une issue et la fin de son fil.
//
// Les deux lectures portent la même clause de visibilité, appliquée indépendamment : la seconde
// ne fait pas confiance au résultat de la première.
func (s *service) GetIssue(ctx context.Context, ref Ref) (IssueDetail, error) {
	if err := validateScope(ref.TeamID, ref.CallerProjectID); err != nil {
		return IssueDetail{}, err
	}

	storeRef := store.Ref{
		TeamID:          ref.TeamID,
		CallerProjectID: ref.CallerProjectID,
		ProjectKey:      ref.ProjectKey,
		Number:          ref.Number,
	}

	found, err := s.store.IssueByRef(ctx, storeRef)
	if err != nil {
		return IssueDetail{}, translateStore(err, "issue by ref")
	}

	rows, err := s.store.ListMessages(ctx, storeRef, found.ID)
	if err != nil {
		return IssueDetail{}, translateStore(err, "list messages")
	}

	total := len(rows)
	if total > maxThreadMessages {
		rows = rows[total-maxThreadMessages:]
	}

	messages := make([]Message, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, toMessage(row))
	}

	return IssueDetail{
		Issue:         toIssue(found),
		Messages:      messages,
		MessagesTotal: total,
	}, nil
}
