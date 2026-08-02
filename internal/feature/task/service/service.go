package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                        | Ligne |
// |-----------------|---------------------------------------------------------------|-------|
// | Service         | Contrat consommé par le handler task                            | 45    |
// | service         | Implémentation, dépendante de l'interface store                 | 62    |
// | New             | Crée le service task                                            | 67    |
// | Task            | Une tâche telle qu'exposée par l'API                            | 73    |
// | Note            | Une note de progression exposée par l'API                       | 86    |
// | TaskDetail      | Une tâche et son fil de notes                                   | 92    |
// | CreateTaskInput | Entrée de création d'une tâche                                  | 99    |
// | ListTasksInput  | Critères de lecture du backlog                                  | 114   |
// | UpdateTaskInput | Patch partiel d'une tâche                                       | 127   |
// | AddNoteInput    | Entrée d'ajout d'une note de progression                        | 142   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRAT UNIQUEMENT — les implémentations sont dans create_task.go, list_tasks.go,
// get_task.go, update_task.go, add_note.go et archive_task.go.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-ia/internal/feature/task/store"
	"github.com/google/uuid"
)

// Erreurs domaine, traduites en codes HTTP par le handler via errors.Is.
var (
	ErrInvalidInput = errors.New("task: invalid input")
	ErrNotFound     = errors.New("task: not found")
	ErrConflict     = errors.New("task: conflict")
)

// Service porte le backlog d'un projet.
//
// Chaque méthode prend teamID et projectID : ils viennent du Principal du token, jamais du corps
// de la requête. Un agent ne peut donc pas désigner le backlog d'un autre projet, même en
// forgeant sa requête.
type Service interface {
	CreateTask(ctx context.Context, in CreateTaskInput) (Task, error)
	ListTasks(ctx context.Context, in ListTasksInput) ([]Task, error)

	// GetTask renvoie la tâche et son fil de notes : reprendre une tâche interrompue demande les
	// deux, et deux allers-retours coûteraient un tour d'agent de plus.
	GetTask(ctx context.Context, teamID, projectID uuid.UUID, number int64) (TaskDetail, error)

	UpdateTask(ctx context.Context, in UpdateTaskInput) (Task, error)
	AddNote(ctx context.Context, in AddNoteInput) (Note, error)

	// ArchiveTask sort une tâche du backlog actif sans la supprimer : l'historique d'un repo se
	// range, il ne s'efface pas.
	ArchiveTask(ctx context.Context, teamID, projectID uuid.UUID, number int64) (Task, error)
}

// service dépend de l'interface store, jamais de sqlc.
type service struct {
	store store.Store
}

// New crée le service task.
func New(st store.Store) Service {
	return &service{store: st}
}

// Task est la vue API d'une tâche. Number est l'identifiant lisible dans le projet : associé à
// la clé du projet, il donne CORE-34.
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

// Note est la vue API d'une note de progression.
type Note struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// TaskDetail est une tâche accompagnée de son fil de notes, du plus ancien au plus récent.
type TaskDetail struct {
	Task
	Notes []Note `json:"notes"`
}

// CreateTaskInput porte les données de création. TeamID et ProjectID viennent du token et ne
// sont jamais lus depuis le corps de la requête.
type CreateTaskInput struct {
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`

	Title    string     `json:"title"`
	Body     string     `json:"body"`
	Status   string     `json:"status"`
	Priority string     `json:"priority"`
	Deadline *time.Time `json:"deadline"`
}

// ListTasksInput décrit une lecture du backlog.
//
// Status vide signifie « tous les statuts ». Les tâches archivées sont exclues par défaut : un
// agent qui demande son travail en cours ne doit pas payer en tokens l'historique du repo.
type ListTasksInput struct {
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`

	Status          string `json:"status"`
	IncludeArchived bool   `json:"include_archived"`
	Limit           int    `json:"limit"`
}

// UpdateTaskInput est un patch : un champ absent du JSON reste nil et laisse la valeur en place.
//
// ClearDeadline est nécessaire parce que `"deadline": null` est indiscernable d'un champ absent
// une fois décodé — sans ce drapeau, effacer une échéance serait impossible à exprimer.
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
}

// AddNoteInput porte une note de progression à ajouter au fil d'une tâche.
type AddNoteInput struct {
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`
	Number    int64     `json:"-"`

	Body string `json:"body"`
}
