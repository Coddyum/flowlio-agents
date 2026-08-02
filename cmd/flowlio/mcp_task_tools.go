package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                 | Résumé                                                | Ligne |
// |-------------------------|-------------------------------------------------------|-------|
// | mcpServer.whoami        | Identité du token courant                               | 30    |
// | mcpServer.listTasks     | Backlog du projet courant                               | 30    |
// | mcpServer.getTask       | Une tâche et son fil de notes                           | 30    |
// | mcpServer.createTask    | Ouvre une tâche et renvoie sa clé                       | 30    |
// | mcpServer.updateTask    | Modifie une tâche, ou l'archive                         | 30    |
// | mcpServer.addNote       | Ajoute une note de progression                          | 30    |
// | mcpServer.parseKey      | Lit un argument key et le résout en numéro              | 30    |
// | mcpServer.numberFromKey | Résout une clé lisible dans le projet du token          | 30    |
// | mcpServer.taskPath      | Compose le chemin d'API d'une tâche                     | 30    |
// | mcpServer.taskKey       | Compose la clé lisible d'un numéro                      | 30    |
// | mcpServer.withKeys      | Ajoute sa clé lisible à chaque tâche d'un listing       | 30    |
//
// Fin du sommaire.
// =====================================================================
//
// Implémentation des six outils. Le projet n'est JAMAIS un paramètre : il vient du token, résolu
// une fois au démarrage. Aucun appel MCP ne peut donc désigner le backlog d'un autre projet.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	taskservice "github.com/Coddyum/flowlio-ia/internal/feature/task/service"
	"github.com/Coddyum/flowlio-ia/internal/feature/workspace/service"
)

const taskAPI = "/api/task"

// whoami renvoie l'identité du token courant.
func (s *mcpServer) whoami(ctx context.Context) (any, error) {
	var out struct {
		Scope string `json:"scope"`
		service.Identity
	}
	if err := s.api.Do(ctx, http.MethodGet, workspaceAPI+"/whoami", nil, &out); err != nil {
		return nil, err
	}
	return map[string]any{
		"team":    out.TeamSlug,
		"project": out.ProjectKey,
		"name":    out.ProjectName,
		"scope":   out.Scope,
	}, nil
}

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
	return s.withKeys(tasks), nil
}

// getTask renvoie une tâche et son fil de notes.
func (s *mcpServer) getTask(ctx context.Context, args json.RawMessage) (any, error) {
	number, err := s.parseKey(args)
	if err != nil {
		return nil, err
	}

	var detail taskservice.TaskDetail
	if err := s.api.Do(ctx, http.MethodGet, s.taskPath(number), nil, &detail); err != nil {
		return nil, err
	}
	return map[string]any{"key": s.taskKey(detail.Number), "task": detail}, nil
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
	return map[string]any{"key": s.taskKey(task.Number), "task": task}, nil
}

// updateTask modifie une tâche, ou l'archive.
//
// L'archivage est un drapeau de cet outil plutôt qu'un septième outil : il coûterait un outil de
// plus dans le contexte de chaque tour d'agent pour une action que personne n'appelle sans
// d'abord passer la tâche en done.
func (s *mcpServer) updateTask(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Key           string  `json:"key"`
		Title         *string `json:"title"`
		Body          *string `json:"body"`
		Status        *string `json:"status"`
		Priority      *string `json:"priority"`
		Deadline      string  `json:"deadline"`
		ClearDeadline bool    `json:"clear_deadline"`
		Archive       bool    `json:"archive"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("arguments illisibles")
	}

	number, err := s.numberFromKey(in.Key)
	if err != nil {
		return nil, err
	}

	// Le patch part avant l'archivage : archiver d'abord rendrait la tâche non modifiable, et le
	// même appel échouerait à moitié.
	var task taskservice.Task
	if in.Title != nil || in.Body != nil || in.Status != nil || in.Priority != nil ||
		in.Deadline != "" || in.ClearDeadline {

		payload := taskservice.UpdateTaskInput{
			Title:         in.Title,
			Body:          in.Body,
			Status:        in.Status,
			Priority:      in.Priority,
			ClearDeadline: in.ClearDeadline,
		}
		deadline, err := parseDeadline(in.Deadline)
		if err != nil {
			return nil, err
		}
		payload.Deadline = deadline

		if err := s.api.Do(ctx, http.MethodPatch, s.taskPath(number), payload, &task); err != nil {
			return nil, err
		}
	}

	if in.Archive {
		if err := s.api.Do(ctx, http.MethodPost, s.taskPath(number)+"/archive", nil, &task); err != nil {
			return nil, err
		}
	}

	if task.Number == 0 {
		return nil, errors.New("aucune modification demandée")
	}
	return map[string]any{"key": s.taskKey(task.Number), "task": task}, nil
}

// addNote ajoute une note de progression au fil d'une tâche.
func (s *mcpServer) addNote(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Key  string `json:"key"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("arguments illisibles")
	}

	number, err := s.numberFromKey(in.Key)
	if err != nil {
		return nil, err
	}

	var note taskservice.Note
	payload := taskservice.AddNoteInput{Body: in.Body}
	if err := s.api.Do(ctx, http.MethodPost, s.taskPath(number)+"/notes", payload, &note); err != nil {
		return nil, err
	}
	return map[string]any{"key": s.taskKey(number), "note": note}, nil
}

// parseKey lit un argument `key` et le résout en numéro.
func (s *mcpServer) parseKey(args json.RawMessage) (int64, error) {
	var in struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return 0, errors.New("arguments illisibles")
	}
	return s.numberFromKey(in.Key)
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

// taskKey compose la clé lisible d'un numéro : c'est la seule forme qu'un agent manipule.
func (s *mcpServer) taskKey(number int64) string {
	return fmt.Sprintf("%s-%d", s.projectKey, number)
}

// withKeys ajoute sa clé lisible à chaque tâche d'un listing.
func (s *mcpServer) withKeys(tasks []taskservice.Task) []map[string]any {
	out := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, map[string]any{"key": s.taskKey(task.Number), "task": task})
	}
	return out
}

