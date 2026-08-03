package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                       | Ligne |
// |-----------------|--------------------------------------------------------------|-------|
// | service.Answer  | Ajoute un message au fil et applique la transition d'état      | 29    |
// | kindFor         | Nomme l'événement d'après l'état atteint                       | 73    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/issue/store"
)

// Answer ajoute un message au fil et, si demandé, clôt l'issue.
//
// L'état résultant n'est PAS choisi par l'appelant : il est déduit en base de son rôle dans la
// conversation — le destinataire qui parle passe l'issue en `answered`, l'auteur qui relance la
// remet en `open`. Un agent ne peut donc pas prétendre avoir répondu à sa propre question.
//
// Répondre à une issue close est refusé : sans ce garde, une réponse tardive ressusciterait une
// discussion terminée dans l'inbox du correspondant. Le refus remonte ErrNotFound, comme une
// issue hors de portée — les deux cas restent indiscernables.
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
		}), "événement de réponse")
	})
	if err != nil {
		return Issue{}, err
	}

	return toIssue(answered), nil
}

// kindFor nomme l'événement d'après l'état atteint. Le genre décrit ce qui s'est passé du point
// de vue de celui qui sera prévenu, pas du point de vue de celui qui a écrit.
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
