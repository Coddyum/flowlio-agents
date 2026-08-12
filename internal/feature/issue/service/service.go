package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                      | Ligne |
// |------------------|-------------------------------------------------------------|-------|
// | Service          | The contract consumed by the issue handler                    | 48    |
// | service          | Implementation, depending on the store interface              | 61    |
// | New              | Creates the issue service                                     | 71    |
// | Issue            | An issue as exposed by the API                                | 80    |
// | Message          | A message of the thread, attributed to the project that wrote it | 94 |
// | IssueDetail      | An issue together with its message thread                     | 104   |
// | Ref              | Names CORE-34 for the caller, scope included                  | 114   |
// | CreateIssueInput | Input for opening an issue towards a sibling project          | 123   |
// | ListIssuesInput  | Criteria for reading the visible issues                       | 141   |
// | AnswerInput      | A message to append to the thread, and an optional closing    | 155   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRACT ONLY — the implementations live in create_issue.go, list_issues.go, get_issue.go and
// answer_issue.go.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/issue/store"
	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
	"github.com/google/uuid"
)

// Domain errors, translated into HTTP codes by the handler through errors.Is.
//
// There is deliberately NO "forbidden" error on an issue key: an issue out of reach is not found.
// Telling the two apart would allow enumerating a sibling repo's backlog by trying numbers.
var (
	ErrInvalidInput = errors.New("issue: invalid input")
	ErrNotFound     = errors.New("issue: not found")
	ErrConflict     = errors.New("issue: conflict")
)

// Service carries the cross-project questions: what a repo asks of a sibling repo.
//
// TeamID and ProjectID come from the token's Principal, never from the request body. That is what
// makes acting on behalf of another project impossible.
type Service interface {
	// CreateIssue opens a question towards a sibling project, named by its key.
	CreateIssue(ctx context.Context, in CreateIssueInput) (Issue, error)

	ListIssues(ctx context.Context, in ListIssuesInput) ([]Issue, error)
	GetIssue(ctx context.Context, ref Ref) (IssueDetail, error)

	// Answer appends a message to the thread and, when asked, closes the issue. The resulting state
	// is not chosen by the caller: it is deduced from its role in the conversation.
	Answer(ctx context.Context, in AnswerInput) (Issue, error)
}

// service depends on the store interface, never on sqlc.
type service struct {
	store store.Store
	// cache carries the wake registrations: on a committed answer the service pushes a wake to the
	// OTHER party's local waker (D55, docs/DESIGN-WAKE.md §5). It is the service and not the store
	// that signals, because which repo to wake depends on the DIRECTION of the exchange — author or
	// recipient — which the store's Event does not carry.
	cache cache.Cache
}

// New creates the issue service.
func New(st store.Store, c cache.Cache) Service {
	return &service{store: st, cache: c}
}

// Issue is the API view. Ref is the full readable key (CORE-34), composed here and nowhere else. It
// always carries the RECIPIENT's key, which owns the issue and its number.
//
// Role and Peer are relative to the caller: "who am I in this conversation, and who is across from
// me". An agent should not have to recompose that information.
type Issue struct {
	Ref       string     `json:"ref"`
	Title     string     `json:"title"`
	State     string     `json:"state"`
	Role      string     `json:"role"`
	Peer      string     `json:"peer"`
	UpdatedAt time.Time  `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
	// Effort is the rigour tier the author declared, echoed back so a creator sees what registered.
	// Omitted when unspecified — the receiver's waker treats absence as the standard tier.
	Effort string `json:"effort,omitempty"`
}

// Message is an entry of the thread. The author is a PROJECT: this is a dialogue between two repos.
type Message struct {
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// IssueDetail is an issue and its thread, oldest to newest.
//
// MessagesTotal exists so the agent knows it is only reading a window: a hundred-message thread
// must not enter its context in one block.
type IssueDetail struct {
	Issue
	Messages      []Message `json:"messages"`
	MessagesTotal int       `json:"messages_total"`
}

// Ref names CORE-34 for the caller.
//
// ProjectKey is the one read from the reference, hence controlled by the caller; TeamID and
// CallerProjectID come from the token. Visibility is decided on those two.
type Ref struct {
	TeamID          uuid.UUID `json:"-"`
	CallerProjectID uuid.UUID `json:"-"`
	ProjectKey      string    `json:"-"`
	Number          int64     `json:"-"`
}

// CreateIssueInput carries the opening of an issue. The recipient is a project KEY: an agent does
// not handle UUIDs, so it cannot inject one.
type CreateIssueInput struct {
	TeamID          uuid.UUID `json:"-"`
	AuthorProjectID uuid.UUID `json:"-"`

	ToProject string `json:"to_project"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	// Effort is the rigour tier the author declares for the answer — low, standard, high or max, or
	// "" to leave it unspecified. It never names a model: the receiver maps the tier to its own agent
	// and clamps it to its own ceiling (internal/pkg/effort, docs/DESIGN-WAKE.md §14).
	Effort string `json:"effort,omitempty"`
}

// ListIssuesInput describes one read.
//
// Role is "", "incoming" or "outgoing"; it narrows what is already visible, it never opens access.
// Closed issues are excluded by default: what is closed calls for no more action, and an agent's
// context is a scarce resource.
type ListIssuesInput struct {
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`

	Role          string `json:"role"`
	State         string `json:"state"`
	IncludeClosed bool   `json:"include_closed"`
	Limit         int    `json:"limit"`
}

// AnswerInput carries a message to append to the thread, and an optional closing.
//
// The body is required even to close: a closing with no reason tells the correspondent nothing, and
// they would find their question shut without knowing why.
type AnswerInput struct {
	Ref Ref `json:"-"`

	Body  string `json:"body"`
	Close bool   `json:"close"`
}
