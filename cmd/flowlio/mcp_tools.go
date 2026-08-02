package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                       | Ligne |
// |-----------------|--------------------------------------------------------------|-------|
// | toolDef         | Définition d'un outil MCP telle qu'annoncée au client          | 50    |
// | object          | Construit un schéma JSON d'objet                               | 57    |
// | prop            | Construit une propriété de schéma JSON                         | 69    |
// | enumProp        | Construit une propriété contrainte à un jeu de valeurs         | 78    |
// | tools           | Les huit outils exposés, et rien de plus                        | 87    |
// | toolsListResult | Réponse de tools/list                                          | 214   |
//
// Fin du sommaire.
// =====================================================================
//
// La surface MCP est un BUDGET, pas une liste de souhaits : chaque outil est réinjecté dans le
// contexte de l'agent à CHAQUE tour. Huit outils, des descriptions courtes, aucun paramètre
// décoratif. Tout ajout ici se paie sur toutes les sessions, indéfiniment.
//
// Ce que ces outils n'exposent volontairement pas :
//   - le projet : il vient du token, jamais d'un paramètre. Il n'existe donc aucun appel MCP
//     capable de désigner le backlog d'un autre projet.
//   - les UUID : un agent travaille sur des clés lisibles (CORE-34).
//   - la suppression : une tâche s'archive, l'historique d'un repo ne s'efface pas.
//   - whoami : son contenu est constant sur la vie du token, donc il est injecté dans les
//     instructions d'initialize. Zéro schéma, zéro tour, et l'information est dans le contexte
//     de l'agent avant son premier message.
//   - add_task_note : replié dans update_task en champ `note`. L'intention réelle est « passer
//     en done ET dire pourquoi », donc un seul appel, une seule transaction. Un outil de plus
//     aurait coûté son schéma à chaque tour pour ne rien ajouter.
//
// Tout retour d'ÉCRITURE a la même forme, {ref, task} ou {ref, issue} : un agent lit la
// référence au même endroit quel que soit l'outil qu'il vient d'appeler, au lieu de la deviner.
//
// `get` n'est pas typé (get_task / get_issue) parce que tâches et issues partagent le compteur du
// projet : l'agent qui lit CORE-34 dans un commit ou une inbox ne sait PAS laquelle des deux
// c'est. Deux outils typés échoueraient donc une fois sur deux.

// statuts et priorités reprennent exactement le vocabulaire du serveur. Les redire ici les rend
// visibles dans le schéma, donc dans le contexte de l'agent : il n'a pas à deviner ni à échouer
// une fois pour apprendre.
var (
	taskStatuses   = []string{"todo", "in_progress", "blocked", "done"}
	taskPriorities = []string{"low", "normal", "high", "urgent"}
	issueStates    = []string{"open", "answered", "closed"}
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

// tools est la surface exposée. Huit outils, arbitrés dans docs/DESIGN-M3.md.
func tools() []toolDef {
	return []toolDef{
		{
			Name: "list_tasks",
			Description: "Backlog du projet courant, de la tâche la plus récente à la plus " +
				"ancienne. La description n'est pas incluse : utiliser get pour la lire.",
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
			Name: "get",
			Description: "Le détail de ce que désigne une référence : une tâche avec son fil de " +
				"notes, ou une issue avec son fil de messages. Le champ kind dit laquelle. " +
				"C'est ce qu'on lit pour reprendre un sujet.",
			InputSchema: object(map[string]any{
				"ref": prop("string",
					"Référence, par exemple CORE-34. Le numéro seul désigne le projet courant."),
			}, "ref"),
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
			Description: "Modifie une tâche du projet courant, et note au passage ce qui a été " +
				"fait. Seuls les champs fournis changent ; les autres restent en l'état. " +
				"ref + note seuls suffisent : c'est ainsi qu'on laisse une trace sans rien " +
				"changer d'autre. Une tâche archivée n'est plus modifiable.",
			InputSchema: object(map[string]any{
				"ref": prop("string",
					"Référence de la tâche, par exemple CORE-34. Le numéro seul est accepté."),
				"title":    prop("string", "Nouveau titre."),
				"body":     prop("string", "Nouvelle description en markdown."),
				"status":   enumProp(taskStatuses, "Nouveau statut."),
				"priority": enumProp(taskPriorities, "Nouvelle priorité."),
				"deadline": prop("string", "Nouvelle échéance au format RFC 3339."),
				"clear_deadline": prop("boolean",
					"Efface l'échéance. Nécessaire parce qu'un champ absent signifie déjà « ne change pas »."),
				"note": prop("string",
					"Note de progression ajoutée au fil, en markdown. C'est la trace que relira "+
						"la session suivante : y écrire ce qui a été fait et ce qui reste. "+
						"Écrite avec le reste du changement, ou pas du tout. Seule avec ref, "+
						"elle remonte la tâche en tête de ce qui est en cours."),
				"archive": prop("boolean",
					"Sort la tâche du backlog actif. Elle reste lisible, avec ses notes. "+
						"Écrit avec le statut et la note dans le même appel : « passe en done, "+
						"voilà pourquoi, et archive » est une seule opération."),
			}, "ref"),
		},
		{
			Name: "create_issue",
			Description: "Pose une question à un projet frère de la team et renvoie sa " +
				"référence. À utiliser quand la réponse ne peut venir que de l'autre repo — " +
				"pour son propre travail, ouvrir une tâche.",
			InputSchema: object(map[string]any{
				"to_project": prop("string",
					"Clé du projet destinataire, par exemple CORE."),
				"title": prop("string",
					"La question en une ligne, 200 caractères au plus."),
				"body": prop("string",
					"Le contexte complet : ce qui est attendu, et ce qui a déjà été essayé."),
			}, "to_project", "title", "body"),
		},
		{
			Name: "list_issues",
			Description: "Les questions échangées avec les projets frères : celles qui vous " +
				"sont adressées et celles que vous avez posées. Les closes sont exclues " +
				"par défaut.",
			InputSchema: object(map[string]any{
				"role": enumProp([]string{"incoming", "outgoing"},
					"incoming : ce qu'on attend de vous. outgoing : ce que vous attendez. "+
						"Omis : les deux."),
				"state": enumProp(issueStates, "Ne renvoyer que les issues dans cet état."),
				"limit": map[string]any{
					"type":        "integer",
					"description": "Nombre maximum d'issues (défaut 20, maximum 100).",
					"minimum":     1,
					"maximum":     100,
				},
				"closed": prop("boolean", "Inclure les issues closes. Exclues par défaut."),
			}),
		},
		{
			Name: "answer_issue",
			Description: "Ajoute un message au fil d'une issue, et la clôt si close vaut vrai. " +
				"C'est le seul moyen de répondre comme de fermer. Un message est obligatoire " +
				"même pour clore : sans motif, le correspondant ne sait pas pourquoi.",
			InputSchema: object(map[string]any{
				"ref": prop("string",
					"Référence de l'issue, par exemple CORE-34."),
				"body":  prop("string", "Le message, en markdown."),
				"close": prop("boolean", "Clôt l'issue. La clôture est définitive."),
			}, "ref", "body"),
		},
		{
			Name: "check_inbox",
			Description: "Ce qui vous attend : les questions entrantes à traiter, vos questions " +
				"qui ont reçu une réponse, et vos tâches en cours. Aucun paramètre. " +
				"À appeler en début de session. L'état de référence reste list_issues et " +
				"list_tasks : cet appel est un point de départ, pas un inventaire complet.",
			InputSchema: object(map[string]any{}),
		},
	}
}

// toolsListResult est la réponse de tools/list.
func toolsListResult() map[string]any {
	return map[string]any{"tools": tools()}
}
