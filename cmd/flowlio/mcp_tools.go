package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                       | Ligne |
// |-----------------|--------------------------------------------------------------|-------|
// | toolDef         | Définition d'un outil MCP telle qu'annoncée au client          | 36    |
// | object          | Construit un schéma JSON d'objet                               | 43    |
// | prop            | Construit une propriété de schéma JSON                         | 55    |
// | enumProp        | Construit une propriété contrainte à un jeu de valeurs         | 64    |
// | tools           | Les six outils exposés, et rien de plus                        | 73    |
// | toolsListResult | Réponse de tools/list                                          | 153   |
//
// Fin du sommaire.
// =====================================================================
//
// La surface MCP est un BUDGET, pas une liste de souhaits : chaque outil est réinjecté dans le
// contexte de l'agent à CHAQUE tour. Six outils, des descriptions courtes, aucun paramètre
// décoratif. Tout ajout ici se paie sur toutes les sessions, indéfiniment.
//
// Ce que ces outils n'exposent volontairement pas :
//   - le projet : il vient du token, jamais d'un paramètre. Il n'existe donc aucun appel MCP
//     capable de désigner le backlog d'un autre projet.
//   - les UUID : un agent travaille sur des clés lisibles (CORE-34).
//   - la suppression : une tâche s'archive, l'historique d'un repo ne s'efface pas.

// statuts et priorités reprennent exactement le vocabulaire du serveur. Les redire ici les rend
// visibles dans le schéma, donc dans le contexte de l'agent : il n'a pas à deviner ni à échouer
// une fois pour apprendre.
var (
	taskStatuses   = []string{"todo", "in_progress", "blocked", "done"}
	taskPriorities = []string{"low", "normal", "high", "urgent"}
)

// toolDef est la définition d'un outil telle qu'annoncée dans tools/list.
type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// object construit un schéma JSON d'objet. required liste les propriétés obligatoires.
func object(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// prop construit une propriété de schéma JSON.
func prop(kind, description string) map[string]any {
	return map[string]any{
		"type":        kind,
		"description": description,
	}
}

// enumProp construit une propriété contrainte à un jeu de valeurs : l'agent lit les valeurs
// admises dans le schéma plutôt que de les découvrir par une erreur.
func enumProp(values []string, description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"enum":        values,
		"description": description,
	}
}

// tools est la surface exposée. Six outils, décidés dans docs/DESIGN-V1.md.
func tools() []toolDef {
	return []toolDef{
		{
			Name: "whoami",
			Description: "Identité du token courant : team, projet et portée. À appeler en " +
				"début de session pour savoir dans quel repo on travaille.",
			InputSchema: object(map[string]any{}),
		},
		{
			Name: "list_tasks",
			Description: "Backlog du projet courant, de la tâche la plus récente à la plus " +
				"ancienne. La description n'est pas incluse : utiliser get_task pour la lire.",
			InputSchema: object(map[string]any{
				"status": enumProp(taskStatuses, "Ne renvoyer que les tâches de ce statut."),
				"limit": map[string]any{
					"type":        "integer",
					"description": "Nombre maximum de tâches (défaut 50, maximum 200).",
					"minimum":     1,
					"maximum":     200,
				},
				"archived": prop("boolean",
					"Inclure les tâches archivées. Exclues par défaut."),
			}),
		},
		{
			Name: "get_task",
			Description: "Une tâche du projet courant, avec sa description complète et son fil " +
				"de notes de progression. C'est ce qu'on lit pour reprendre une tâche.",
			InputSchema: object(map[string]any{
				"key": prop("string",
					"Clé de la tâche, par exemple CORE-34. Le numéro seul est accepté."),
			}, "key"),
		},
		{
			Name: "create_task",
			Description: "Ouvre une tâche dans le backlog du projet courant et renvoie sa clé. " +
				"Le statut vaut todo et la priorité normal si on ne les précise pas.",
			InputSchema: object(map[string]any{
				"title": prop("string",
					"Titre en une ligne, 200 caractères au plus."),
				"body": prop("string",
					"Description en markdown : le contexte complet nécessaire pour traiter la tâche."),
				"status":   enumProp(taskStatuses, "Statut initial. Défaut : todo."),
				"priority": enumProp(taskPriorities, "Priorité. Défaut : normal."),
				"deadline": prop("string",
					"Échéance au format RFC 3339, par exemple 2026-09-01T12:00:00Z."),
			}, "title"),
		},
		{
			Name: "update_task",
			Description: "Modifie une tâche du projet courant. Seuls les champs fournis " +
				"changent ; les autres restent en l'état. Une tâche archivée n'est plus modifiable.",
			InputSchema: object(map[string]any{
				"key": prop("string",
					"Clé de la tâche, par exemple CORE-34. Le numéro seul est accepté."),
				"title":    prop("string", "Nouveau titre."),
				"body":     prop("string", "Nouvelle description en markdown."),
				"status":   enumProp(taskStatuses, "Nouveau statut."),
				"priority": enumProp(taskPriorities, "Nouvelle priorité."),
				"deadline": prop("string", "Nouvelle échéance au format RFC 3339."),
				"clear_deadline": prop("boolean",
					"Efface l'échéance. Nécessaire parce qu'un champ absent signifie déjà « ne change pas »."),
				"archive": prop("boolean",
					"Sort la tâche du backlog actif. Elle reste lisible, avec ses notes."),
			}, "key"),
		},
		{
			Name: "add_task_note",
			Description: "Ajoute une note de progression au fil d'une tâche. C'est la trace que " +
				"relira la session suivante : y écrire ce qui a été fait et ce qui reste.",
			InputSchema: object(map[string]any{
				"key": prop("string",
					"Clé de la tâche, par exemple CORE-34. Le numéro seul est accepté."),
				"body": prop("string", "La note, en markdown."),
			}, "key", "body"),
		},
	}
}

// toolsListResult est la réponse de tools/list.
func toolsListResult() map[string]any {
	return map[string]any{"tools": tools()}
}
