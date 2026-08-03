package main

// GARANTIE 21 DU TABLEAU DE docs/DESIGN-TUI.md § « Garanties de sécurité ».
//
// Ce que ce fichier verrouille : AUCUN OUTIL MCP NE TOUCHE `/api/overview`.
//
// POURQUOI C'EST LA GARANTIE LA PLUS FACILE À PERDRE. `/api/overview` rend l'état d'une team
// ENTIÈRE, fil des conversations compris. Un agent qui l'atteindrait lirait les questions que ses
// repos frères se posent entre eux — et la promesse d'isolation du produit tomberait EN LECTURE,
// sans qu'un seul test de tenancy ne devienne rouge. La route est admin, donc un token d'agent
// serait refusé aujourd'hui ; mais le jour où un outil « pratique » est câblé sur elle avec le
// token admin du fichier d'identifiants, plus rien ne l'arrête.
//
// POURQUOI UN SCAN DE SOURCE ET PAS UN PARCOURS DE `tools()`. `toolDef` ne porte pas de chemin
// HTTP : celui-ci est choisi dans le corps des appels (mcp_call.go, mcp_task_tools.go,
// mcp_issue_tools.go). Il n'existe donc aucune table à parcourir, et le seul lien mécanique
// possible est le texte du paquet.
//
// `scripts/check-overview-scope.sh` NE COUVRE PAS CE CAS : il refuse les occurrences de
// `Overview` — le nom des queries générées — hors de `internal/feature/overview/`. La chaîne
// `"/api/overview"` est en minuscules et lui échappe entièrement.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MUTATION : ajouter un outil `team_overview` qui appelle `/api/overview` → ce test rouge.
func TestMCPToolsNeverCallOverview(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("lecture du paquet: %v", err)
	}

	scannés := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		source, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("lecture de %s: %v", name, err)
		}
		scannés++

		if strings.Contains(string(source), "/api/overview") {
			t.Errorf("%s atteint /api/overview — la surface de supervision est team-scopée, "+
				"aucun binaire d'agent ne doit pouvoir la lire", name)
		}
	}

	// Sans ce garde, un test qui ne scannerait plus aucun fichier — répertoire déplacé, suffixe
	// changé — passerait pour vert en ne vérifiant rien.
	if scannés == 0 {
		t.Fatal("aucun fichier source scanné : ce test ne mesure plus rien")
	}
}

// La liste des outils est écrite ICI, à la main. Un outil ajouté sans être ajouté à cette liste
// rend ce test rouge — y compris un outil qui n'appellerait pas `/api/overview` mais élargirait
// la surface MCP, qui se paie dans le contexte de l'agent à CHAQUE tour.
//
// C'est le second bout de la garantie : le scan ci-dessus attrape le chemin, celui-ci attrape
// l'outil.
func TestMCPToolSurfaceIsClosed(t *testing.T) {
	attendus := map[string]bool{
		"list_tasks":   true,
		"get":          true,
		"create_task":  true,
		"update_task":  true,
		"create_issue": true,
		"list_issues":  true,
		"answer_issue": true,
		"check_inbox":  true,
	}

	déclarés := tools()
	if len(déclarés) != len(attendus) {
		t.Errorf("tools() expose %d outils, %d attendus — la surface MCP a changé sans que ce "+
			"fichier suive", len(déclarés), len(attendus))
	}

	for _, tool := range déclarés {
		if !attendus[tool.Name] {
			t.Errorf("outil inattendu: %q — tout ajout à la surface MCP se paie à chaque tour "+
				"de chaque session", tool.Name)
		}
	}
}
