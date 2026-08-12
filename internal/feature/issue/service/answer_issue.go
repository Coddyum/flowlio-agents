package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                       | Ligne |
// |-----------------|--------------------------------------------------------------|-------|
// | service.Answer  | Appends a message to the thread and applies the transition     | 31    |
// | kindFor         | Names the event after the state reached                        | 94    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/core/wakepush"
	"github.com/Coddyum/flowlio-agents/internal/feature/issue/store"
	"github.com/google/uuid"
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
	var other uuid.UUID
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

		// The OTHER party — the one who did not just speak — is who the event must wake, and so is its
		// notify target. The recipient answering wakes the author; the author following up wakes the
		// recipient. Never the caller: keying the probe head on `other` is what stops the answering
		// repo from waking itself for its own event.
		other = answered.ProjectID
		if in.Ref.CallerProjectID == answered.ProjectID {
			other = answered.AuthorProjectID
		}

		return translateStore(tx.AppendEvent(ctx, store.Event{
			TeamID:          in.Ref.TeamID,
			ProjectID:       answered.ProjectID,
			ActorProjectID:  in.Ref.CallerProjectID,
			NotifyProjectID: other,
			Kind:            kindFor(answered.State),
			SubjectID:       answered.ID,
		}), "answer event")
	})
	if err != nil {
		return Issue{}, err
	}

	// Push a wake to that same OTHER party's local waker, so a dead session there learns without
	// waiting for a human (D55). Signalling the caller's own repo would only wake the session that is
	// already live. Best effort — the ladder and the piggyback are the backstop.
	// The thread's tier, not a fresh one: a five-round exchange is one issue alternating states, so
	// every wake of it runs at the rigour its author declared when opening it. answered carries the
	// stored effort because Answer reads the issue (SELECT i.*) before it transitions.
	wakepush.Signal(s.cache, in.Ref.TeamID, other, answered.Effort)

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
