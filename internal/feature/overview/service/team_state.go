package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                    | Ligne |
// |---------------------|-----------------------------------------------------------|-------|
// | service.TeamBySlug  | Résout un slug en identité de team                          | 43    |
// | service.TeamState   | Assemble les repos, leur pouls et la file de dettes         | 64    |
// | mergePulse          | Rattache à chaque repo le dernier appel de ses tokens        | 121   |
// | classifyIssues      | Classe chaque issue en vol en answer ou collect              | 158   |
// | classifyTasks       | Classe chaque tâche en ask ou resume, ou l'omet              | 184   |
// | ref                 | Compose la référence lisible d'une ligne                     | 211   |
//
// Fin du sommaire.
// =====================================================================
//
// L'ÉCRAN RÉPOND À UNE SEULE QUESTION : QUI DOIT QUOI, ET DEPUIS COMBIEN DE TEMPS.
//
// La classification est faite ICI et pas chez le client. Un client qui rejouerait « bloqué sans
// question ouverte ⇒ ask » serait un second endroit où la règle vit, donc le premier à diverger.
//
// TROIS REQUÊTES INDÉPENDANTES, PAS UNE JOINTURE. Compteurs, pouls et dettes viennent de quatre
// lectures distinctes qui ne partagent qu'un `team_id`. C'est délibéré : une jointure ferait
// disparaître le repo qui n'a rien en vol, et c'est la pire panne possible sur cet écran parce
// qu'elle est silencieuse.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/overview/store"
	"github.com/google/uuid"
)

// TeamBySlug résout un slug en identité de team.
//
// C'est le SEUL endroit du module où un identifiant fourni par le client entre. Il ne produit
// qu'un uuid interne, jamais rendu au dehors : le handler s'en sert pour scoper les appels
// suivants et le jette.
func (s *service) TeamBySlug(ctx context.Context, slug string) (Team, error) {
	if slug == "" {
		return Team{}, errors.Join(ErrInvalidInput, errors.New("team manquante"))
	}

	team, err := s.store.TeamBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Team{}, ErrNotFound
		}
		return Team{}, err
	}

	return Team{ID: team.ID, Slug: team.Slug, Name: team.Name}, nil
}

// TeamState assemble l'écran d'ensemble d'une team.
//
// L'horloge est prise UNE fois, au début : `generated_at`, le seuil de dormance et l'âge de
// chaque ligne se réfèrent tous au même instant. Deux appels à time.Now() dans la même réponse
// suffiraient à rendre une tâche dormante de trois secondes selon le champ qu'on lit.
func (s *service) TeamState(ctx context.Context, teamID uuid.UUID) (TeamState, error) {
	if err := requireTeam(teamID); err != nil {
		return TeamState{}, err
	}

	now := time.Now().UTC()

	counters, err := s.store.Projects(ctx, teamID)
	if err != nil {
		return TeamState{}, err
	}
	pulses, err := s.store.LastSeen(ctx, teamID)
	if err != nil {
		return TeamState{}, err
	}
	issues, issueTotal, err := s.store.IssueDebts(ctx, teamID, maxDebts)
	if err != nil {
		return TeamState{}, err
	}
	tasks, taskTotal, err := s.store.TaskDebts(ctx, teamID, now.Add(-staleAfter), maxDebts)
	if err != nil {
		return TeamState{}, err
	}

	// hidden compte d'abord ce que les DEUX bornes SQL ont retenu. Les lignes omises par la
	// classification ne s'y ajoutent pas : une tâche bloquée qui a déjà posé sa question n'est
	// pas une dette cachée, elle est déjà représentée par la ligne `answer` de son destinataire.
	hidden := int(issueTotal) - len(issues) + int(taskTotal) - len(tasks)

	debts := append(classifyIssues(issues), classifyTasks(tasks)...)
	sort.SliceStable(debts, func(i, j int) bool {
		if debts[i].Since.Equal(debts[j].Since) {
			return debts[i].Ref < debts[j].Ref
		}
		return debts[i].Since.Before(debts[j].Since)
	})

	// Seconde coupe : les deux files sont bornées séparément en SQL, leur somme ne l'est pas.
	// Ce qui tombe ici est caché au même titre que ce que le LIMIT a retenu, donc compté pareil.
	if len(debts) > maxDebts {
		hidden += len(debts) - maxDebts
		debts = debts[:maxDebts]
	}

	return TeamState{
		GeneratedAt: now,
		Projects:    mergePulse(counters, pulses),
		Debts:       debts,
		Truncated:   hidden,
	}, nil
}

// mergePulse rattache à chaque repo le dernier appel authentifié de ses tokens.
//
// La fusion se fait par CLÉ et non par index : les deux lectures ne rendent pas le même nombre de
// lignes, et un repo dont aucun token n'a jamais servi n'apparaît pas du tout dans le pouls. Son
// horodatage reste alors absent, ce qui est l'information juste.
func mergePulse(counters []store.ProjectCounters, pulses []store.ProjectPulse) []ProjectLine {
	seen := make(map[string]time.Time, len(pulses))
	for _, p := range pulses {
		seen[p.Key] = p.LastSeen
	}

	out := make([]ProjectLine, 0, len(counters))
	for _, c := range counters {
		line := ProjectLine{
			Key:            c.Key,
			OwesAnswer:     c.OwesAnswer,
			AwaitingAnswer: c.AwaitingAnswer,
			AnsweredUnread: c.AnsweredUnread,
			TasksRunning:   c.TasksRunning,
			TasksBlocked:   c.TasksBlocked,
		}
		if at, ok := seen[c.Key]; ok {
			line.LastAgentSeenAt = &at
		}
		out = append(out, line)
	}
	return out
}

// classifyIssues classe chaque issue en vol.
//
// Le débiteur change de camp selon l'état, et c'est tout l'intérêt de l'écran :
//
//	open     → le DESTINATAIRE doit une réponse       (kind answer)
//	answered → l'ÉMETTEUR doit aller la chercher      (kind collect)
//
// La référence, elle, ne bouge pas : c'est toujours celle du destinataire, parce que c'est celle
// qu'on retape pour ouvrir le fil.
//
// Tout autre état est ignoré silencieusement — la query exclut déjà `closed`, et un état inconnu
// est un état ajouté par une migration future, qu'il vaut mieux ne pas rendre du tout que rendre
// avec un kind inventé.
func classifyIssues(issues []store.IssueDebt) []Debt {
	out := make([]Debt, 0, len(issues))
	for _, i := range issues {
		debt := Debt{Ref: ref(i.ProjectKey, i.Number), Title: i.Title, Since: i.UpdatedAt}

		switch i.State {
		case "open":
			debt.Kind, debt.Debtor, debt.Peer = KindAnswer, i.ProjectKey, i.AuthorProjectKey
		case "answered":
			debt.Kind, debt.Debtor, debt.Peer = KindCollect, i.AuthorProjectKey, i.ProjectKey
		default:
			continue
		}
		out = append(out, debt)
	}
	return out
}

// classifyTasks classe chaque tâche rendue par la query, ou l'omet.
//
//	blocked + aucune question ouverte → ask     : le seul cul-de-sac que rien d'autre ne montre
//	blocked + une question ouverte    → OMISE   : déjà représentée par la ligne answer du voisin
//	in_progress                       → resume  : la query ne l'a rendue que si elle est dormante
//
// Aucun seuil n'est réévalué ici : la query a déjà appliqué `last_move < @stale_before`. Le
// refaire en Go serait un second endroit où le seuil vit.
func classifyTasks(tasks []store.TaskDebt) []Debt {
	out := make([]Debt, 0, len(tasks))
	for _, t := range tasks {
		debt := Debt{
			Ref:    ref(t.ProjectKey, t.Number),
			Debtor: t.ProjectKey,
			Title:  t.Title,
			Since:  t.LastMove,
		}

		switch {
		case t.Status == "blocked" && t.HasOpenQuestion:
			continue
		case t.Status == "blocked":
			debt.Kind = KindAsk
		case t.Status == "in_progress":
			debt.Kind = KindResume
		default:
			continue
		}
		out = append(out, debt)
	}
	return out
}

// ref compose la référence lisible d'une ligne — `CORE-41`. C'est la seule désignation qu'un
// humain manipule sur cette surface, et la seule que le détail accepte en retour.
func ref(projectKey string, number int64) string {
	return fmt.Sprintf("%s-%d", projectKey, number)
}
