package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                | Résumé                                                  | Ligne |
// |------------------------|---------------------------------------------------------|-------|
// | mcpServer.createIssue  | Pose une question à un projet frère                       | 40    |
// | mcpServer.listIssues   | Les questions échangées avec les projets frères           | 61    |
// | mcpServer.answerIssue  | Ajoute un message au fil d'une issue, et la clôt          | 107   |
// | mcpServer.checkInbox   | Ce qui attend l'agent, sans aucun paramètre               | 143   |
// | mcpServer.issuePath    | Compose le chemin d'API d'une issue                       | 157   |
// | splitRef               | Découpe CORE-34 en clé de projet et numéro                | 167   |
//
// Fin du sommaire.
// =====================================================================
//
// Les issues sont le cœur du produit : deux repos qui se parlent sans que l'humain serve de
// messager. Une référence porte TOUJOURS la clé du projet destinataire, qui possède l'issue.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	inboxservice "github.com/Coddyum/flowlio-agents/internal/feature/inbox/service"
	issueservice "github.com/Coddyum/flowlio-agents/internal/feature/issue/service"
)

const (
	issueAPI = "/api/issue"
	inboxAPI = "/api/inbox"
)

// createIssue pose une question à un projet frère de la team.
func (s *mcpServer) createIssue(ctx context.Context, args json.RawMessage) (any, error) {
	var in issueservice.CreateIssueInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("arguments illisibles")
	}

	in.ToProject = strings.ToUpper(strings.TrimSpace(in.ToProject))
	if in.ToProject == s.projectKey {
		return nil, fmt.Errorf(
			"une question à son propre projet (%s) est une tâche — utiliser create_task",
			s.projectKey)
	}

	var issue issueservice.Issue
	if err := s.api.Do(ctx, http.MethodPost, issueAPI+"/", in, &issue); err != nil {
		return nil, err
	}
	return writeResult("issue", issue.Ref, issue), nil
}

// listIssues renvoie les questions échangées avec les projets frères.
func (s *mcpServer) listIssues(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Role   string `json:"role"`
		State  string `json:"state"`
		Limit  int    `json:"limit"`
		Closed bool   `json:"closed"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("arguments illisibles")
	}

	query := url.Values{}
	if in.Role != "" {
		query.Set("role", in.Role)
	}
	if in.State != "" {
		query.Set("state", in.State)
	}
	if in.Limit > 0 {
		query.Set("limit", strconv.Itoa(in.Limit))
	}
	if in.Closed {
		query.Set("closed", "true")
	}

	path := issueAPI + "/"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var issues []issueservice.Issue
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &issues); err != nil {
		return nil, err
	}

	// Le titre d'une issue entrante est écrit par le pair, et 200 caractères suffisent à loger
	// une consigne. Un listing ne porte pas de rappel de lecture : une ligne par issue, la
	// consigne complète est déjà dans les instructions de session.
	f, err := newFraming(s.projectKey)
	if err != nil {
		return nil, err
	}
	return f.markIssues(issues), nil
}

// answerIssue ajoute un message au fil d'une issue, et la clôt si demandé.
func (s *mcpServer) answerIssue(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Ref   string `json:"ref"`
		Body  string `json:"body"`
		Close bool   `json:"close"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("arguments illisibles")
	}

	projectKey, number, err := splitRef(in.Ref, s.projectKey)
	if err != nil {
		return nil, err
	}

	payload := issueservice.AnswerInput{Body: in.Body, Close: in.Close}
	var issue issueservice.Issue
	path := s.issuePath(projectKey, number) + "/answer"
	if err := s.api.Do(ctx, http.MethodPost, path, payload, &issue); err != nil {
		return nil, err
	}

	// Répondre à une issue ENTRANTE en renvoie le titre, écrit par le pair : il est balisé comme
	// partout ailleurs. L'enveloppe d'écriture reste {ref, objet} — pas de rappel de lecture ici,
	// l'agent vient d'écrire, il ne découvre rien.
	f, err := newFraming(s.projectKey)
	if err != nil {
		return nil, err
	}
	return writeResult("issue", issue.Ref, f.markIssue(issue)), nil
}

// checkInbox renvoie ce qui attend l'agent. Aucun paramètre : tout vient du token.
//
// C'est le premier appel d'une session, donc le premier endroit où du texte écrit par un autre
// dépôt entre dans le contexte de l'agent. Tout ce que le pair a écrit y est balisé.
func (s *mcpServer) checkInbox(ctx context.Context, _ json.RawMessage) (any, error) {
	var inbox inboxservice.Inbox
	if err := s.api.Do(ctx, http.MethodGet, inboxAPI+"/", nil, &inbox); err != nil {
		return nil, err
	}

	f, err := newFraming(s.projectKey)
	if err != nil {
		return nil, err
	}
	return inboxResult{Lecture: f.notice(), Inbox: f.markInbox(inbox)}, nil
}

// issuePath compose le chemin d'API d'une issue.
func (s *mcpServer) issuePath(projectKey string, number int64) string {
	return issueAPI + "/" + url.PathEscape(projectKey) + "/" + strconv.FormatInt(number, 10)
}

// splitRef découpe CORE-34 en clé de projet et numéro.
//
// Contrairement aux tâches, la clé peut désigner un projet frère : une issue appartient à son
// destinataire, qui n'est pas toujours l'appelant. Un numéro nu désigne le projet courant.
// Le contrôle d'accès n'est PAS fait ici — il est dans la query : une référence pointant une
// conversation à laquelle l'appelant ne participe pas remonte simplement « introuvable ».
func splitRef(ref, defaultKey string) (string, int64, error) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "", 0, errors.New("référence manquante")
	}

	projectKey, digits := defaultKey, trimmed
	if prefix, suffix, found := strings.Cut(trimmed, "-"); found {
		// Un préfixe vide (« -34 ») produirait un chemin d'API avec un segment vide, donc une
		// route qui ne correspond à rien : le refuser ici donne un message utile.
		if prefix == "" {
			return "", 0, fmt.Errorf("référence invalide: %s (attendu %s-34)", trimmed, defaultKey)
		}
		projectKey, digits = strings.ToUpper(prefix), suffix
	}

	number, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || number < 1 {
		return "", 0, fmt.Errorf("référence invalide: %s (attendu %s-34)", trimmed, defaultKey)
	}
	return projectKey, number, nil
}
