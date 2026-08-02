package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                 | Résumé                                                | Ligne |
// |-------------------------|-------------------------------------------------------|-------|
// | mcpServer.listTasks     | Backlog du projet courant                               | 41    |
// | mcpServer.get           | Résout une référence, qu'elle soit tâche ou issue       | 82    |
// | mcpServer.createTask    | Ouvre une tâche et renvoie sa clé                       | 134   |
// | mcpServer.updateTask    | Modifie une tâche, la note, ou l'archive                | 173   |
// | mcpServer.numberFromKey | Résout une clé lisible dans le projet du token          | 237   |
// | mcpServer.taskPath      | Compose le chemin d'API d'une tâche                     | 260   |
// | mcpServer.taskRef       | Compose la référence lisible d'un numéro                | 265   |
// | mcpServer.withRefs      | Ajoute sa référence à chaque tâche d'un listing         | 273   |
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

	issueservice "github.com/Coddyum/flowlio-ia/internal/feature/issue/service"
	taskservice "github.com/Coddyum/flowlio-ia/internal/feature/task/service"
	"github.com/Coddyum/flowlio-ia/internal/pkg/client"
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

// get résout une référence, qu'elle désigne une tâche ou une issue.
//
// Le compteur du projet est partagé entre les deux : un agent qui lit CORE-34 dans un commit,
// une inbox ou un message d'issue ne SAIT PAS laquelle des deux c'est. Deux outils typés
// échoueraient donc une fois sur deux — cet outil essaie les deux et dit ce qu'il a trouvé.
//
// Une référence portant la clé d'un projet frère ne peut désigner qu'une issue : les tâches d'un
// autre repo ne sont accessibles à personne.
func (s *mcpServer) get(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("arguments illisibles")
	}

	projectKey, number, err := splitRef(in.Ref, s.projectKey)
	if err != nil {
		return nil, err
	}

	if projectKey == s.projectKey {
		var detail taskservice.TaskDetail
		err := s.api.Do(ctx, http.MethodGet, s.taskPath(number), nil, &detail)
		if err == nil {
			return map[string]any{
				"kind": "task",
				"ref":  s.taskRef(detail.Number),
				"task": detail,
			}, nil
		}
		// Toute erreur autre qu'une absence est définitive : réessayer en issue masquerait une
		// panne derrière un « introuvable ».
		var apiErr *client.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
			return nil, err
		}
	}

	var issue issueservice.IssueDetail
	if err := s.api.Do(ctx, http.MethodGet, s.issuePath(projectKey, number), nil, &issue); err != nil {
		return nil, err
	}

	// C'est le seul outil qui rend des corps de message COMPLETS, écrits par un autre dépôt et
	// versés dans un contexte qui a un shell. Chaque prise de parole du pair est encadrée, la
	// mienne ne l'est pas — voir mcp_untrusted.go.
	f, err := newFraming(s.projectKey)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"kind":    "issue",
		"ref":     issue.Ref,
		"lecture": f.notice(),
		"issue":   f.markIssueDetail(issue),
	}, nil
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

	// Le patch part avant l'archivage : archiver d'abord rendrait la tâche non modifiable, et le
	// même appel échouerait à moitié. La note suit le même chemin, donc elle est écrite tant que
	// la tâche est encore active.
	var task taskservice.Task
	if in.Title != nil || in.Body != nil || in.Status != nil || in.Priority != nil ||
		in.Deadline != "" || in.ClearDeadline || in.Note != nil {

		payload := taskservice.UpdateTaskInput{
			Title:         in.Title,
			Body:          in.Body,
			Status:        in.Status,
			Priority:      in.Priority,
			ClearDeadline: in.ClearDeadline,
			Note:          in.Note,
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
