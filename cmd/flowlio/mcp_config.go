package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                         | Ligne |
// |------------------|----------------------------------------------------------------|-------|
// | serverEntry      | Déclaration d'un serveur MCP, telle qu'attendue par les agents   | 47    |
// | writeMCPConfig   | Écrit ou complète le .mcp.json du dépôt, sans jamais de secret   | 57    |
// | flowlioEntry     | Compose la déclaration du serveur flowlio                        | 108   |
//
// Fin du sommaire.
// =====================================================================
//
// LE SECRET N'ENTRE JAMAIS DANS CE FICHIER. C'est la seule règle qui compte ici.
//
// `.mcp.json` vit à la racine du dépôt, et les utilisateurs le commitent — c'est même son
// intérêt : toute l'équipe et tous les agents partagent la même configuration. Y écrire un token
// reviendrait à publier des identifiants sur GitHub à l'échelle de tous les utilisateurs du
// produit. La valeur écrite est donc TOUJOURS la référence `${FLOWLIO_TOKEN}`, que l'agent
// résout depuis son environnement.
//
// Le fichier existant est COMPLÉTÉ, jamais remplacé : un dépôt a souvent déjà des serveurs MCP
// déclarés, et les écraser pour installer le nôtre serait un dégât silencieux. Une entrée
// « flowlio » déjà présente est laissée telle quelle — elle a pu être ajustée à la main.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// mcpConfigName est le nom reconnu par les agents (Claude Code, Codex, OpenCode).
	mcpConfigName = ".mcp.json"
	// mcpServerKey nomme notre entrée dans le fichier.
	mcpServerKey = "flowlio"
	// tokenReference est la seule valeur admissible pour le token : une référence, pas un secret.
	tokenReference = "${FLOWLIO_TOKEN}"

	mcpConfigPerm = 0o644
)

// serverEntry est la déclaration d'un serveur MCP. Les autres entrées du fichier ne sont jamais
// décodées dans cette forme : elles restent du JSON brut, pour être réécrites à l'identique.
type serverEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// writeMCPConfig écrit ou complète le .mcp.json du répertoire donné et dit ce qui a été fait.
//
// Renvoie le chemin écrit et un booléen : faux si une entrée flowlio existait déjà et a été
// conservée. Toute autre entrée du fichier est préservée octet pour octet.
func writeMCPConfig(dir, apiURL string) (path string, written bool, err error) {
	path = filepath.Join(dir, mcpConfigName)

	top := map[string]json.RawMessage{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &top); err != nil {
			return path, false, fmt.Errorf("%s existe et n'est pas un JSON lisible : %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return path, false, fmt.Errorf("lecture de %s : %w", path, err)
	}

	servers := map[string]json.RawMessage{}
	if existing, found := top["mcpServers"]; found {
		if err := json.Unmarshal(existing, &servers); err != nil {
			return path, false, fmt.Errorf("%s : mcpServers illisible : %w", path, err)
		}
	}

	// Une entrée déjà présente a pu être ajustée à la main : on ne la touche pas.
	if _, found := servers[mcpServerKey]; found {
		return path, false, nil
	}

	entry, err := json.Marshal(flowlioEntry(apiURL))
	if err != nil {
		return path, false, fmt.Errorf("encodage de l'entrée %s : %w", mcpServerKey, err)
	}
	servers[mcpServerKey] = entry

	encoded, err := json.Marshal(servers)
	if err != nil {
		return path, false, fmt.Errorf("encodage de mcpServers : %w", err)
	}
	top["mcpServers"] = encoded

	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return path, false, fmt.Errorf("encodage de %s : %w", path, err)
	}
	if err := os.WriteFile(path, append(out, '\n'), mcpConfigPerm); err != nil {
		return path, false, fmt.Errorf("écriture de %s : %w", path, err)
	}
	return path, true, nil
}

// flowlioEntry compose la déclaration du serveur. Le token est une RÉFÉRENCE d'environnement :
// ce fichier est fait pour être commité, il ne doit jamais porter de secret.
func flowlioEntry(apiURL string) serverEntry {
	return serverEntry{
		Command: "flowlio",
		Args:    []string{"mcp"},
		Env: map[string]string{
			"FLOWLIO_API_URL": apiURL,
			"FLOWLIO_TOKEN":   tokenReference,
		},
	}
}
