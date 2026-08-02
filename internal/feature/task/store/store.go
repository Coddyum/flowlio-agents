package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément    | Résumé                                                             | Ligne |
// |------------|--------------------------------------------------------------------|-------|
// | Task       | Une tâche telle que persistée, dans le scope de son projet           | 40    |
// | Note       | Une note de progression attachée à une tâche                         | 57    |
// | NewTask    | Données d'insertion d'une tâche, numéro déjà réservé                 | 65    |
// | TaskFilter | Critères de lecture du backlog d'un projet                           | 78    |
// | TaskPatch  | Patch partiel d'une tâche : un champ nil laisse la valeur en place   | 90    |
// | Store      | Contrat de persistance de la feature task                            | 108   |
// | store      | Implémentation adossée aux queries générées par sqlc                 | 129   |
// | New        | Crée le store task                                                   | 135   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRAT UNIQUEMENT — les implémentations sont dans task.go, note.go et tx.go.

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-ia/internal/database"
	"github.com/google/uuid"
)

// ErrNotFound signale une ligne absente ou hors scope — les deux cas sont volontairement
// indiscernables. ErrConflict couvre les violations de contrainte.
var (
	ErrNotFound = errors.New("task store: not found")
	ErrConflict = errors.New("task store: conflict")
)

// Task est une tâche du backlog d'un projet. TeamID et ProjectID ne sont pas décoratifs : ils
// sont la clé de scope portée par chaque query.
type Task struct {
	ID         uuid.UUID
	TeamID     uuid.UUID
	ProjectID  uuid.UUID
	Number     int64
	Title      string
	Body       string
	Status     string
	Priority   string
	Deadline   *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ArchivedAt *time.Time
}

// Note est une note de progression. Elle ne porte pas de scope : elle n'est jamais lue autrement
// qu'à travers sa tâche, qui le porte.
type Note struct {
	ID        uuid.UUID
	Body      string
	CreatedAt time.Time
}

// NewTask porte les données d'insertion. Number est déjà réservé par ClaimNumber, dans la même
// transaction que l'insertion.
type NewTask struct {
	TeamID    uuid.UUID
	ProjectID uuid.UUID
	Number    int64
	Title     string
	Body      string
	Status    string
	Priority  string
	Deadline  *time.Time
}

// TaskFilter décrit une lecture du backlog. Status vide signifie « tous les statuts » ;
// les tâches archivées restent exclues sauf demande explicite.
type TaskFilter struct {
	TeamID          uuid.UUID
	ProjectID       uuid.UUID
	Status          string
	IncludeArchived bool
	Limit           int32
}

// TaskPatch est un patch partiel : un pointeur nil laisse le champ inchangé.
//
// ClearDeadline existe parce que nil veut déjà dire « ne change pas » — sans ce drapeau,
// effacer une échéance serait impossible à exprimer.
type TaskPatch struct {
	TeamID    uuid.UUID
	ProjectID uuid.UUID
	Number    int64

	Title    *string
	Body     *string
	Status   *string
	Priority *string

	Deadline      *time.Time
	ClearDeadline bool
}

// Store est le contrat consommé par le service.
//
// Toute méthode qui touche une tâche prend teamID ET projectID : il n'existe pas de lecture ni
// d'écriture non scopée dans ce contrat, donc aucun appelant ne peut en oublier une.
type Store interface {
	// WithTx exécute fn dans une transaction, sur un store qui la partage. *sql.DB ne remonte
	// jamais jusqu'au service : celui-ci ne voit que cette interface.
	WithTx(ctx context.Context, fn func(Store) error) error

	// ClaimNumber réserve le prochain identifiant lisible du projet (CORE-34). Le compteur est
	// partagé avec les issues : un numéro ne désigne jamais deux objets.
	ClaimNumber(ctx context.Context, teamID, projectID uuid.UUID) (int64, error)

	CreateTask(ctx context.Context, in NewTask) (Task, error)
	TaskByNumber(ctx context.Context, teamID, projectID uuid.UUID, number int64) (Task, error)
	ListTasks(ctx context.Context, filter TaskFilter) ([]Task, error)
	UpdateTask(ctx context.Context, patch TaskPatch) (Task, error)
	ArchiveTask(ctx context.Context, teamID, projectID uuid.UUID, number int64) (Task, error)

	AddNote(ctx context.Context, teamID, projectID uuid.UUID, number int64, body string) (Note, error)
	ListNotes(ctx context.Context, teamID, projectID uuid.UUID, number int64) ([]Note, error)
}

// store adosse le contrat aux queries générées. db ne sert qu'à ouvrir une transaction dans
// WithTx : aucune query ne passe par lui directement.
type store struct {
	q  *database.Queries
	db *sql.DB
}

// New crée le store task.
func New(q *database.Queries, db *sql.DB) Store {
	return &store{q: q, db: db}
}
