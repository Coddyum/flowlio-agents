package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/issue/store"
)

// CreateIssue ouvre une question vers un projet frère de la même team.
//
// L'issue, son premier message et son événement s'écrivent dans UNE transaction : un événement
// perdu est une notification jamais reçue, et une issue sans message serait une question vide.
//
// Le destinataire est désigné par sa clé, résolue DANS la query d'insertion. Une clé inconnue —
// ou appartenant à une autre team — ne réserve aucun numéro et remonte la même erreur : on ne
// peut donc pas découvrir l'existence d'un projet d'une autre team en essayant des clés.
func (s *service) CreateIssue(ctx context.Context, in CreateIssueInput) (Issue, error) {
	if err := validateScope(in.TeamID, in.AuthorProjectID); err != nil {
		return Issue{}, err
	}

	toProject := strings.ToUpper(strings.TrimSpace(in.ToProject))
	if toProject == "" {
		return Issue{}, fmt.Errorf("%w: projet destinataire manquant", ErrInvalidInput)
	}

	title := strings.TrimSpace(in.Title)
	if err := validateTitle(title); err != nil {
		return Issue{}, err
	}
	body := strings.TrimSpace(in.Body)
	if err := validateBody(body); err != nil {
		return Issue{}, err
	}

	var created store.Issue
	err := s.store.WithTx(ctx, func(tx store.Store) error {
		var err error
		created, err = tx.CreateIssue(ctx, store.NewIssue{
			TeamID:          in.TeamID,
			AuthorProjectID: in.AuthorProjectID,
			ToProjectKey:    toProject,
			Title:           title,
			Body:            body,
		})
		if err != nil {
			return translateStore(err, "projet "+toProject)
		}

		// Une issue vers soi-même est refusée par la base (issues_not_self), qui remonterait un
		// conflit. Le message donné ici dit quoi faire à la place : le cas est une confusion
		// d'usage courante, pas une tentative d'abus.
		if created.ProjectID == in.AuthorProjectID {
			return fmt.Errorf("%w: une question à son propre projet est une tâche — utiliser create_task",
				ErrInvalidInput)
		}

		if err := tx.AddFirstMessage(ctx, created.ID, in.AuthorProjectID, body); err != nil {
			return translateStore(err, "premier message")
		}

		return translateStore(tx.AppendEvent(ctx, store.Event{
			TeamID:         in.TeamID,
			ProjectID:      created.ProjectID,
			ActorProjectID: in.AuthorProjectID,
			Kind:           store.KindIssueOpened,
			SubjectID:      created.ID,
		}), "événement d'ouverture")
	})
	if err != nil {
		return Issue{}, err
	}

	return toIssue(created), nil
}
