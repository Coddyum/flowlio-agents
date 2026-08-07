package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | Service            | The contract consumed by the workspace handler                | 43    |
// | service            | Implementation, depending on the store interface             | 77    |
// | New                | Creates the workspace service                                | 82    |
// | CreateTeamInput    | Input for creating a team                                    | 88    |
// | Team               | A team as exposed by the API                                 | 94    |
// | CreateProjectInput | Input for creating a project                                 | 103   |
// | Project            | A project as exposed by the API                              | 110   |
// | CreateTokenInput   | Input for issuing an agent token                             | 118   |
// | CreatedToken       | A freshly created token: the one chance to see the secret    | 126   |
// | TokenInfo          | A listed token, with neither secret nor hash                 | 135   |
// | TrustPairInput     | One directed edge named by the keys of its two ends           | 152   |
// | TrustDecision      | What a write on the graph actually changed                   | 163   |
// | TrustEdge          | A directed edge of the graph as exposed by the API           | 171   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRACT ONLY — the implementations live in teams.go, projects.go, tokens.go and trust.go.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/google/uuid"
)

// Domain errors, translated into HTTP codes by the handler through errors.Is.
var (
	ErrInvalidInput = errors.New("workspace: invalid input")
	ErrNotFound     = errors.New("workspace: not found")
	ErrConflict     = errors.New("workspace: already exists")
)

// Service carries the administration of tenancy: teams, projects, agent tokens.
type Service interface {
	CreateTeam(ctx context.Context, in CreateTeamInput) (Team, error)
	// ListTeams enumerates the teams visible to a principal. `pinned` is that principal's own
	// team, or uuid.Nil for an admin bound to none — see the note on the implementation.
	ListTeams(ctx context.Context, pinned uuid.UUID) ([]Team, error)
	TeamBySlug(ctx context.Context, slug string) (Team, error)

	CreateProject(ctx context.Context, in CreateProjectInput) (Project, error)
	ListProjects(ctx context.Context, teamID uuid.UUID) ([]Project, error)

	// Whoami turns a principal's identifiers into readable names, so that neither the CLI nor an
	// agent ever has to handle a UUID.
	Whoami(ctx context.Context, teamID, projectID uuid.UUID) (Identity, error)

	// CreateToken issues a project token. The secret in clear is returned here, once, and nowhere
	// else.
	CreateToken(ctx context.Context, in CreateTokenInput) (CreatedToken, error)
	ListTokens(ctx context.Context, teamID uuid.UUID, projectKey string) ([]TokenInfo, error)
	RevokeToken(ctx context.Context, teamID, tokenID uuid.UUID) error

	// Trust graph — human administration, under an admin token.
	//
	// These three methods carry NO authorisation decision: they edit a declaration, and it is the
	// CreateIssue query that enforces it. Their only validation is that of two strings typed by a
	// human — tenancy itself lives in the query.
	//
	// AllowTrust and RevokeTrust act on ONE DIRECTION. `From` may open a question at `To`; the
	// reciprocal is a second call, and ListTrust returns one edge per direction.
	AllowTrust(ctx context.Context, in TrustPairInput) (TrustDecision, error)
	RevokeTrust(ctx context.Context, in TrustPairInput) (TrustDecision, error)
	ListTrust(ctx context.Context, teamID uuid.UUID) ([]TrustEdge, error)
}

// service depends on the store interface, never on sqlc.
type service struct {
	store store.Store
}

// New creates the workspace service.
func New(st store.Store) Service {
	return &service{store: st}
}

// CreateTeamInput carries the data for creating a team. Slug is the readable identifier used in
// the CLI.
type CreateTeamInput struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Team is the API view of a team.
type Team struct {
	ID        uuid.UUID `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateProjectInput carries the data for creating a project. Key prefixes the readable
// identifiers (FRNT-34).
type CreateProjectInput struct {
	TeamID uuid.UUID `json:"-"`
	Key    string    `json:"key"`
	Name   string    `json:"name"`
}

// Project is the API view of a project.
type Project struct {
	ID        uuid.UUID `json:"id"`
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateTokenInput carries the data for issuing an agent token, scoped to a project.
type CreateTokenInput struct {
	TeamID     uuid.UUID `json:"-"`
	ProjectKey string    `json:"project"`
	Name       string    `json:"name"`
}

// CreatedToken is returned exactly once, at creation. Secret is neither stored, nor displayable
// again, nor logged.
type CreatedToken struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	Prefix     string    `json:"prefix"`
	ProjectKey string    `json:"project"`
	Secret     string    `json:"secret"`
}

// TokenInfo is the listing view: neither secret nor hash.
type TokenInfo struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Revoked    bool       `json:"revoked"`
}

// TrustPairInput names ONE DIRECTED edge by the KEYS of its two ends.
//
// No UUID: both keys are resolved INSIDE the query, under the team_id already proven by teamFor. A
// handler resolving the keys itself would hand-rebuild the very enumeration the model refuses to
// expose.
//
// From is the repo allowed to OPEN a question, To the repo it may open it at. The order is the
// whole content of the type: swapping the two fields names a different edge.
type TrustPairInput struct {
	TeamID uuid.UUID `json:"-"`
	From   string    `json:"from"`
	To     string    `json:"to"`
}

// TrustDecision says what the write actually changed, so the CLI can tell "done" from "it already
// was" without a second round trip.
//
// Changed is false on a replay: `trust allow` on an already-open edge, `trust deny` on an
// already-closed one. Both verbs are idempotent, and this field is what makes it visible.
type TrustDecision struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Changed bool   `json:"changed"`
}

// TrustEdge is a DIRECTED edge as exposed by the API: a sender, a recipient and a date. A mutually
// trusted pair is two of these.
type TrustEdge struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	CreatedAt time.Time `json:"created_at"`
}
