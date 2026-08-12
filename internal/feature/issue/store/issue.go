package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément           | Résumé                                                      | Ligne |
// |-------------------|-------------------------------------------------------------|-------|
// | store.CreateIssue | Opens an issue towards a sibling project, number included     | 34    |
// | store.IssueByRef  | Reads an issue the caller can see                             | 68    |
// | store.ListIssues  | Lists the visible issues, filtered by role and by state       | 85    |
// | store.Answer      | Appends a message and applies the state transition            | 128   |
// | toIssue           | Projects a full row onto the domain type                      | 152   |
// | fromNullTime      | Turns a nullable date read from the database into a pointer   | 172   |
// | fromNullString    | Turns a nullable text column into a plain string               | 182   |
// | toNullString      | Turns a plain string into a nullable column, "" becoming NULL   | 191   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"database/sql"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// CreateIssue opens an issue towards a sibling project.
//
// Resolving the recipient, reserving its number and inserting all hold in ONE statement: if the key
// is unknown — or known but belonging to another team — the CTE produces nothing, hence no number
// is consumed. Nobody can push a third-party project's counter forward by guessing its key, and
// "does not exist" stays indistinguishable from "outside the team".
func (s *store) CreateIssue(ctx context.Context, in NewIssue) (Issue, error) {
	row, err := s.q.CreateIssue(ctx, database.CreateIssueParams{
		TeamID:          in.TeamID,
		AuthorProjectID: in.AuthorProjectID,
		ToProjectKey:    in.ToProjectKey,
		Title:           in.Title,
		Effort:          toNullString(in.Effort),
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
		Effort:          fromNullString(row.Effort),
	}, nil
}

// IssueByRef reads an issue named by CORE-34.
//
// The visibility clause — author OR recipient — is in the query. An issue the caller should not see
// yields ErrNotFound, exactly like a non-existent number: there is therefore no way to enumerate a
// sibling repo's backlog by trying numbers.
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

// ListIssues lists the issues the calling project can see.
//
// Role narrows the visibility clause, it never authorises it: both flags add to the full predicate
// rather than replace it.
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

// Answer appends a message to the thread and applies the state transition.
//
// Both hold in a single statement: split apart, a correspondent could close the issue in between,
// and the message would land in a closed issue without moving updated_at — a written answer that
// never shows up in anyone's inbox.
//
// The state is not a parameter: it is computed in the database from WHO is speaking.
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

// toIssue projects a full row onto the domain type. The calling project serves to decide the
// direction of the conversation, which this query does not compute itself.
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
		Effort:           fromNullString(row.Effort),
	}
}

// fromNullTime turns a nullable date read from the database into a pointer.
func fromNullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	value := t.Time
	return &value
}

// fromNullString turns a nullable text column into a plain string, a NULL becoming "". The effort
// tier is the only such column here: "" is its "unspecified", folded to standard downstream.
func fromNullString(s sql.NullString) string {
	if !s.Valid {
		return ""
	}
	return s.String
}

// toNullString turns a plain string into a nullable column, "" becoming NULL. An empty effort must
// reach the column as NULL, not as an empty string the CHECK would reject.
func toNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
