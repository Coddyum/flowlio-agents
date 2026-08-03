package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | Service            | Contrat consommé par le handler workspace                    | 43    |
// | service            | Implémentation, dépendante de l'interface store              | 71    |
// | New                | Crée le service workspace                                    | 76    |
// | CreateTeamInput    | Entrée de création d'une team                                | 82    |
// | Team               | Une team telle qu'exposée par l'API                          | 88    |
// | CreateProjectInput | Entrée de création d'un projet                               | 97    |
// | Project            | Un projet tel qu'exposé par l'API                            | 104   |
// | CreateTokenInput   | Entrée de création d'un token d'agent                        | 112   |
// | CreatedToken       | Token fraîchement créé : seule occasion de voir le secret    | 120   |
// | TokenInfo          | Token listé, sans secret ni hash                             | 129   |
// | TrustPairInput     | Une paire de projets désignée par ses deux clés              | 145   |
// | TrustDecision      | Ce qu'une écriture sur le graphe a réellement changé         | 156   |
// | TrustEdge          | Une arête du graphe telle qu'exposée par l'API               | 163   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRAT UNIQUEMENT — les implémentations sont dans teams.go, projects.go, tokens.go et trust.go.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/google/uuid"
)

// Erreurs domaine, traduites en codes HTTP par le handler via errors.Is.
var (
	ErrInvalidInput = errors.New("workspace: invalid input")
	ErrNotFound     = errors.New("workspace: not found")
	ErrConflict     = errors.New("workspace: already exists")
)

// Service porte l'administration de la tenancy : teams, projets, tokens d'agent.
type Service interface {
	CreateTeam(ctx context.Context, in CreateTeamInput) (Team, error)
	ListTeams(ctx context.Context) ([]Team, error)
	TeamBySlug(ctx context.Context, slug string) (Team, error)

	CreateProject(ctx context.Context, in CreateProjectInput) (Project, error)
	ListProjects(ctx context.Context, teamID uuid.UUID) ([]Project, error)

	// Whoami traduit les identifiants d'un principal en noms lisibles, pour que ni la CLI ni
	// un agent n'aient à manipuler d'UUID.
	Whoami(ctx context.Context, teamID, projectID uuid.UUID) (Identity, error)

	// CreateToken émet un token de projet. Le secret en clair n'est renvoyé qu'ici, une fois.
	CreateToken(ctx context.Context, in CreateTokenInput) (CreatedToken, error)
	ListTokens(ctx context.Context, teamID uuid.UUID, projectKey string) ([]TokenInfo, error)
	RevokeToken(ctx context.Context, teamID, tokenID uuid.UUID) error

	// Graphe de confiance — administration humaine, sous token admin.
	//
	// Ces trois méthodes ne portent AUCUNE décision d'autorisation : elles éditent une
	// déclaration, et c'est la query CreateIssue qui l'applique. Leur seule validation est
	// celle de deux chaînes tapées par un humain — la tenancy, elle, vit dans la query.
	AllowTrust(ctx context.Context, in TrustPairInput) (TrustDecision, error)
	RevokeTrust(ctx context.Context, in TrustPairInput) (TrustDecision, error)
	ListTrust(ctx context.Context, teamID uuid.UUID) ([]TrustEdge, error)
}

// service dépend de l'interface store, jamais de sqlc.
type service struct {
	store store.Store
}

// New crée le service workspace.
func New(st store.Store) Service {
	return &service{store: st}
}

// CreateTeamInput porte les données de création d'une team. Slug est l'identifiant lisible
// utilisé en CLI.
type CreateTeamInput struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Team est la vue API d'une team.
type Team struct {
	ID        uuid.UUID `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateProjectInput porte les données de création d'un projet. Key sert de préfixe aux
// identifiants lisibles (FRNT-34).
type CreateProjectInput struct {
	TeamID uuid.UUID `json:"-"`
	Key    string    `json:"key"`
	Name   string    `json:"name"`
}

// Project est la vue API d'un projet.
type Project struct {
	ID        uuid.UUID `json:"id"`
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateTokenInput porte les données d'émission d'un token d'agent, scopé à un projet.
type CreateTokenInput struct {
	TeamID     uuid.UUID `json:"-"`
	ProjectKey string    `json:"project"`
	Name       string    `json:"name"`
}

// CreatedToken est renvoyé une seule fois, à la création. Secret n'est ni stocké, ni
// réaffichable, ni journalisé.
type CreatedToken struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	Prefix     string    `json:"prefix"`
	ProjectKey string    `json:"project"`
	Secret     string    `json:"secret"`
}

// TokenInfo est la vue de listing : ni secret, ni hash.
type TokenInfo struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Revoked    bool       `json:"revoked"`
}

// TrustPairInput désigne une paire de projets par leurs CLÉS.
//
// Aucun UUID : les deux clés sont résolues DANS la query, sous le team_id déjà prouvé par
// teamFor. Un handler qui résoudrait les clés lui-même recréerait à la main l'énumération que le
// modèle refuse d'exposer.
//
// L'ordre des deux clés n'a AUCUNE signification : l'arête est une paire, pas une flèche.
type TrustPairInput struct {
	TeamID uuid.UUID `json:"-"`
	First  string    `json:"first"`
	Second string    `json:"second"`
}

// TrustDecision dit ce que l'écriture a effectivement changé, pour que la CLI distingue « fait »
// de « c'était déjà le cas » sans second aller-retour.
//
// Changed est faux sur un rejeu : `trust allow` d'une paire déjà ouverte, `trust deny` d'une paire
// déjà fermée. Les deux verbes sont idempotents, et c'est ce champ qui le rend visible.
type TrustDecision struct {
	First   string `json:"first"`
	Second  string `json:"second"`
	Changed bool   `json:"changed"`
}

// TrustEdge est une arête telle qu'exposée par l'API : deux clés et une date.
type TrustEdge struct {
	First     string    `json:"first"`
	Second    string    `json:"second"`
	CreatedAt time.Time `json:"created_at"`
}
