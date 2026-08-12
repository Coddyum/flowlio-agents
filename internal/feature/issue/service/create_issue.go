package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/core/wakepush"
	"github.com/Coddyum/flowlio-agents/internal/feature/issue/store"
)

// CreateIssue opens a question towards a sibling project of the same team.
//
// The issue, its first message and its event are written in ONE transaction: a lost event is a
// notification never received, and an issue with no message would be an empty question.
//
// The recipient is named by its key, resolved INSIDE the insert query. A key that is unknown,
// belongs to another team, or has no declared trust towards it, reserves no number and yields the
// SAME error: one can therefore neither discover that a project of another team exists by trying
// keys, nor map out the trust graph of one's own.
//
// NO AUTHORISATION CHECK HERE, AND THAT IS THE RULE. The recipient, its team and the right to write
// to it are three conditions of the same WHERE, in the same statement. An `if` added here would
// have to re-resolve the readable key into a UUID, that is, hand-build the enumeration query the
// model refuses to expose.
//
// A self-addressing guard lived here until FLWL-19. It was DEAD: the issues_not_self CHECK raised
// inside tx.CreateIssue, so the test that followed was never reached and its message was never
// returned to anyone. Since the trust predicate it is doubly unreachable — self-addressing gives
// least = greatest, a shape not insertable in the graph. The guard that really produces the useful
// message is client-side, in cmd/flowlio/mcp_issue_tools.go.
func (s *service) CreateIssue(ctx context.Context, in CreateIssueInput) (Issue, error) {
	if err := validateScope(in.TeamID, in.AuthorProjectID); err != nil {
		return Issue{}, err
	}

	toProject := strings.ToUpper(strings.TrimSpace(in.ToProject))
	if toProject == "" {
		return Issue{}, fmt.Errorf("%w: missing recipient project", ErrInvalidInput)
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
			return translateStore(err, "project "+toProject)
		}

		if err := tx.AddFirstMessage(ctx, created.ID, in.AuthorProjectID, body); err != nil {
			return translateStore(err, "first message")
		}

		return translateStore(tx.AppendEvent(ctx, store.Event{
			TeamID:          in.TeamID,
			ProjectID:       created.ProjectID,
			ActorProjectID:  in.AuthorProjectID,
			NotifyProjectID: created.ProjectID, // the recipient must answer: wake it, not the author
			Kind:            store.KindIssueOpened,
			SubjectID:       created.ID,
		}), "opening event")
	})
	if err != nil {
		return Issue{}, err
	}

	// A question just landed for the recipient: push a wake to its local waker, so a dead session
	// there learns it has something to answer without waiting for a human (D55). Best effort — the
	// escalation ladder and the piggyback are the backstop.
	wakepush.Signal(s.cache, in.TeamID, created.ProjectID)

	return toIssue(created), nil
}
