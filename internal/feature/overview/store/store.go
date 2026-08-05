package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                          | Ligne |
// |-----------------|-----------------------------------------------------------------|-------|
// | Team            | Identity of a team, as the slug resolves it                       | 54    |
// | ProjectCounters | The five counters of a repo                                       | 63    |
// | ProjectPulse    | Last authenticated call of a token of the repo                    | 75    |
// | IssueDebt       | One issue in flight, with no body and no excerpt                  | 86    |
// | TaskDebt        | One task a human can act on                                       | 101   |
// | Issue           | The thread of an issue, seen by a third party                     | 115   |
// | Message         | One message of the thread, designated by its author's key         | 127   |
// | Task            | One task designated by its reference                              | 135   |
// | Note            | One progress note                                                 | 149   |
// | Store           | Team-scoped read contract, with no write at all                   | 160   |
// | store           | Implementation backed by the sqlc-generated queries               | 176   |
// | New             | Creates the overview store                                        | 181   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRACT ONLY — the implementation lives in team.go, projects.go, debts.go, thread.go and
// task.go.
//
// No Transactor, and there will not be one: this surface is READ-ONLY. None of its methods writes
// a row, none can therefore need atomicity. The rule is guarded by scripts/check-overview-scope.sh,
// which rejects any INSERT/UPDATE/DELETE in sql/queries/overview.sql.
//
// THE SIGNATURE INVARIANT IS THE REAL GUARDRAIL. The team slug never goes below the handler:
// every method takes a non-nullable `teamID uuid.UUID` as its first parameter, with the single
// exception of TeamBySlug, which is the one that PRODUCES it. A caller therefore cannot forget a
// scope — it cannot even express one.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// ErrNotFound signals a row that cannot be found in the requested scope — unknown team, or a
// reference that does not belong to the resolved team.
//
// ONE SENTINEL FOR BOTH CASES, deliberately: "this team exists but not for you" and "this team
// does not exist" must be indistinguishable from the outside, otherwise a sweep of slugs
// enumerates the installation's teams.
var ErrNotFound = errors.New("overview store: not found")

// Team is the identity of a team, and nothing more. It will not grow: this shape is the product
// of the one query of the file that is not scoped by team_id.
type Team struct {
	ID   uuid.UUID
	Slug string
	Name string
}

// ProjectCounters carries the five counters of a repo. A row ALWAYS exists, including for a repo
// with nothing in flight: a repo disappearing from the supervisor's screen is the one
// unrecoverable flaw of this surface, they cannot look for what they cannot see.
type ProjectCounters struct {
	Key            string
	OwesAnswer     int64
	AwaitingAnswer int64
	AnsweredUnread int64
	TasksRunning   int64
	TasksBlocked   int64
}

// ProjectPulse is the last authenticated call of a token of the repo. A repo no token of which
// has ever served has no row at all: it has no pulse, which is not the same thing as a pulse at
// zero.
type ProjectPulse struct {
	Key      string
	LastSeen time.Time
}

// IssueDebt is one issue in flight. No body and no excerpt: fifty excerpts do not read in three
// seconds, and the detail is one call away.
//
// ProjectKey is the RECIPIENT, AuthorProjectKey the sender. That pair decides who the debtor is:
// on an `open` issue the recipient owes an answer, on an `answered` issue the sender is the one
// who must go and fetch it.
type IssueDebt struct {
	Number           int64
	State            string
	Title            string
	ProjectKey       string
	AuthorProjectKey string
	UpdatedAt        time.Time
}

// TaskDebt is one task a human can act on.
//
// LastMove is not updated_at: it is the most recent of updated_at and of the last note, without
// which an agent actively documenting its progress would be reported as a "dead session".
// HasOpenQuestion tells "blocked and it asked" apart from "blocked and it asked nothing" — the
// second is the only dead end nothing else in the product makes visible.
type TaskDebt struct {
	Number          int64
	Status          string
	Priority        string
	Title           string
	ProjectKey      string
	Deadline        *time.Time
	LastMove        time.Time
	HasOpenQuestion bool
}

// Issue is the thread of an issue, seen by a third party that is neither its author nor its
// recipient. CreatedAt AND UpdatedAt: "open for 5 days, silent for 3" are two different pieces of
// information for a supervisor.
type Issue struct {
	ID               uuid.UUID
	Number           int64
	State            string
	Title            string
	ProjectKey       string
	AuthorProjectKey string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Message is one message of the thread, designated by the key of the project that wrote it.
type Message struct {
	AuthorKey string
	BodyMd    string
	CreatedAt time.Time
}

// Task is one task designated by its reference, body included: this is the only place in the
// product where a human reads it without the repo's token.
type Task struct {
	ID         uuid.UUID
	Number     int64
	Status     string
	Priority   string
	Title      string
	BodyMd     string
	ProjectKey string
	Deadline   *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Note is one progress note. It is the real answer to "why is this blocked".
type Note struct {
	BodyMd    string
	CreatedAt time.Time
}

// Store reads the state of a whole team. It is the ONLY store of the repo whose reads carry no
// project predicate, and that is why it lives in its own module: sitting a team-only method next
// to a project-scoped one is the setup where copy-paste leaks.
//
// The methods that bound their result also yield the total BEFORE the bound: without it a
// truncated list lies, and the screen is wrong in a silent, credible way.
type Store interface {
	// TeamBySlug is the ONLY method without a teamID: it is the one that produces it.
	TeamBySlug(ctx context.Context, slug string) (Team, error)

	Projects(ctx context.Context, teamID uuid.UUID) ([]ProjectCounters, error)
	LastSeen(ctx context.Context, teamID uuid.UUID) ([]ProjectPulse, error)
	IssueDebts(ctx context.Context, teamID uuid.UUID, limit int32) ([]IssueDebt, int64, error)
	TaskDebts(ctx context.Context, teamID uuid.UUID, staleBefore time.Time, limit int32) ([]TaskDebt, int64, error)

	IssueByRef(ctx context.Context, teamID uuid.UUID, projectKey string, number int64) (Issue, error)
	IssueMessages(ctx context.Context, teamID, issueID uuid.UUID, limit int32) ([]Message, int64, error)
	TaskByRef(ctx context.Context, teamID uuid.UUID, projectKey string, number int64) (Task, error)
	TaskNotes(ctx context.Context, teamID, taskID uuid.UUID, limit int32) ([]Note, int64, error)
}

// store backs the contract with the generated queries. No *sql.DB: no transaction, ever.
type store struct {
	q *database.Queries
}

// New creates the overview store.
func New(q *database.Queries) Store {
	return &store{q: q}
}
