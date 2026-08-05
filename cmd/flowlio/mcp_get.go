package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                          | Ligne |
// |----------------|-----------------------------------------------------------------|-------|
// | mcpServer.get  | Résout une référence, qu'elle désigne une tâche ou une issue      | 38    |
// | getTaskResult  | Réponse de get(ref) sur une tâche, champs ordonnés                | 94    |
// | getIssueResult | Réponse de get(ref) sur une issue, rappel de lecture en tête      | 102   |
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
	"net/http"

	issueservice "github.com/Coddyum/flowlio-agents/internal/feature/issue/service"
	taskservice "github.com/Coddyum/flowlio-agents/internal/feature/task/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

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
			return getTaskResult{Kind: "task", Ref: s.taskRef(detail.Number), Task: detail}, nil
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
	return getIssueResult{
		Kind:    "issue",
		Ref:     issue.Ref,
		Reading: f.notice(),
		Issue:   f.markIssueDetail(issue),
	}, nil
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
