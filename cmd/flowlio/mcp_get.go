package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément           | Résumé                                                       | Ligne |
// |-------------------|--------------------------------------------------------------|-------|
// | mcpServer.get     | Résout une référence, qu'elle désigne une tâche ou une issue   | 51    |
// | refResponse       | What /api/ref answers: the kind, then exactly one payload      | 101   |
// | mcpServer.refPath | Compose le chemin d'API d'une référence                        | 113   |
// | getTaskResult     | Réponse de get(ref) sur une tâche, champs ordonnés             | 126   |
// | getIssueResult    | Réponse de get(ref) sur une issue, rappel de lecture en tête   | 134   |
//
// Fin du sommaire.
// =====================================================================
//
// `get` est le seul outil POLYMORPHE de la surface MCP, et le seul qui rende des corps de message
// COMPLETS écrits par un autre dépôt. Ces deux propriétés en font un fichier à part : la première
// lui donne une logique de résolution que ne partagent ni les outils de tâche ni ceux d'issue, la
// seconde en fait le point d'entrée le plus exposé du produit.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	issueservice "github.com/Coddyum/flowlio-agents/internal/feature/issue/service"
	taskservice "github.com/Coddyum/flowlio-agents/internal/feature/task/service"
)

// refAPI est le préfixe de l'API de résolution de référence.
const refAPI = "/api/ref"

// get résout une référence, qu'elle désigne une tâche ou une issue.
//
// Le compteur du projet est partagé entre les deux : un agent qui lit CORE-34 dans un commit,
// une inbox ou un message d'issue ne SAIT PAS laquelle des deux c'est. Deux outils typés
// échoueraient donc une fois sur deux — cet outil résout et dit ce qu'il a trouvé.
//
// ONE HTTP CALL, AND THAT IS THE WHOLE POINT OF FLWL-16. This tool used to try the task route,
// read its 404, then try the issue route — two round trips on the path check_inbox feeds, which
// is to say on the most-called read of the product. The choice between the two now happens INSIDE
// the API, in `internal/feature/ref`, where it costs a query instead of a request.
//
// The trap that was avoided before and stays avoided lives there too: only "found nothing" falls
// through from task to issue. Any other failure is definitive — retrying would hide an outage
// behind an "unknown reference", and an agent would conclude its reference does not exist.
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

	var resolved refResponse
	if err := s.api.Do(ctx, http.MethodGet, s.refPath(projectKey, number), nil, &resolved); err != nil {
		return nil, err
	}

	switch {
	case resolved.Kind == "task" && resolved.Task != nil:
		return getTaskResult{Kind: resolved.Kind, Ref: resolved.Ref, Task: *resolved.Task}, nil

	case resolved.Kind == "issue" && resolved.Issue != nil:
		// C'est le seul outil qui rend des corps de message COMPLETS, écrits par un autre dépôt et
		// versés dans un contexte qui a un shell. Chaque prise de parole du pair est encadrée, la
		// mienne ne l'est pas — voir mcp_untrusted.go.
		f, err := newFraming(s.projectKey)
		if err != nil {
			return nil, err
		}
		return getIssueResult{
			Kind:    resolved.Kind,
			Ref:     resolved.Ref,
			Reading: f.notice(),
			Issue:   f.markIssueDetail(*resolved.Issue),
		}, nil
	}

	// An answer this layer cannot name is reported, never guessed at. Rendering a payload under
	// the wrong kind would hand an agent an unframed issue body — the one thing this file exists
	// to make impossible.
	return nil, fmt.Errorf("réponse de résolution inattendue pour %s-%d (kind=%q)",
		projectKey, number, resolved.Kind)
}

// refResponse is what /api/ref answers: the kind, the reference, and exactly one payload.
//
// The two payloads are TYPED here although the API carries them as raw JSON. This side is allowed
// to name them — the CLI is not a feature, so importing both services costs nothing. The API
// cannot: see the note on the resolver interfaces in internal/core/module/module.go.
type refResponse struct {
	Kind  string                    `json:"kind"`
	Ref   string                    `json:"ref"`
	Task  *taskservice.TaskDetail   `json:"task"`
	Issue *issueservice.IssueDetail `json:"issue"`
}

// refPath compose le chemin d'API d'une référence.
//
// La clé de projet est TOUJOURS envoyée, même pour un numéro nu : splitRef y a déjà substitué
// celle de l'appelant, et l'API la compare au projet du token pour décider si une tâche est
// seulement envisageable.
func (s *mcpServer) refPath(projectKey string, number int64) string {
	return refAPI + "/" + url.PathEscape(projectKey) + "/" + strconv.FormatInt(number, 10)
}

// getTaskResult et getIssueResult fixent l'ORDRE des champs de get(ref).
//
// Une map[string]any était sérialisée par ordre ALPHABÉTIQUE de clé — donc `issue` avant
// `lecture`. Sur le seul outil qui rend des corps de message complets, l'agent lisait jusqu'à
// plusieurs centaines de kilo-octets de texte tiers AVANT d'apprendre quel sceau fait foi. Une
// struct place le rappel devant le contenu qu'il cadre, à coût de zéro octet.
//
// Les deux branches sont traitées ensemble : `kind` et `ref` d'abord, pour que l'agent sache ce
// qu'il lit avant de le lire, quelle que soit la nature de la référence.
type getTaskResult struct {
	Kind string                 `json:"kind"`
	Ref  string                 `json:"ref"`
	Task taskservice.TaskDetail `json:"task"`
}

// getIssueResult porte en plus le rappel de lecture : une issue contient du texte écrit par un
// pair, une tâche non.
type getIssueResult struct {
	Kind    string                   `json:"kind"`
	Ref     string                   `json:"ref"`
	Reading string                   `json:"reading"`
	Issue   issueservice.IssueDetail `json:"issue"`
}
