package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | service.RefDetail  | Rend le détail d'une référence : issue d'abord, tâche ensuite | 40  |
// | service.issueDetail| Assemble le fil d'une issue et son total de messages         | 72    |
// | service.taskDetail | Assemble une tâche, ses notes et leur total                  | 106   |
//
// Fin du sommaire.
// =====================================================================
//
// POURQUOI L'ISSUE PASSE AVANT LA TÂCHE, ET POURQUOI CE N'EST PAS ARBITRAIRE.
//
// Les numéros d'issue et de tâche vivent dans deux séquences par projet : `CORE-41` peut donc
// désigner les deux. L'ordre tranche la collision, et il la tranche du côté de l'issue parce que
// c'est la référence qu'un agent ÉCRIT à un autre repo — c'est celle qu'un humain aura sous les
// yeux quand il tapera la commande. Une tâche se trouve depuis l'écran d'ensemble, où la ligne
// porte déjà son kind.
//
// Conséquence assumée : une tâche dont le numéro entre en collision avec une issue du même repo
// n'est pas atteignable par cette route. Deux appels, un ordre fixe, aucun paramètre de
// désambiguïsation — c'est le compromis le moins cher, et le seul qui ne demande rien à
// l'utilisateur.

import (
	"context"
	"errors"

	"github.com/Coddyum/flowlio-agents/internal/feature/overview/store"
	"github.com/google/uuid"
)

// RefDetail rend le détail de la référence désignée, dans la team résolue.
//
// Une référence hors de la team est introuvable, exactement comme une référence qui n'existe pas
// — c'est le même ErrNotFound, et le même 404. Rien dans cette réponse ne permet de distinguer
// « CORE-41 n'existe pas » de « CORE-41 existe chez le voisin ».
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

// issueDetail assemble le fil d'une issue.
//
// `from` est l'ÉMETTEUR : le destinataire est déjà dans la référence, le répéter n'apprendrait
// rien. `messages_total` n'est émis que s'il dépasse le nombre de messages rendus — sinon il
// répète `len(messages)`, et une information qui se déduit du reste finit par diverger.
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

// taskDetail assemble une tâche et ses notes de progression.
//
// Le corps est rendu ENTIER, contrairement aux lignes de l'écran d'ensemble : c'est le seul
// endroit du produit où un humain lit ce qu'un agent a écrit sans le token de son repo, et une
// troncature y ferait perdre exactement l'information qu'on est venu chercher.
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
