package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                       | Ligne |
// |-----------------|--------------------------------------------------------------|-------|
// | service.Answer  | Appends a message to the thread and applies the transition     | 30    |
// | kindFor         | Names the event after the state reached                        | 84    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/core/wakepush"
	"github.com/Coddyum/flowlio-agents/internal/feature/issue/store"
)

// Answer appends a message to the thread and, when asked, closes the issue.
//
// The resulting state is NOT chosen by the caller: it is deduced in the database from its role in
// the conversation — the recipient speaking moves the issue to `answered`, the author following up
// puts it back to `open`. An agent therefore cannot claim to have answered its own question.
//
// Answering a closed issue is refused: without that guard, a late reply would resurrect a finished
// discussion in the correspondent's inbox. The refusal yields ErrNotFound, like an issue out of
// reach — the two cases stay indistinguishable.
func (s *service) Answer(ctx context.Context, in AnswerInput) (Issue, error) {
	if err := validateScope(in.Ref.TeamID, in.Ref.CallerProjectID); err != nil {
		return Issue{}, err
	}

	body := strings.TrimSpace(in.Body)
	if err := validateBody(body); err != nil {
		return Issue{}, err
	}

	var answered store.Issue
	err := s.store.WithTx(ctx, func(tx store.Store) error {
		var err error
		answered, err = tx.Answer(ctx, store.Answer{
			Ref: store.Ref{
				TeamID:          in.Ref.TeamID,
				CallerProjectID: in.Ref.CallerProjectID,
				ProjectKey:      in.Ref.ProjectKey,
				Number:          in.Ref.Number,
			},
			Body:  body,
			Close: in.Close,
		})
		if err != nil {
			return translateStore(err, "answer issue")
		}

		return translateStore(tx.AppendEvent(ctx, store.Event{
			TeamID:         in.Ref.TeamID,
			ProjectID:      answered.ProjectID,
			ActorProjectID: in.Ref.CallerProjectID,
			Kind:           kindFor(answered.State),
			SubjectID:      answered.ID,
		}), "answer event")
	})
	if err != nil {
		return Issue{}, err
	}

	// Wake the OTHER party — the one who did not just speak. The recipient answering wakes the
	// author (its question is answered); the author following up wakes the recipient (a new message
	// awaits it). Signalling the caller's own repo would only wake the session that is already live
	// (D55). Best effort — the ladder and the piggyback are the backstop.
	other := answered.ProjectID
	if in.Ref.CallerProjectID == answered.ProjectID {
		other = answered.AuthorProjectID
	}
	wakepush.Signal(s.cache, in.Ref.TeamID, other)

	return toIssue(answered), nil
}

// kindFor names the event after the state reached. The kind describes what happened from the point
// of view of whoever will be told, not from that of whoever wrote.
func kindFor(state string) string {
	switch state {
	case "closed":
		return store.KindIssueClosed
	case "answered":
		return store.KindIssueAnswered
	default:
		return store.KindIssueReopened
	}
}
