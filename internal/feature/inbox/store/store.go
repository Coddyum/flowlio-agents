package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément   | Résumé                                                              | Ligne |
// |-----------|---------------------------------------------------------------------|-------|
// | Scope     | Scope complet d'une lecture d'inbox, curseur compris                  | 40    |
// | Cursor    | Position de lecture du token et tête du journal de la team            | 51    |
// | IssueLine | Une issue actionnable, résumée pour l'inbox                           | 61    |
// | TaskLine  | Une tâche en cours, résumée pour l'inbox                              | 73    |
// | Store     | Contrat de lecture de l'état actionnable d'un projet                  | 85    |
// | store     | Implémentation adossée aux queries générées par sqlc                  | 104   |
// | New       | Crée le store inbox                                                   | 109   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRAT UNIQUEMENT — l'implémentation est dans inbox.go.
//
// Pas de Transactor ici, délibérément : l'inbox ne renvoie pas un flux d'événements mais l'état
// courant, recalculé à chaque appel. Sa justesse ne dépend d'aucune atomicité — le curseur ne
// pilote qu'un drapeau d'affichage, jamais la présence d'une ligne. Voir docs/DESIGN-M3.md.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// ErrNotFound signale un projet introuvable dans le scope demandé.
var ErrNotFound = errors.New("inbox store: not found")

// Scope porte tout ce qui identifie une lecture d'inbox.
//
// TokenID vient du Principal au même titre que la team et le projet : le curseur est par TOKEN,
// pas par projet, pour que deux sessions d'agent sur le même repo aient chacune leur avancement.
type Scope struct {
	TokenID   uuid.UUID
	TeamID    uuid.UUID
	ProjectID uuid.UUID
	Limit     int32
}

// Cursor porte la position de lecture du token et la tête du journal de la team.
//
// La tête est capturée AVANT le calcul des seaux : un événement écrit pendant l'appel restera
// donc « nouveau » au prochain tour, plutôt que d'être silencieusement dépassé.
type Cursor struct {
	LastEventID int64
	HeadEventID int64
}

// IssueLine est une issue actionnable, résumée.
//
// Excerpt est le dernier message, tronqué : l'inbox doit tenir dans le contexte d'un agent qui
// démarre. New indique qu'un événement la concernant est postérieur au curseur — c'est un
// confort de lecture, pas un critère de présence : la ligne est là parce que l'ÉTAT le dit.
type IssueLine struct {
	Number    int64
	Title     string
	PeerKey   string
	Excerpt   string
	Truncated bool
	New       bool
	UpdatedAt time.Time
}

// TaskLine est une tâche en cours, résumée. Pas de drapeau « nouveau » : c'est le travail de
// l'agent lui-même, rien ne peut lui en apprendre l'existence.
type TaskLine struct {
	Number    int64
	Title     string
	Priority  string
	UpdatedAt time.Time
}

// Store lit l'état actionnable d'un projet.
//
// Le journal d'événements n'est jamais interrogé par un prédicat propre : il est atteint par un
// EXISTS sur un sujet DÉJÀ scopé. Il n'existe donc aucune lecture capable de révéler l'activité
// d'un projet tiers.
type Store interface {
	// ProjectKey résout la clé du projet du token, nécessaire pour composer les références.
	ProjectKey(ctx context.Context, teamID, projectID uuid.UUID) (string, error)

	Cursor(ctx context.Context, sc Scope) (Cursor, error)

	// IncomingOpen : les questions qu'on attend de moi.
	IncomingOpen(ctx context.Context, sc Scope, lastEventID int64) ([]IssueLine, error)
	// OutgoingAnswered : mes questions qui ont reçu une réponse.
	OutgoingAnswered(ctx context.Context, sc Scope, lastEventID int64) ([]IssueLine, error)
	// InProgressTasks : mon travail interrompu.
	InProgressTasks(ctx context.Context, sc Scope) ([]TaskLine, error)

	// Advance avance le curseur du token, sans jamais le faire reculer.
	Advance(ctx context.Context, tokenID uuid.UUID, headEventID int64) error
}

// store adosse le contrat aux queries générées. Pas de *sql.DB : l'inbox n'ouvre jamais de
// transaction.
type store struct {
	q *database.Queries
}

// New crée le store inbox.
func New(q *database.Queries) Store {
	return &store{q: q}
}
