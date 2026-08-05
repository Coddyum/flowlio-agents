package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                    | Ligne |
// |---------------------|-----------------------------------------------------------|-------|
// | service.TeamBySlug  | Resolves a slug into a team identity                        | 43    |
// | service.TeamState   | Assembles the repos, their pulse and the debt queue         | 64    |
// | mergePulse          | Attaches to each repo the last call of its tokens            | 121   |
// | classifyIssues      | Classifies each issue in flight as answer or collect         | 158   |
// | classifyTasks       | Classifies each task as ask or resume, or omits it           | 184   |
// | ref                 | Composes the readable reference of a row                     | 211   |
//
// Fin du sommaire.
// =====================================================================
//
// THE SCREEN ANSWERS ONE QUESTION: WHO OWES WHAT, AND FOR HOW LONG.
//
// The classification is done HERE and not at the client. A client replaying "blocked with no open
// question ⇒ ask" would be a second place where the rule lives, hence the first to diverge.
//
// THREE INDEPENDENT QUERIES, NOT ONE JOIN. Counters, pulse and debts come from four distinct
// reads that share nothing but a `team_id`. That is deliberate: a join would make the repo with
// nothing in flight disappear, and that is the worst possible failure on this screen because it
// is silent.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/overview/store"
	"github.com/google/uuid"
)

// TeamBySlug resolves a slug into a team identity.
//
// It is the ONLY place in the module where a client-supplied identifier comes in. It produces
// nothing but an internal uuid, never handed outside: the handler uses it to scope the following
// calls and throws it away.
func (s *service) TeamBySlug(ctx context.Context, slug string) (Team, error) {
	if slug == "" {
		return Team{}, errors.Join(ErrInvalidInput, errors.New("missing team"))
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

// TeamState assembles a team's overview screen.
//
// The clock is taken ONCE, at the start: `generated_at`, the dormancy threshold and every row's
// age all refer to the same instant. Two calls to time.Now() in the same response would be enough
// to make a task dormant by three seconds depending on which field is read.
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

	// hidden counts first what the TWO SQL bounds held back. The rows omitted by the
	// classification do not add to it: a blocked task that already asked its question is not a
	// hidden debt, it is already represented by its recipient's `answer` row.
	hidden := int(issueTotal) - len(issues) + int(taskTotal) - len(tasks)

	debts := append(classifyIssues(issues), classifyTasks(tasks)...)
	sort.SliceStable(debts, func(i, j int) bool {
		if debts[i].Since.Equal(debts[j].Since) {
			return debts[i].Ref < debts[j].Ref
		}
		return debts[i].Since.Before(debts[j].Since)
	})

	// Second cut: the two queues are bounded separately in SQL, their sum is not. What falls
	// here is hidden just as much as what the LIMIT held back, so it is counted the same.
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

// mergePulse attaches to each repo the last authenticated call of its tokens.
//
// The merge is done by KEY and not by index: the two reads do not yield the same number of rows,
// and a repo no token of which has ever served does not appear in the pulse at all. Its timestamp
// then stays absent, which is the accurate piece of information.
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

// classifyIssues classifies each issue in flight.
//
// The debtor changes sides depending on the state, and that is the whole point of the screen:
//
//	open     → the RECIPIENT owes an answer            (kind answer)
//	answered → the SENDER must go and fetch it         (kind collect)
//
// The reference itself does not move: it is always the recipient's, because that is the one
// retyped to open the thread.
//
// Any other state is ignored silently — the query already excludes `closed`, and an unknown state
// is a state added by a future migration, better not rendered at all than rendered with a made-up
// kind.
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

// classifyTasks classifies each task yielded by the query, or omits it.
//
//	blocked + no open question → ask     : the only dead end nothing else shows
//	blocked + an open question → OMITTED : already represented by the neighbour's answer row
//	in_progress                → resume  : the query only yielded it if it is dormant
//
// No threshold is re-evaluated here: the query already applied `last_move < @stale_before`. Doing
// it again in Go would be a second place where the threshold lives.
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

// ref composes the readable reference of a row — `CORE-41`. It is the only designation a human
// handles on this surface, and the only one the detail accepts back.
func ref(projectKey string, number int64) string {
	return fmt.Sprintf("%s-%d", projectKey, number)
}
