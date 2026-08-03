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
// Le destinataire est désigné par sa clé, résolue DANS la query d'insertion. Une clé inconnue,
// appartenant à une autre team, ou vers laquelle aucune confiance n'est déclarée, ne réserve aucun
// numéro et remonte la MÊME erreur : on ne peut donc ni découvrir l'existence d'un projet d'une
// autre team en essayant des clés, ni cartographier le graphe de confiance de la sienne.
//
// AUCUN CONTRÔLE D'AUTORISATION ICI, ET C'EST LA RÈGLE. Le destinataire, sa team et le droit de
// lui écrire sont trois conditions du même WHERE, dans la même instruction. Un `if` ajouté ici
// devrait re-résoudre la clé lisible en UUID, c'est-à-dire fabriquer à la main la query
// d'énumération que le modèle refuse d'exposer.
//
// Un garde anti-auto-adressage a vécu ici jusqu'à FLWL-19. Il était MORT : la CHECK issues_not_self
// levait à l'intérieur de tx.CreateIssue, donc le test qui suivait n'était jamais atteint et son
// message n'a jamais été rendu à personne. Depuis le prédicat de confiance il est doublement
// inatteignable — l'auto-adressage donne least = greatest, forme non insérable dans le graphe.
// Le garde qui produit vraiment le message utile est côté client, cmd/flowlio/mcp_issue_tools.go.
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
