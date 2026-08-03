package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                 | Résumé                                                | Ligne |
// |-------------------------|-------------------------------------------------------|-------|
// | mcpServer.listTasks     | Backlog du projet courant                               | 38    |
// | mcpServer.createTask    | Ouvre une tâche et renvoie sa clé                       | 72    |
// | mcpServer.updateTask    | Modifie une tâche, la note, ou l'archive                | 111   |
// | mcpServer.numberFromKey | Résout une clé lisible dans le projet du token          | 178   |
// | mcpServer.taskPath      | Compose le chemin d'API d'une tâche                     | 201   |
// | mcpServer.taskRef       | Compose la référence lisible d'un numéro                | 206   |
// | mcpServer.withRefs      | Ajoute sa référence à chaque tâche d'un listing         | 214   |
//
// Fin du sommaire.
// =====================================================================
//
// Implémentation des outils de tâche. Le projet n'est JAMAIS un paramètre : il vient du token,
// résolu une fois au démarrage. Aucun appel MCP ne peut donc désigner le backlog d'un autre
// projet. Les outils d'issue et d'inbox sont dans mcp_issue_tools.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	taskservice "github.com/Coddyum/flowlio-agents/internal/feature/task/service"
)

const taskAPI = "/api/task"

// listTasks renvoie le backlog du projet courant, chaque tâche portant sa clé lisible.
func (s *mcpServer) listTasks(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Status   string `json:"status"`
		Limit    int    `json:"limit"`
		Archived bool   `json:"archived"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("arguments illisibles")
	}

	query := url.Values{}
	if in.Status != "" {
		query.Set("status", in.Status)
	}
	if in.Limit > 0 {
		query.Set("limit", strconv.Itoa(in.Limit))
	}
	if in.Archived {
		query.Set("archived", "true")
	}

	path := taskAPI + "/"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var tasks []taskservice.Task
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &tasks); err != nil {
		return nil, err
	}
	return s.withRefs(tasks), nil
}

// createTask ouvre une tâche et renvoie sa clé, qui est ce dont l'agent a besoin ensuite.
func (s *mcpServer) createTask(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		Status   string `json:"status"`
		Priority string `json:"priority"`
		Deadline string `json:"deadline"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("arguments illisibles")
	}

	payload := taskservice.CreateTaskInput{
		Title:    in.Title,
		Body:     in.Body,
		Status:   in.Status,
		Priority: in.Priority,
	}
	deadline, err := parseDeadline(in.Deadline)
	if err != nil {
		return nil, err
	}
	payload.Deadline = deadline

	var task taskservice.Task
	if err := s.api.Do(ctx, http.MethodPost, taskAPI+"/", payload, &task); err != nil {
		return nil, err
	}
	return writeResult("task", s.taskRef(task.Number), task), nil
}

// updateTask modifie une tâche, y ajoute une note de progression, ou l'archive.
//
// L'archivage et la note sont des champs de cet outil plutôt que deux outils de plus : ils
// coûteraient leur schéma dans le contexte de chaque tour d'agent pour des actions que personne
// n'appelle sans changer un statut dans le même geste.
//
// La note voyage DANS le patch et non dans un second appel : l'API les écrit en une transaction,
// donc « statut changé, motif perdu » n'est plus un état atteignable.
func (s *mcpServer) updateTask(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Ref           string  `json:"ref"`
		Title         *string `json:"title"`
		Body          *string `json:"body"`
		Status        *string `json:"status"`
		Priority      *string `json:"priority"`
		Deadline      string  `json:"deadline"`
		ClearDeadline bool    `json:"clear_deadline"`
		Note          *string `json:"note"`
		Archive       bool    `json:"archive"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("arguments illisibles")
	}

	number, err := s.numberFromKey(in.Ref)
	if err != nil {
		return nil, err
	}

	// UNE SEULE requête, quoi que l'agent demande.
	//
	// Le statut, la note et l'archivage voyagent dans le même PATCH, donc dans la même
	// transaction. L'archivage était un second aller-retour : une panne entre les deux commitait
	// la note sans archiver, l'agent lisait `api: internal error` et rejouait — ce qui écrivait la
	// note une SECONDE fois. Ce n'est pas de la déduplication (docs/DECISION-idempotence.md reste
	// en vigueur) : c'est une couture non atomique qu'on retire.
	//
	// L'échéance est jugée sur sa forme TAILLÉE, comme dans parseDeadline. Les deux bornes ont
	// divergé un temps — ici `!= ""`, là-bas `TrimSpace(...) == ""` — et une échéance faite de
	// blancs franchissait alors ce garde pour être ignorée juste après : le PATCH partait avec
	// TOUS les champs à nil, l'API rendait la tâche inchangée, et l'agent lisait un succès en
	// croyant avoir posé une échéance. Une échéance faite de blancs est une échéance absente,
	// partout et de la même façon.
	if in.Title == nil && in.Body == nil && in.Status == nil && in.Priority == nil &&
		strings.TrimSpace(in.Deadline) == "" && !in.ClearDeadline && in.Note == nil && !in.Archive {
		return nil, errors.New("aucune modification demandée")
	}

	payload := taskservice.UpdateTaskInput{
		Title:         in.Title,
		Body:          in.Body,
		Status:        in.Status,
		Priority:      in.Priority,
		ClearDeadline: in.ClearDeadline,
		Note:          in.Note,
		Archive:       in.Archive,
	}
	deadline, err := parseDeadline(in.Deadline)
	if err != nil {
		return nil, err
	}
	payload.Deadline = deadline

	var task taskservice.Task
	if err := s.api.Do(ctx, http.MethodPatch, s.taskPath(number), payload, &task); err != nil {
		return nil, err
	}
	return writeResult("task", s.taskRef(task.Number), task), nil
}

// numberFromKey résout une clé lisible en numéro, DANS le projet du token.
//
// Une clé portant le préfixe d'un autre projet est refusée ici, avec un message explicite. Le
// serveur n'a de toute façon aucun moyen de servir un autre projet — le refus dit à l'agent
// pourquoi, au lieu de le laisser interpréter un 404 comme « cette tâche n'existe pas ».
func (s *mcpServer) numberFromKey(key string) (int64, error) {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return 0, errors.New("clé de tâche manquante")
	}

	digits := trimmed
	if prefix, suffix, found := strings.Cut(trimmed, "-"); found {
		if !strings.EqualFold(prefix, s.projectKey) {
			return 0, fmt.Errorf("la clé %s appartient au projet %s ; ce token ne donne accès qu'à %s",
				trimmed, strings.ToUpper(prefix), s.projectKey)
		}
		digits = suffix
	}

	number, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || number < 1 {
		return 0, fmt.Errorf("clé de tâche invalide: %s (attendu %s-34)", trimmed, s.projectKey)
	}
	return number, nil
}

// taskPath compose le chemin d'API d'une tâche.
func (s *mcpServer) taskPath(number int64) string {
	return taskAPI + "/" + strconv.FormatInt(number, 10)
}

// taskRef compose la référence lisible d'un numéro : c'est la seule forme qu'un agent manipule.
func (s *mcpServer) taskRef(number int64) string {
	return fmt.Sprintf("%s-%d", s.projectKey, number)
}

// withRefs ajoute sa référence à chaque tâche d'un listing.
//
// La référence est portée par l'enveloppe et non par Task : l'API travaille sur des numéros et
// ne connaît pas les clés de projet, composer CORE-34 est le rôle de cette couche.
func (s *mcpServer) withRefs(tasks []taskservice.Task) []map[string]any {
	out := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, writeResult("task", s.taskRef(task.Number), task))
	}
	return out
}
