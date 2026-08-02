package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément  | Résumé                                                             | Ligne |
// |----------|--------------------------------------------------------------------|-------|
// | toIssue  | Projette une issue du store en vue API, du point de vue de l'appelant| 28    |
// | toIssues | Projette une liste d'issues                                          | 46    |
// | toMessage| Projette un message du store en vue API                              | 55    |
//
// Fin du sommaire.
// =====================================================================

import (
	"fmt"

	"github.com/Coddyum/flowlio-ia/internal/feature/issue/store"
)

// toIssue projette une issue en vue API, du point de vue du projet appelant.
//
// La référence porte TOUJOURS la clé du destinataire : c'est lui qui possède l'issue et son
// numéro, et c'est cette clé que l'agent devra réutiliser pour répondre.
//
// Role et Peer sont calculés ici plutôt que renvoyés bruts : un agent qui reçoit « auteur : WEB,
// destinataire : CORE » doit encore savoir lequel il est. Lui donner directement « rôle :
// entrante, en face : WEB » supprime un raisonnement, donc une occasion de se tromper.
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
	}
}

// toIssues projette une liste d'issues.
func toIssues(rows []store.Issue) []Issue {
	issues := make([]Issue, 0, len(rows))
	for _, row := range rows {
		issues = append(issues, toIssue(row))
	}
	return issues
}

// toMessage projette un message du store en vue API.
func toMessage(m store.Message) Message {
	return Message{
		Author:    m.AuthorKey,
		Body:      m.Body,
		CreatedAt: m.CreatedAt,
	}
}
