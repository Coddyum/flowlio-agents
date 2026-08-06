package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément       | Résumé                                                          | Ligne |
// |---------------|-----------------------------------------------------------------|-------|
// | Service       | The contract consumed by the memory handler                       | 78    |
// | service       | Implementation, depending on the store interface                  | 103   |
// | New           | Creates the memory service                                        | 108   |
// | RememberInput | What it takes to write one entry                                  | 113   |
// | RecallInput   | One reading of a project's memory                                 | 136   |
// | Entry         | An entry as exposed by the API                                    | 158   |
// | Recalled      | A reading and what it left out                                    | 179   |
// | IndexLine     | One line of the index injected into the MCP handshake             | 185   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRACT ONLY — the implementations live in remember.go, recall.go and validate.go.
//
// WHAT THIS FEATURE IS FOR. An agent picking a repository back up had to re-read the whole backlog
// and the code to find out why things are the way they are (FLWL-71). Tasks say WHAT is being
// done. Nothing said WHY it was decided, or what already bit.
//
// WHAT IT IS NOT. It is not a session journal — the `events` table already records that, without
// the agent's cooperation, and asking an agent to write down what the server already knows is how
// a register ends up empty. It is not a team-wide memory either: that was dropped on 2026-08-05
// and carries the entire injection risk of the product, since a shared memory an agent reads as
// instructions is a channel between repositories.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/memory/store"
	"github.com/google/uuid"
)

// Domain errors, translated into HTTP codes by the handler through errors.Is.
var (
	ErrInvalidInput = errors.New("memory: invalid input")
	ErrNotFound     = errors.New("memory: not found")
	ErrConflict     = errors.New("memory: slug already taken")
	// ErrQuotaExceeded reports a project whose memory has reached its storage bound. Its own error,
	// distinct from ErrConflict: nothing about the request is wrong, and no identical retry will
	// ever work.
	ErrQuotaExceeded = errors.New("memory: quota exceeded")
)

// Kinds of entry, re-exported so no caller has to import the store to name one.
const (
	KindDecision = store.KindDecision
	KindLearning = store.KindLearning
	KindState    = store.KindState
)

// Kinds is the accepted vocabulary, in the order it is presented to an agent.
var Kinds = []string{KindDecision, KindLearning, KindState}

const (
	// defaultLimit and maxLimit bound a reading. Both exist for the same reason every list of this
	// repository is bounded: an unbounded read of free text fills the context of the agent that
	// asked for it, on a call it thought was cheap.
	defaultLimit = 30
	maxLimit     = 200

	// indexLimit bounds the index injected into the MCP handshake. Much tighter than a reading: it
	// is paid on EVERY session, before the agent's first message, and it carries titles only. A
	// repository past this many live entries has a curation problem the index cannot fix by
	// growing.
	indexLimit = 60
)

// Service carries one repository's memory. Every method takes teamID and projectID: they come
// from the token's Principal, never from the request body, so naming another project's memory is
// not expressible.
type Service interface {
	// Remember writes one entry, and supersedes what it replaces IN THE SAME TRANSACTION.
	//
	// The two are one write because they are one intent: "this is the decision now, and it retires
	// that one". Kept apart, a failure between them leaves the new entry in force alongside the old
	// one it was meant to retire — two contradictory decisions, both live, which is the precise
	// state this feature exists to prevent.
	Remember(ctx context.Context, in RememberInput) (Entry, error)

	// Recall lists or searches. One method: the presence of a query decides, and a caller that had
	// to choose would be a caller that could choose wrong.
	Recall(ctx context.Context, in RecallInput) (Recalled, error)

	// Get reads one entry by its slug.
	Get(ctx context.Context, teamID, projectID uuid.UUID, slug string) (Entry, error)

	// Index returns the titles injected into the MCP handshake. Entries in force only.
	//
	// This is HOW READING GETS FORCED, and it is the whole answer to "how do you make an agent use
	// this". Not by asking in a tool description — an agent cannot start a session without having
	// read its instructions, and that is already the channel carrying the project directory.
	Index(ctx context.Context, teamID, projectID uuid.UUID) ([]IndexLine, error)
}

// service depends on the store interface, never on sqlc.
type service struct {
	store store.Store
}

// New creates the memory service.
func New(st store.Store) Service {
	return &service{store: st}
}

// RememberInput is one entry to write.
type RememberInput struct {
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`

	// Slug is the stable identifier the entry is cited by, from a commit, a card or another entry.
	// Chosen by the author rather than drawn from the project counter: the registry this feature
	// has to absorb — our own docs/decisions.md — names its entries D24, D25, D26, and those are
	// cited across three repositories. Renumbering them would break every citation.
	Slug string `json:"slug"`

	Kind  string `json:"kind"`
	Title string `json:"title"`
	Body  string `json:"body"`

	// Supersedes lists the slugs this entry retires, in the same transaction as its own insert.
	//
	// NON-NEGOTIABLE, and it is the reason this table exists rather than a markdown file. Six cards
	// were found stale on 2026-08-05 because nothing said which decision had overtaken which. A
	// registry that only ever appends is a registry whose reader has to guess what still holds.
	Supersedes []string `json:"supersedes"`
}

// RecallInput is one reading of a project's memory.
type RecallInput struct {
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`

	// Query is a full-text expression. Empty lists instead of searching.
	//
	// Postgres FTS, and nothing else: no embedding, no model call. The product contains no AI, and
	// a memory that needed one to be read would be the first place that rule broke.
	Query string `json:"query"`

	// Kind restricts to one kind. Empty means every kind.
	Kind string `json:"kind"`

	// IncludeSuperseded brings back what was retired. False by default: a session picking a
	// repository up wants what is TRUE. The history is one flag away, because answering "why was it
	// like that" is the other half of what supersession buys.
	IncludeSuperseded bool `json:"include_superseded"`

	Limit int32 `json:"limit"`
}

// Entry is the API view of one memory entry.
type Entry struct {
	Slug  string `json:"slug"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Body  string `json:"body"`

	// SupersededBy is empty while the entry is in force. A slug, never an identifier.
	SupersededBy string `json:"superseded_by,omitempty"`
	// Supersedes is never nil: an entry that replaced nothing serialises as `[]`, because `null`
	// reads as "unknown" where the truth is "none".
	Supersedes []string `json:"supersedes"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Recalled is one reading and what it left out.
//
// Total is the count BEFORE the bound, from the same query. An agent therefore knows it is seeing
// a slice of something bigger without a second call — the same contract the note thread and the
// issue list already carry.
type Recalled struct {
	Entries []Entry `json:"entries"`
	Total   int     `json:"total"`
}

// IndexLine is one line of the handshake index: a title, never a body.
type IndexLine struct {
	Slug  string `json:"slug"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
}
