package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément           | Résumé                                                      | Ligne |
// |-------------------|-------------------------------------------------------------|-------|
// | store.CreateIssue | Ouvre une issue vers un projet frère, numéro compris          | 32    |
// | store.IssueByRef  | Lit une issue visible par l'appelant                          | 64    |
// | store.ListIssues  | Liste les issues visibles, filtrées par rôle et par état      | 81    |
// | store.Answer      | Ajoute un message et applique la transition d'état            | 124   |
// | toIssue           | Projette une ligne complète en type domaine                   | 148   |
// | fromNullTime      | Convertit une date nullable lue en base en pointeur            | 167   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"database/sql"
	"time"

	"github.com/Coddyum/flowlio-ia/internal/database"
	"github.com/google/uuid"
)

// CreateIssue ouvre une issue vers un projet frère.
//
// La résolution du destinataire, la réservation de son numéro et l'insertion tiennent dans UNE
// instruction : si la clé est inconnue — ou connue mais appartenant à une autre team — la CTE ne
// produit rien, donc aucun numéro n'est consommé. On ne peut pas faire avancer le compteur d'un
// projet tiers en devinant sa clé, et « inexistant » reste indiscernable de « hors team ».
func (s *store) CreateIssue(ctx context.Context, in NewIssue) (Issue, error) {
	row, err := s.q.CreateIssue(ctx, database.CreateIssueParams{
		TeamID:          in.TeamID,
		AuthorProjectID: in.AuthorProjectID,
		ToProjectKey:    in.ToProjectKey,
		Title:           in.Title,
	})
	if err != nil {
		return Issue{}, translate(err, "create issue")
	}

	return Issue{
		ID:              row.ID,
		TeamID:          row.TeamID,
		ProjectID:       row.ProjectID,
		AuthorProjectID: row.AuthorProjectID,
		Number:          row.Number,
		Title:           row.Title,
		State:           string(row.State),
		ProjectKey:      in.ToProjectKey,
		Incoming:        false,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
		ClosedAt:        fromNullTime(row.ClosedAt),
	}, nil
}

// IssueByRef lit une issue désignée par CORE-34.
//
// La clause de visibilité — auteur OU destinataire — est dans la query. Une issue que l'appelant
// ne devrait pas voir remonte ErrNotFound, exactement comme un numéro inexistant : il n'existe
// donc aucun moyen d'énumérer le backlog d'un repo frère en essayant des numéros.
func (s *store) IssueByRef(ctx context.Context, ref Ref) (Issue, error) {
	row, err := s.q.GetIssueByRef(ctx, database.GetIssueByRefParams{
		TeamID:          ref.TeamID,
		ProjectKey:      ref.ProjectKey,
		Number:          ref.Number,
		CallerProjectID: ref.CallerProjectID,
	})
	if err != nil {
		return Issue{}, translate(err, "issue by ref")
	}
	return toIssue(row, ref.CallerProjectID), nil
}

// ListIssues liste les issues visibles par le projet appelant.
//
// Role restreint la clause de visibilité, il ne l'autorise jamais : les deux drapeaux
// s'ajoutent au prédicat complet plutôt que de le remplacer.
func (s *store) ListIssues(ctx context.Context, filter IssueFilter) ([]Issue, error) {
	params := database.ListIssuesParams{
		TeamID:        filter.TeamID,
		ProjectID:     filter.ProjectID,
		OnlyIncoming:  filter.Role == "incoming",
		OnlyOutgoing:  filter.Role == "outgoing",
		IncludeClosed: filter.IncludeClosed,
		MaxRows:       filter.Limit,
	}
	if isState(filter.State) {
		params.State = database.NullIssueState{
			IssueState: database.IssueState(filter.State),
			Valid:      true,
		}
	}

	rows, err := s.q.ListIssues(ctx, params)
	if err != nil {
		return nil, translate(err, "list issues")
	}

	issues := make([]Issue, 0, len(rows))
	for _, row := range rows {
		issues = append(issues, Issue{
			Number:           row.Number,
			Title:            row.Title,
			State:            string(row.State),
			ProjectKey:       row.ProjectKey,
			AuthorProjectKey: row.AuthorProjectKey,
			Incoming:         row.Incoming,
			UpdatedAt:        row.UpdatedAt,
		})
	}
	return issues, nil
}

// Answer ajoute un message au fil et applique la transition d'état.
//
// Les deux tiennent dans une seule instruction : séparées, un correspondant pourrait fermer
// l'issue entre les deux, et le message atterrirait dans une issue close sans faire bouger
// updated_at — une réponse écrite qui n'apparaît jamais dans l'inbox de personne.
//
// L'état n'est pas un paramètre : il est calculé en base depuis QUI parle.
func (s *store) Answer(ctx context.Context, in Answer) (Issue, error) {
	issue, err := s.IssueByRef(ctx, in.Ref)
	if err != nil {
		return Issue{}, err
	}

	row, err := s.q.AnswerIssue(ctx, database.AnswerIssueParams{
		TeamID:          in.Ref.TeamID,
		TargetProjectID: issue.ProjectID,
		Number:          in.Ref.Number,
		ProjectID:       in.Ref.CallerProjectID,
		BodyMd:          in.Body,
		Close:           in.Close,
	})
	if err != nil {
		return Issue{}, translate(err, "answer issue")
	}

	issue.State = string(row.State)
	return issue, nil
}

// toIssue projette une ligne complète en type domaine. Le projet appelant sert à décider du
// sens de la conversation, que cette query ne calcule pas elle-même.
func toIssue(row database.GetIssueByRefRow, callerProjectID uuid.UUID) Issue {
	return Issue{
		ID:               row.ID,
		TeamID:           row.TeamID,
		ProjectID:        row.ProjectID,
		AuthorProjectID:  row.AuthorProjectID,
		Number:           row.Number,
		Title:            row.Title,
		State:            string(row.State),
		ProjectKey:       row.ProjectKey,
		AuthorProjectKey: row.AuthorProjectKey,
		Incoming:         row.ProjectID == callerProjectID,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
		ClosedAt:         fromNullTime(row.ClosedAt),
	}
}

// fromNullTime convertit une date nullable lue en base en pointeur.
func fromNullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	value := t.Time
	return &value
}
