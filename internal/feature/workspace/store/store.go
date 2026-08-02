package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément  | Résumé                                                             | Ligne |
// |----------|--------------------------------------------------------------------|-------|
// | Team     | Une team, unité de tenancy                                           | 36    |
// | Project  | Un projet, c'est-à-dire un repo, identifié par sa clé courte         | 44    |
// | Token    | Un token de projet tel que persisté, sans jamais le secret en clair  | 54    |
// | Store    | Contrat de persistance de la feature workspace                       | 67    |
// | store    | Implémentation adossée aux queries générées par sqlc                 | 84    |
// | New      | Crée le store workspace                                              | 89    |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRAT UNIQUEMENT — les implémentations sont dans team.go, project.go et token.go.

import (
	"errors"
	"time"

	"context"

	"github.com/Coddyum/flowlio-ia/internal/database"
	"github.com/google/uuid"
)

// ErrNotFound signale une ligne absente ; ErrConflict une contrainte d'unicité violée.
var (
	ErrNotFound = errors.New("workspace store: not found")
	ErrConflict = errors.New("workspace store: conflict")
)

// Team est l'unité de tenancy : tout le reste lui appartient.
type Team struct {
	ID        uuid.UUID
	Slug      string
	Name      string
	CreatedAt time.Time
}

// Project représente un repo. Key est le préfixe des identifiants lisibles (FRNT-34).
type Project struct {
	ID        uuid.UUID
	TeamID    uuid.UUID
	Key       string
	Name      string
	CreatedAt time.Time
}

// Token est un token de projet tel que stocké : le secret n'existe qu'à l'instant de la
// création, sous forme de hash.
type Token struct {
	ID         uuid.UUID
	TeamID     uuid.UUID
	ProjectID  uuid.UUID
	Name       string
	Prefix     string
	CreatedAt  time.Time
	LastUsedAt time.Time
	Revoked    bool
}

// Store est le contrat consommé par le service. Chaque lecture porte son scope de tenancy :
// une query sans team_id serait une faille d'isolation, pas un oubli d'ergonomie.
type Store interface {
	CreateTeam(ctx context.Context, slug, name string) (Team, error)
	TeamByID(ctx context.Context, id uuid.UUID) (Team, error)
	TeamBySlug(ctx context.Context, slug string) (Team, error)
	ListTeams(ctx context.Context) ([]Team, error)

	CreateProject(ctx context.Context, teamID uuid.UUID, key, name string) (Project, error)
	ProjectByID(ctx context.Context, teamID, id uuid.UUID) (Project, error)
	ProjectByKey(ctx context.Context, teamID uuid.UUID, key string) (Project, error)
	ListProjects(ctx context.Context, teamID uuid.UUID) ([]Project, error)

	CreateToken(ctx context.Context, teamID, projectID uuid.UUID, name, prefix, hash string) (Token, error)
	ListTokens(ctx context.Context, teamID, projectID uuid.UUID) ([]Token, error)
	RevokeToken(ctx context.Context, teamID, tokenID uuid.UUID) (Token, error)
}

// store adosse le contrat aux queries générées.
type store struct {
	q *database.Queries
}

// New crée le store workspace.
func New(q *database.Queries) Store {
	return &store{q: q}
}
