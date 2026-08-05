package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément   | Résumé                                                              | Ligne |
// |-----------|---------------------------------------------------------------------|-------|
// | Scope     | Full scope of one inbox read, read cursor included                    | 41    |
// | Cursor    | Read position of the token and head of the team journal               | 52    |
// | IssueLine | One actionable issue, summarised for the inbox                        | 62    |
// | TaskLine  | One task in progress, summarised for the inbox                        | 74    |
// | UnblockedLine | One task whose internal dependencies are all lifted               | 87    |
// | Store     | Read contract over the actionable state of a project                  | 100   |
// | store     | Implementation backed by the sqlc-generated queries                   | 121   |
// | New       | Creates the inbox store                                               | 126   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRACT ONLY — the implementation lives in inbox.go.
//
// No Transactor here, deliberately: the inbox does not return a stream of events but the current
// state, recomputed on every call. Its correctness rests on no atomicity — the cursor only drives
// a display flag, never the presence of a line. See docs/DESIGN-M3.md.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// ErrNotFound signals a project that cannot be found in the requested scope.
var ErrNotFound = errors.New("inbox store: not found")

// Scope carries everything that identifies one inbox read.
//
// TokenID comes from the Principal just like the team and the project: the cursor is per TOKEN,
// not per project, so that two agent sessions on the same repo each keep their own progress.
type Scope struct {
	TokenID   uuid.UUID
	TeamID    uuid.UUID
	ProjectID uuid.UUID
	Limit     int32
}

// Cursor carries the read position of the token and the head of the team journal.
//
// The head is captured BEFORE the buckets are computed: an event written during the call stays
// "new" on the next round, rather than being silently skipped over.
type Cursor struct {
	LastEventID int64
	HeadEventID int64
}

// IssueLine is one actionable issue, summarised.
//
// Excerpt is the last message, truncated: the inbox has to fit in the context of an agent that is
// starting up. New says an event concerning it is later than the cursor — that is a reading
// comfort, not a presence criterion: the line is there because the STATE says so.
type IssueLine struct {
	Number    int64
	Title     string
	PeerKey   string
	Excerpt   string
	Truncated bool
	New       bool
	UpdatedAt time.Time
}

// TaskLine is one task in progress, summarised. No "new" flag: this is the agent's own work,
// nothing can teach it that it exists.
type TaskLine struct {
	Number    int64
	Title     string
	Priority  string
	UpdatedAt time.Time
}

// UnblockedLine is one task whose internal dependencies are all lifted.
//
// Status is carried here and not in TaskLine because it is the useful piece of the bucket: `todo`
// says "pick it up again", `blocked` says "your obstacle is lifted, but you had blocked it
// yourself for something else and nobody decided in your place". Without it the two cases would
// be indistinguishable and the agent would have to re-read the task to know what to do.
type UnblockedLine struct {
	Number   int64
	Title    string
	Priority string
	Status   string
	New      bool
}

// Store reads the actionable state of a project.
//
// The event journal is never queried by a predicate of its own: it is reached through an EXISTS
// over a subject that is ALREADY scoped. There is therefore no read able to reveal the activity
// of a third-party project.
type Store interface {
	// ProjectKey resolves the key of the token's project, needed to compose references.
	ProjectKey(ctx context.Context, teamID, projectID uuid.UUID) (string, error)

	Cursor(ctx context.Context, sc Scope) (Cursor, error)

	// IncomingOpen: the questions that are expected from me.
	IncomingOpen(ctx context.Context, sc Scope, lastEventID int64) ([]IssueLine, error)
	// OutgoingAnswered: my questions that got an answer.
	OutgoingAnswered(ctx context.Context, sc Scope, lastEventID int64) ([]IssueLine, error)
	// InProgressTasks: my interrupted work.
	InProgressTasks(ctx context.Context, sc Scope) ([]TaskLine, error)
	// UnblockedTasks: my tasks another task of the repo was blocking, and no longer does.
	UnblockedTasks(ctx context.Context, sc Scope, lastEventID int64) ([]UnblockedLine, error)

	// Advance moves the token cursor forward, never letting it go backwards.
	Advance(ctx context.Context, tokenID uuid.UUID, headEventID int64) error
}

// store backs the contract with the generated queries. No *sql.DB: the inbox never opens a
// transaction.
type store struct {
	q *database.Queries
}

// New creates the inbox store.
func New(q *database.Queries) Store {
	return &store{q: q}
}
