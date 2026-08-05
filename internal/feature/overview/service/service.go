package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                              | Ligne |
// |-------------|---------------------------------------------------------------------|-------|
// | Service     | Contract consumed by the overview handler                             | 76    |
// | service     | Implementation, depending on the store interface                      | 88    |
// | New         | Creates the overview service                                          | 93    |
// | Team        | Identity of a team resolved by its slug                               | 99    |
// | ProjectLine | One repo, its five counters and its pulse                             | 110   |
// | Debt        | One debt row, already classified                                      | 128   |
// | TeamState   | The overview screen: the repos, the debts, what is hidden             | 142   |
// | Message     | One message of the thread, as it is exposed                           | 150   |
// | Note        | One progress note, as it is exposed                                   | 157   |
// | RefDetail   | The detail of a reference, polymorphic between issue and task         | 173   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRACT ONLY — the implementation lives in team_state.go, ref_detail.go and validate.go.
//
// WHAT THIS SURFACE DOES NOT YIELD, AND WHY. Every cut below was paid for once; reopening them
// takes an argument, not a client's convenience:
//
//   - no UUID, anywhere. The team is designated by a slug, everything else by a `KEY-number`
//     reference. An opaque identifier handed to a client becomes an identifier accepted as input
//     the day after.
//   - no `health`, no `is_stale`, no duration in seconds, no colour. "Three days = red" is a
//     rendering policy: coding it here makes it wrong for the next team.
//   - no "new" flag. The cursor belongs to an agent token; a human "already seen" would be a new
//     table, hence a write on a surface declared read-only.
//   - no excerpt on the debt rows. Fifty excerpts do not read in three seconds, and the detail is
//     one call away.
//   - no echo of the team slug. The caller supplied it; sending it back invites making this
//     response the source of the team's metadata.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/overview/store"
	"github.com/google/uuid"
)

// Domain errors, translated into HTTP codes by the handler.
//
// ErrNotFound covers "unknown slug", "team that is not yours" and "reference not found" alike:
// telling them apart would give an oracle letting the installation's teams be enumerated by
// sweeping slugs.
var (
	ErrInvalidInput = errors.New("overview: invalid input")
	ErrNotFound     = errors.New("overview: not found")
)

// The four debt kinds. The classification is TOTAL: every row yielded by the store falls into
// exactly one kind, or is omitted for a named reason (§ team_state.go).
const (
	// KindAnswer — a sibling agent is blocked on this repo.
	KindAnswer = "answer"
	// KindCollect — it has its answer and has not consumed it.
	KindCollect = "collect"
	// KindResume — session likely dead in the middle of a task.
	KindResume = "resume"
	// KindAsk — it declared itself stuck without asking anybody anything.
	KindAsk = "ask"
)

// Service answers the two questions of a human supervisor: "where are my repos at" and "what is
// being said about CORE-41".
//
// TeamBySlug is exposed because the handler must resolve the scope BEFORE passing it to the other
// two methods, and because it is that same call that rejects an admin pinned to another team. The
// other two methods take nothing but an already-resolved teamID: a slug never comes down here.
type Service interface {
	// TeamBySlug resolves a slug into a team identity, or yields ErrNotFound.
	TeamBySlug(ctx context.Context, slug string) (Team, error)

	// TeamState assembles the repos, their pulse and the team's debt queue.
	TeamState(ctx context.Context, teamID uuid.UUID) (TeamState, error)

	// RefDetail yields the detail of a reference: issue first, task second.
	RefDetail(ctx context.Context, teamID uuid.UUID, projectKey string, number int64) (RefDetail, error)
}

// service depends on the store interface, never on sqlc.
type service struct {
	store store.Store
}

// New creates the overview service.
func New(st store.Store) Service {
	return &service{store: st}
}

// Team is the identity of a team resolved by its slug. It never leaves as-is towards the client:
// the handler keeps nothing but the ID, to scope the other two calls.
type Team struct {
	ID   uuid.UUID
	Slug string
	Name string
}

// ProjectLine is one repo, its five counters and its pulse.
//
// LastAgentSeenAt is a TIMESTAMP, never a duration: a duration goes stale inside the client. It is
// absent — and not zero — for a repo no token of which has served yet: "no pulse" and "pulse on
// the 1st of January of year 1" are not the same thing.
type ProjectLine struct {
	Key             string     `json:"key"`
	OwesAnswer      int64      `json:"owes_answer"`
	AwaitingAnswer  int64      `json:"awaiting_answer"`
	AnsweredUnread  int64      `json:"answered_unread"`
	TasksRunning    int64      `json:"tasks_running"`
	TasksBlocked    int64      `json:"tasks_blocked"`
	LastAgentSeenAt *time.Time `json:"last_agent_seen_at,omitempty"`
}

// Debt is one debt row, already classified: the client has no business rule to replay.
//
// Ref always carries the key of the issue's RECIPIENT or of the task's owner — that is the one
// retyped to open the detail. Debtor is the one that must act, and the two differ on `collect`:
// the issue is `CORE-41`, but it is WEB that must go and read its answer.
//
// Peer is empty on `resume` and `ask`, which have no counterpart. Since aggregates two different
// columns (`issues.updated_at` and `last_move`), hence its name rather than `updated_at`.
type Debt struct {
	Kind   string    `json:"kind"`
	Ref    string    `json:"ref"`
	Debtor string    `json:"debtor"`
	Peer   string    `json:"peer,omitempty"`
	Title  string    `json:"title"`
	Since  time.Time `json:"since"`
}

// TeamState is the overview screen.
//
// GeneratedAt is the clock the client computes ALL the ages from: if it used its own, a drift
// would produce "−3 s ago". Truncated counts the debts the bound hid; without it a truncated list
// lies, and the screen is wrong in a silent, credible way.
type TeamState struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Projects    []ProjectLine `json:"projects"`
	Debts       []Debt        `json:"debts"`
	Truncated   int           `json:"truncated"`
}

// Message is one message of the thread, designated by the key of the project that wrote it.
type Message struct {
	From      string    `json:"from"`
	CreatedAt time.Time `json:"created_at"`
	Body      string    `json:"body"`
}

// Note is one progress note.
type Note struct {
	CreatedAt time.Time `json:"created_at"`
	Body      string    `json:"body"`
}

// RefDetail is the detail of a reference, polymorphic between issue and task.
//
// ONE TYPE, AND NOT TWO. `kind` is the first field, and it is the one saying which fields are
// filled in — an exact mirror of what the MCP surface already does. Two structs would have forced
// an `any` in the service signature, which code-conventions.md rejects.
//
// MessagesTotal and NotesTotal are emitted ONLY when they exceed the number of rows rendered:
// "3 notes, 3 rendered" teaches nothing, "3 rendered out of 47" changes the reading.
//
// closed_at is cut: `state` and `updated_at` already say it, and a third source of the same
// information is a divergence waiting to happen.
type RefDetail struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`

	// Issue only.
	From  string `json:"from,omitempty"`
	State string `json:"state,omitempty"`

	// Task only.
	Status   string `json:"status,omitempty"`
	Priority string `json:"priority,omitempty"`

	Title     string     `json:"title"`
	Body      string     `json:"body,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Deadline  *time.Time `json:"deadline,omitempty"`

	Messages      []Message `json:"messages,omitempty"`
	MessagesTotal int       `json:"messages_total,omitempty"`
	Notes         []Note    `json:"notes,omitempty"`
	NotesTotal    int       `json:"notes_total,omitempty"`
}
