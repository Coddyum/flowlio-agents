package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément    | Résumé                                                             | Ligne |
// |------------|--------------------------------------------------------------------|-------|
// | Service    | Contract consumed by the inbox handler                               | 53    |
// | service    | Implementation, depending on the store interface                     | 62    |
// | New        | Creates the inbox service                                            | 67    |
// | CheckInput | Scope of the call, entirely taken from the token                     | 73    |
// | IssueLine  | One actionable issue as it is exposed                                | 81    |
// | TaskLine   | One task in progress as it is exposed                                | 92    |
// | UnblockedLine | One task no internal dependency blocks any more                   | 103   |
// | More       | What did not fit in the buckets                                      | 113   |
// | Inbox      | The actionable state of the project, in four buckets                 | 129   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRACT ONLY — the implementation lives in check.go.
//
// WHY A STATE AND NOT A STREAM — structuring decision of docs/DESIGN-M3.md.
//
// check_inbox does NOT return the events that happened since the last call. It returns what there
// is to do NOW, recomputed on every call from issues.state and tasks.status.
//
// Consequence: no notification can be lost. A stream would have to guarantee exactly-once
// delivery — a sequence identifier is assigned on insert and not on commit, so a slow transaction
// can commit a number a reader has already passed, and the matching issue would become invisible
// forever. Here that flaw costs at worst a missing "new" flag.
//
// Two successive calls therefore return the same thing if nothing happened. That is a property,
// not a flaw: an agent whose context was just compacted must find its work in progress again, not
// conclude "nothing to do" because another call had already read it.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/inbox/store"
	"github.com/google/uuid"
)

// Domain errors, translated into HTTP codes by the handler.
var (
	ErrInvalidInput = errors.New("inbox: invalid input")
	ErrNotFound     = errors.New("inbox: not found")
)

// Service answers the only question an agent asks when a session starts: what is waiting for
// me?
type Service interface {
	// Check returns the actionable state of the project and moves the token cursor forward.
	//
	// Moving the cursor forward gates no line: it only removes the "new" flag on the next
	// call.
	Check(ctx context.Context, in CheckInput) (Inbox, error)
}

// service depends on the store interface, never on sqlc.
type service struct {
	store store.Store
}

// New creates the inbox service.
func New(st store.Store) Service {
	return &service{store: st}
}

// CheckInput carries the scope of the call. Every field comes from the token: this call literally
// has no input parameter on the agent side.
type CheckInput struct {
	TokenID   uuid.UUID `json:"-"`
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`
}

// IssueLine is one actionable issue. Ref always carries the key of the RECIPIENT project: that is
// the one to reuse when answering.
type IssueLine struct {
	Ref       string    `json:"ref"`
	Title     string    `json:"title"`
	Peer      string    `json:"peer"`
	Excerpt   string    `json:"excerpt"`
	Truncated bool      `json:"truncated,omitempty"`
	New       bool      `json:"new"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TaskLine is one task in progress. No "new" flag: this is the agent's own work.
type TaskLine struct {
	Ref      string `json:"ref"`
	Title    string `json:"title"`
	Priority string `json:"priority"`
}

// UnblockedLine is one task whose progress no internal dependency blocks any more.
//
// Status tells the two outcomes of the unblocking apart, and it is the useful piece of the
// bucket: `todo` says "pick it up again", `blocked` says "your obstacle is lifted, but you had
// blocked it yourself for something else and nobody decided in your place".
type UnblockedLine struct {
	Ref      string `json:"ref"`
	Title    string `json:"title"`
	Priority string `json:"priority"`
	Status   string `json:"status"`
	New      bool   `json:"new"`
}

// More counts what did not fit in the buckets, so an agent knows it is not seeing everything and
// goes for the rest with list_issues or list_tasks.
type More struct {
	NeedsAnswer int `json:"needs_answer,omitempty"`
	Answered    int `json:"answered,omitempty"`
	InProgress  int `json:"in_progress,omitempty"`
	Unblocked   int `json:"unblocked,omitempty"`
}

// Inbox is the actionable state of the project, in four buckets:
//   - NeedsAnswer: somebody is blocked on me;
//   - Answered   : I was blocked on ANOTHER repo, I no longer am;
//   - InProgress : my own interrupted work;
//   - Unblocked  : I was blocked by another task of THIS repo, I no longer am.
//
// The two forms of unblocking are distinct buckets and must stay so: one is settled by answering
// a peer (answer_issue), the other by picking your own work up again. Merging them would force
// the agent to re-read every line to know which of the two it is looking at.
type Inbox struct {
	Project     string          `json:"project"`
	NeedsAnswer []IssueLine     `json:"needs_answer"`
	Answered    []IssueLine     `json:"answered"`
	InProgress  []TaskLine      `json:"in_progress"`
	Unblocked   []UnblockedLine `json:"unblocked"`
	More        *More           `json:"more,omitempty"`
}
