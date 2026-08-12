package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément  | Résumé                                                             | Ligne |
// |----------|--------------------------------------------------------------------|-------|
// | toIssue  | Projects a store issue onto the API view, from the caller's side     | 28    |
// | toIssues | Projects a list of issues                                            | 47    |
// | toMessage| Projects a store message onto the API view                           | 56    |
//
// Fin du sommaire.
// =====================================================================

import (
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/feature/issue/store"
)

// toIssue projects an issue onto the API view, from the calling project's point of view.
//
// The reference ALWAYS carries the recipient's key: it owns the issue and its number, and it is
// that key the agent will have to reuse in order to answer.
//
// Role and Peer are computed here rather than returned raw: an agent receiving "author: WEB,
// recipient: CORE" still has to work out which one it is. Handing it "role: incoming, across:
// WEB" removes a piece of reasoning, hence a chance to get it wrong.
func toIssue(i store.Issue) Issue {
	role, peer := "outgoing", i.ProjectKey
	if i.Incoming {
		role, peer = "incoming", i.AuthorProjectKey
	}

	return Issue{
		Ref:       fmt.Sprintf("%s-%d", i.ProjectKey, i.Number),
		Title:     i.Title,
		State:     i.State,
		Role:      role,
		Peer:      peer,
		UpdatedAt: i.UpdatedAt,
		ClosedAt:  i.ClosedAt,
		Effort:    i.Effort,
	}
}

// toIssues projects a list of issues.
func toIssues(rows []store.Issue) []Issue {
	issues := make([]Issue, 0, len(rows))
	for _, row := range rows {
		issues = append(issues, toIssue(row))
	}
	return issues
}

// toMessage projects a store message onto the API view.
func toMessage(m store.Message) Message {
	return Message{
		Author:    m.AuthorKey,
		Body:      m.Body,
		CreatedAt: m.CreatedAt,
	}
}
