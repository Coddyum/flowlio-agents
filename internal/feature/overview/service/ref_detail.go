package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | service.RefDetail  | Yields the detail of a reference: issue first, task second   | 39  |
// | service.issueDetail| Assembles an issue thread and its message total              | 72    |
// | service.taskDetail | Assembles a task, its notes and their total                  | 106   |
//
// Fin du sommaire.
// =====================================================================
//
// WHY THE ISSUE COMES BEFORE THE TASK, AND WHY THAT IS NOT ARBITRARY.
//
// Issue and task numbers live in two sequences per project: `CORE-41` can therefore designate
// both. The order settles the collision, and it settles it on the issue's side because that is
// the reference an agent WRITES to another repo — the one a human will have in front of them when
// typing the command. A task is found from the overview screen, where the row already carries its
// kind.
//
// Accepted consequence: a task whose number collides with an issue of the same repo is not
// reachable through this route. Two calls, a fixed order, no disambiguation parameter — it is the
// cheapest trade-off, and the only one that asks nothing of the user.

import (
	"context"
	"errors"

	"github.com/Coddyum/flowlio-agents/internal/feature/overview/store"
	"github.com/google/uuid"
)

// RefDetail yields the detail of the designated reference, within the resolved team.
//
// A reference outside the team cannot be found, exactly like a reference that does not exist — it
// is the same ErrNotFound, and the same 404. Nothing in this response tells "CORE-41 does not
// exist" apart from "CORE-41 exists next door".
func (s *service) RefDetail(ctx context.Context, teamID uuid.UUID, projectKey string, number int64) (RefDetail, error) {
	if err := requireTeam(teamID); err != nil {
		return RefDetail{}, err
	}
	if err := requireRef(projectKey, number); err != nil {
		return RefDetail{}, err
	}

	issue, err := s.store.IssueByRef(ctx, teamID, projectKey, number)
	switch {
	case err == nil:
		return s.issueDetail(ctx, teamID, issue)
	case !errors.Is(err, store.ErrNotFound):
		return RefDetail{}, err
	}

	task, err := s.store.TaskByRef(ctx, teamID, projectKey, number)
	switch {
	case err == nil:
		return s.taskDetail(ctx, teamID, task)
	case errors.Is(err, store.ErrNotFound):
		return RefDetail{}, ErrNotFound
	default:
		return RefDetail{}, err
	}
}

// issueDetail assembles an issue thread.
//
// `from` is the SENDER: the recipient is already in the reference, repeating it would teach
// nothing. `messages_total` is only emitted when it exceeds the number of messages rendered —
// otherwise it repeats `len(messages)`, and a piece of information deducible from the rest ends
// up diverging.
func (s *service) issueDetail(ctx context.Context, teamID uuid.UUID, issue store.Issue) (RefDetail, error) {
	messages, total, err := s.store.IssueMessages(ctx, teamID, issue.ID, maxMessages)
	if err != nil {
		return RefDetail{}, err
	}

	detail := RefDetail{
		Kind:      "issue",
		Ref:       ref(issue.ProjectKey, issue.Number),
		From:      issue.AuthorProjectKey,
		State:     issue.State,
		Title:     issue.Title,
		CreatedAt: issue.CreatedAt,
		UpdatedAt: issue.UpdatedAt,
		Messages:  make([]Message, 0, len(messages)),
	}
	for _, m := range messages {
		detail.Messages = append(detail.Messages, Message{
			From:      m.AuthorKey,
			CreatedAt: m.CreatedAt,
			Body:      m.BodyMd,
		})
	}
	if int(total) > len(messages) {
		detail.MessagesTotal = int(total)
	}
	return detail, nil
}

// taskDetail assembles a task and its progress notes.
//
// The body is rendered WHOLE, unlike the rows of the overview screen: this is the only place in
// the product where a human reads what an agent wrote without its repo's token, and a truncation
// there would lose exactly the information they came for.
func (s *service) taskDetail(ctx context.Context, teamID uuid.UUID, task store.Task) (RefDetail, error) {
	notes, total, err := s.store.TaskNotes(ctx, teamID, task.ID, maxNotes)
	if err != nil {
		return RefDetail{}, err
	}

	detail := RefDetail{
		Kind:      "task",
		Ref:       ref(task.ProjectKey, task.Number),
		Status:    task.Status,
		Priority:  task.Priority,
		Title:     task.Title,
		Body:      task.BodyMd,
		CreatedAt: task.CreatedAt,
		UpdatedAt: task.UpdatedAt,
		Deadline:  task.Deadline,
		Notes:     make([]Note, 0, len(notes)),
	}
	for _, n := range notes {
		detail.Notes = append(detail.Notes, Note{CreatedAt: n.CreatedAt, Body: n.BodyMd})
	}
	if int(total) > len(notes) {
		detail.NotesTotal = int(total)
	}
	return detail, nil
}
