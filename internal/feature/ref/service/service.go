package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément      | Résumé                                                           | Ligne |
// |--------------|------------------------------------------------------------------|-------|
// | Service      | Contract consumed by the ref handler                               | 69    |
// | service      | Implementation: one store, and two peers reached through the       | 74    |
// | New          | Creates the ref service                                            | 82    |
// | ResolveInput | A reference to resolve, scope included                             | 90    |
// | Resolved     | What a reference turned out to be, kind announced before content   | 108   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRACT ONLY — the implementation is in resolve_ref.go.
//
// WHY THIS FEATURE EXISTS. The project counter is SHARED between tasks and issues: an agent
// reading CORE-34 in a commit message, an inbox or an issue thread does not know which of the two
// it is. Until this module, the MCP layer resolved that by trying one route then the other —
// two HTTP round trips on the hottest read path of the product, the one check_inbox feeds.
//
// WHY IT HAS NO store/ WORTH THE NAME. It owns no table. It composes `task` and `issue` through
// the FeatureRegistry, and keeps a single query for the one fact neither peer can give it: the
// caller's own project key. See store/store.go.

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/feature/ref/store"
	"github.com/google/uuid"
)

// Domain errors, translated into HTTP codes by the handler through errors.Is.
//
// There is deliberately NO "forbidden": a reference out of the caller's reach is unfindable,
// exactly like a number that was never issued. Telling the two apart would let an agent
// enumerate a sibling's backlog by trying numbers — the same rule the issue feature carries, and
// this module must not be the surface that breaks it.
var (
	ErrInvalidInput = errors.New("ref: invalid input")
	ErrNotFound     = errors.New("ref: not found")
)

// Kinds a reference can turn out to be. They are part of the API: the MCP layer branches on them
// to decide whether the payload needs the untrusted-content framing.
const (
	KindTask  = "task"
	KindIssue = "issue"
)

// Registry keys of the two features this one composes.
//
// Written as literals because a feature may not import a feature — that is the whole point of the
// registry. The coupling is therefore by STRING, and it is checked at startup rather than at
// compile time: see resolveTasks / resolveIssues in resolve_ref.go, which fail loudly.
const (
	taskModuleKey  = "task"
	issueModuleKey = "issue"
)

// Service resolves a reference, whatever it designates.
//
// TeamID and ProjectID come from the token's Principal, never from the request. That is what
// makes resolving a reference on behalf of another project impossible.
type Service interface {
	ResolveRef(ctx context.Context, in ResolveInput) (Resolved, error)
}

// service depends on its store and on the registry — never on another feature's package.
type service struct {
	store store.Store
	// registry is resolved LAZILY, at request time and not at construction. Modules are built
	// before any of them is registered, so a peer captured in New would always be nil.
	registry module.FeatureRegistry
}

// New creates the ref service.
func New(st store.Store, registry module.FeatureRegistry) Service {
	return &service{store: st, registry: registry}
}

// ResolveInput is a reference to resolve.
//
// ProjectKey is the one read FROM the reference, so the caller controls it; TeamID and ProjectID
// come from the token. Visibility is decided on the latter two, inside the query.
type ResolveInput struct {
	TeamID    uuid.UUID
	ProjectID uuid.UUID

	ProjectKey string
	Number     int64
}

// Resolved is what the reference turned out to be.
//
// FIELD ORDER IS PART OF THE ANSWER, and it is why this is a struct and not a map. `kind` and
// `ref` come first so a reader knows WHAT it is reading before it reads it — an issue payload
// carries complete message bodies written by another repository, and the MCP layer puts its
// reading notice in front of them for the same reason (cmd/flowlio/mcp_get.go).
//
// Exactly one of Task and Issue is ever set. They are raw JSON because they are the owning
// feature's own API view: naming those types here would be an import from one feature into
// another. See the note on the resolver interfaces in internal/core/module/module.go.
type Resolved struct {
	Kind  string          `json:"kind"`
	Ref   string          `json:"ref"`
	Task  json.RawMessage `json:"task,omitempty"`
	Issue json.RawMessage `json:"issue,omitempty"`
}
