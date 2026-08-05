package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément       | Résumé                                                          | Ligne |
// |---------------|-----------------------------------------------------------------|-------|
// | Task          | Une tâche telle que persistée, dans le scope de son projet        | 47    |
// | Note          | Une note de progression attachée à une tâche                      | 64    |
// | NewTask       | Données d'insertion d'une tâche, numéro déjà réservé              | 72    |
// | TaskFilter    | Critères de lecture du backlog d'un projet                        | 85    |
// | TaskPatch     | Patch partiel d'une tâche : un champ nil laisse la valeur en place | 97    |
// | Dependency    | Une arête de blocage telle que persistée                          | 121   |
// | NewDependency | Données d'ouverture d'une arête de blocage                        | 137   |
// | Edge          | Une arête active, réduite à ses deux extrémités                   | 148   |
// | Event         | Une entrée du journal, écrite dans la transaction qui la produit  | 158   |
// | Store         | Contrat de persistance de la feature task                         | 175   |
// | store         | Implémentation adossée aux queries générées par sqlc              | 225   |
// | New           | Crée le store task                                                | 234   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRAT UNIQUEMENT — les implémentations sont dans task.go, note.go, dependency.go, event.go
// et tx.go.

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// ErrNotFound signale une ligne absente ou hors scope — les deux cas sont volontairement
// indiscernables. ErrConflict couvre les violations de contrainte ; ErrCorrupted signale une
// incohérence interne, qui n'est jamais la faute de l'appelant.
var (
	ErrNotFound  = errors.New("task store: not found")
	ErrConflict  = errors.New("task store: conflict")
	ErrCorrupted = errors.New("task store: corrupted state")
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

	// Archive sort la tâche du backlog actif, dans la MÊME écriture que le reste du patch.
	// Ce n'est pas une commodité : séparé, l'archivage était un second aller-retour, et une panne
	// entre les deux faisait rejouer à l'agent une note déjà écrite.
	Archive bool
}

// Dependency est une arête de blocage : « TaskID est bloquée par BlockerTaskID jusqu'à ce que
// celle-ci atteigne UntilStatus ».
//
// ProjectID n'est pas décoratif : c'est la MÊME colonne dans les deux clés étrangères composites
// de la table, donc les deux extrémités ne peuvent pas vivre dans des projets différents.
type Dependency struct {
	ID            uuid.UUID
	ProjectID     uuid.UUID
	TaskID        uuid.UUID
	BlockerTaskID uuid.UUID
	UntilStatus   string
	SetBlocked    bool
	CreatedAt     time.Time
	ReleasedAt    *time.Time
}

// NewDependency porte les données d'ouverture d'une arête.
//
// SetBlocked dit si c'est CETTE arête qui fait passer la tâche à `blocked`. Il est calculé par le
// service au moment de l'écriture — après, l'information est perdue : une tâche déjà bloquée est
// indiscernable d'une tâche bloquée par l'arête qu'on vient d'ouvrir.
type NewDependency struct {
	TeamID        uuid.UUID
	ProjectID     uuid.UUID
	TaskID        uuid.UUID
	BlockerTaskID uuid.UUID
	UntilStatus   string
	SetBlocked    bool
}

// Edge est une arête active réduite à ses deux extrémités. C'est tout ce dont le parcours de
// détection de cycle a besoin — le reste de la ligne ne ferait que traverser le réseau.
type Edge struct {
	TaskID        uuid.UUID
	BlockerTaskID uuid.UUID
}

// Event est une entrée du journal.
//
// La feature task porte sa propre écriture d'événement plutôt que d'emprunter celle de la feature
// issue : un module n'importe jamais un autre module. Les deux passent par la même query générée,
// ce qui garde une seule définition de la table.
type Event struct {
	TeamID         uuid.UUID
	ProjectID      uuid.UUID
	ActorProjectID uuid.UUID
	Kind           string
	SubjectID      uuid.UUID
}

// Store est le contrat consommé par le service.
//
// Toute méthode qui touche une tâche prend teamID ET projectID : il n'existe pas de lecture ni
// d'écriture non scopée dans ce contrat, donc aucun appelant ne peut en oublier une.
//
// Les méthodes d'arête prennent projectID sans teamID quand elles n'atteignent une tâche que par
// son identifiant : celui-ci vient toujours d'une lecture déjà scopée par team, et la table ne
// porte pas de team_id à comparer. Ce n'est pas un relâchement — c'est la même règle appliquée
// une couche plus haut, là où l'identifiant est né.
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

	AddNote(ctx context.Context, teamID, projectID uuid.UUID, number int64, body string) (Note, error)
	// ListNotes rend la fin du fil — au plus limit notes, dans l'ordre d'écriture — et le nombre
	// total de notes écrites. La borne est DANS la query : un `[:limit]` en Go aurait quand même
	// tiré le fil entier depuis Postgres, ce qui est précisément le coût qu'on refuse.
	ListNotes(ctx context.Context, teamID, projectID uuid.UUID, number int64, limit int32) ([]Note, int, error)

	CreateDependency(ctx context.Context, in NewDependency) (Dependency, error)

	// ReleaseBlockerEdges libère les arêtes qu'une tâche débloque en atteignant blockerStatus, et
	// rend les tâches ainsi libérées. force ignore la condition : une bloquante ARCHIVÉE
	// n'atteindra jamais rien, et laisser ses arêtes en place fabriquerait des tâches
	// mortes-vivantes.
	ReleaseBlockerEdges(ctx context.Context, projectID, blockerTaskID uuid.UUID, blockerStatus string, force bool) ([]uuid.UUID, error)

	// ReleaseEdge libère UNE arête nommée et rend la tâche libérée. Une arête absente ou déjà
	// libérée rend une liste vide, pas une erreur : les deux sont le même non-événement pour
	// l'appelant.
	ReleaseEdge(ctx context.Context, projectID, taskID, blockerTaskID uuid.UUID) ([]uuid.UUID, error)

	// ClearBlock ramène la tâche de `blocked` à `todo`, et seulement si toutes ses arêtes sont
	// libérées ET qu'au moins une l'avait bloquée. Les trois conditions sont dans la query, donc
	// aucun appelant ne peut en oublier une. false signifie « rien à changer », pas un échec.
	ClearBlock(ctx context.Context, teamID, projectID, taskID uuid.UUID) (bool, error)

	// ActiveEdges rend le graphe de blocage non libéré du projet, pour le parcours de détection de
	// cycle. Borné par nature : ce sont les blocages en cours d'un seul repo, pas son historique.
	ActiveEdges(ctx context.Context, projectID uuid.UUID) ([]Edge, error)

	// AppendEvent écrit une entrée du journal, toujours dans la transaction de ce qui la produit :
	// un événement écrit à part pourrait manquer alors que l'arête est libérée, et la tâche
	// débloquée ne l'apprendrait jamais — le manque exact que cette feature comble.
	AppendEvent(ctx context.Context, event Event) error
}

// store adosse le contrat aux queries générées. db ne sert qu'à ouvrir une transaction dans
// WithTx : aucune query ne passe par lui directement.
type store struct {
	q  *database.Queries
	db *sql.DB
	// inTx marque un store qui porte déjà une transaction, pour que WithTx refuse l'imbrication
	// au lieu d'en ouvrir une seconde sur une autre connexion.
	inTx bool
}

// New crée le store task.
func New(q *database.Queries, db *sql.DB) Store {
	return &store{q: q, db: db}
}
