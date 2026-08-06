package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                        | Ligne |
// |-----------------|---------------------------------------------------------------|-------|
// | Service         | The contract consumed by the task handler                       | 54    |
// | service         | Implementation, depending on the store interface                | 82    |
// | New             | Creates the task service                                        | 87    |
// | Task            | A task as exposed by the API                                    | 93    |
// | Note            | A progress note as exposed by the API                           | 106   |
// | TaskDetail      | A task together with its note thread                            | 116   |
// | CreateTaskInput | Input for creating a task                                       | 124   |
// | ListTasksInput  | Criteria for reading the backlog                                | 139   |
// | UpdateTaskInput | Partial patch of a task, progress note included                 | 152   |
// | BlockTaskInput  | Opening a blocking edge between two tasks of the project        | 190   |
// | UnblockTaskInput| Releasing one named edge by hand                                | 204   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRACT ONLY — the implementations live in create_task.go, list_tasks.go, get_task.go and
// update_task.go.
//
// There is NO archive method: archiving is a field of UpdateTask, written in the same transaction
// as the rest. One single write on a task, hence one single surface to secure and no non-atomic
// seam between two calls.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
	"github.com/google/uuid"
)

// Domain errors, translated into HTTP codes by the handler through errors.Is.
var (
	ErrInvalidInput = errors.New("task: invalid input")
	ErrNotFound     = errors.New("task: not found")
	ErrConflict     = errors.New("task: conflict")
	// ErrQuotaExceeded reports a project whose note thread has reached its storage bound
	// (store.ProjectNoteBytesQuota). Its own error, distinct from ErrConflict: the caller did
	// nothing wrong and retrying identically will never work, so the two must not answer alike.
	ErrQuotaExceeded = errors.New("task: note quota exceeded")
)

// Service carries the backlog of one project.
//
// Every method takes teamID and projectID: they come from the token's Principal, never from the
// request body. An agent therefore cannot name another project's backlog, not even by forging its
// request.
type Service interface {
	CreateTask(ctx context.Context, in CreateTaskInput) (Task, error)
	ListTasks(ctx context.Context, in ListTasksInput) ([]Task, error)

	// GetTask returns the task and its note thread: resuming an interrupted task needs both, and
	// two round trips would cost one more agent turn.
	GetTask(ctx context.Context, teamID, projectID uuid.UUID, number int64) (TaskDetail, error)

	// UpdateTask applies a patch and, when Note is provided, writes the progress note in the SAME
	// transaction: "move to done and say why" is a single intention, hence a single write, which
	// succeeds or fails as one.
	//
	// It is also what RELEASES blocking edges: a task reaching its release status, or getting
	// archived, unblocks whatever was waiting on it, in the same transaction. A separate path would
	// have made "the blocker is done, the blocked task never heard about it" possible.
	UpdateTask(ctx context.Context, in UpdateTaskInput) (Task, error)

	// BlockTask opens an edge reading "this task is blocked by another one of the SAME project,
	// until that one reaches Until". Returns the blocked task in its resulting state.
	BlockTask(ctx context.Context, in BlockTaskInput) (Task, error)

	// UnblockTask releases an edge by hand, without waiting for the blocker to move on. Going back
	// to `todo` obeys the same rule as an automatic release: only if that edge was what set the
	// block and no other one remains.
	UnblockTask(ctx context.Context, in UnblockTaskInput) (Task, error)
}

// service depends on the store interface, never on sqlc.
type service struct {
	store store.Store
}

// New creates the task service.
func New(st store.Store) Service {
	return &service{store: st}
}

// Task is the API view of a task. Number is the human-readable identifier inside the project:
// paired with the project key, it reads CORE-34.
type Task struct {
	Number    int64      `json:"number"`
	Title     string     `json:"title"`
	Body      string     `json:"body,omitempty"`
	Status    string     `json:"status"`
	Priority  string     `json:"priority"`
	Deadline  *time.Time `json:"deadline,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Archived  bool       `json:"archived"`
}

// Note is the API view of a progress note.
type Note struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// TaskDetail is a task together with the TAIL of its note thread, oldest to newest.
//
// NotesTotal exists so the agent knows it is only reading a window — same reason as MessagesTotal
// on the issue side. Without that counter, a truncated thread is indistinguishable from a short
// one, and the agent concludes there is nothing else to know about the task it is resuming.
type TaskDetail struct {
	Task
	Notes      []Note `json:"notes"`
	NotesTotal int    `json:"notes_total"`
}

// CreateTaskInput carries the creation data. TeamID and ProjectID come from the token and are
// never read from the request body.
type CreateTaskInput struct {
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`

	Title    string     `json:"title"`
	Body     string     `json:"body"`
	Status   string     `json:"status"`
	Priority string     `json:"priority"`
	Deadline *time.Time `json:"deadline"`
}

// ListTasksInput describes one read of the backlog.
//
// An empty Status means "every status". Archived tasks are excluded by default: an agent asking
// for its work in progress must not pay, in tokens, for the repo's history.
type ListTasksInput struct {
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`

	Status          string `json:"status"`
	IncludeArchived bool   `json:"include_archived"`
	Limit           int    `json:"limit"`
}

// UpdateTaskInput is a patch: a field absent from the JSON stays nil and leaves the value in place.
//
// ClearDeadline is needed because `"deadline": null` is indistinguishable from an absent field
// once decoded — without that flag, wiping a deadline would be impossible to express.
type UpdateTaskInput struct {
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`
	Number    int64     `json:"-"`

	Title    *string `json:"title"`
	Body     *string `json:"body"`
	Status   *string `json:"status"`
	Priority *string `json:"priority"`

	Deadline      *time.Time `json:"deadline"`
	ClearDeadline bool       `json:"clear_deadline"`

	// Note appends a progress note to the thread, in the same transaction as the patch.
	//
	// It is a field rather than a separate operation because an agent's real intention is "move to
	// done AND say why": two writes made a status change without its reason possible, and cost one
	// more round trip every turn.
	// An empty string is rejected: a note with no content teaches the next session nothing.
	Note *string `json:"note"`

	// Archive takes the task out of the active backlog, in the SAME write as the rest of the patch.
	//
	// A field rather than a separate operation, for the reason that folded the note in: archiving
	// was a second HTTP round trip, and atomicity stopped at that boundary. A failure between the
	// two committed the note without archiving, the agent read an error and replayed — which wrote
	// the note a second time. Folded in, the call succeeds or fails as one.
	//
	// Archiving also RELEASES the edges this task was blocking: archived, it will never reach its
	// release status, and leaving them in place would manufacture undead tasks.
	Archive bool `json:"archive"`
}

// BlockTaskInput opens an edge reading "Number is blocked by Blocker".
//
// Blocker is a NUMBER, not a reference: no cross-repo form exists, and that is not a gap. A
// dependency crossing a repository already has its object, the issue (D42). The guard holds in the
// database — both ends of the edge share the same project_id column — not here.
type BlockTaskInput struct {
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`
	Number    int64     `json:"-"`

	Blocker int64 `json:"blocker"`

	// Until is the status the blocker must reach for the edge to be released. Empty means `done`.
	// Only `in_progress` and `done` are accepted: the other two are not progress, and an edge
	// releasing on `todo` would be born already released.
	Until string `json:"until"`
}

// UnblockTaskInput releases, by hand, the edge between Number and Blocker.
type UnblockTaskInput struct {
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`
	Number    int64     `json:"-"`

	Blocker int64 `json:"-"`
}
