package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                        | Ligne |
// |-----------------|---------------------------------------------------------------|-------|
// | Service         | Contrat consommé par le handler task                            | 50    |
// | service         | Implémentation, dépendante de l'interface store                 | 78    |
// | New             | Crée le service task                                            | 83    |
// | Task            | Une tâche telle qu'exposée par l'API                            | 89    |
// | Note            | Une note de progression exposée par l'API                       | 102   |
// | TaskDetail      | Une tâche et son fil de notes                                   | 113   |
// | CreateTaskInput | Entrée de création d'une tâche                                  | 121   |
// | ListTasksInput  | Critères de lecture du backlog                                  | 136   |
// | UpdateTaskInput | Patch partiel d'une tâche, note de progression comprise         | 149   |
// | BlockTaskInput  | Ouverture d'une arête de blocage entre deux tâches du projet     | 187   |
// | UnblockTaskInput| Libération à la main d'une arête nommée                          | 201   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRAT UNIQUEMENT — les implémentations sont dans create_task.go, list_tasks.go,
// get_task.go et update_task.go.
//
// Il n'y a PAS de méthode d'archivage : archiver est un champ d'UpdateTask, écrit dans la même
// transaction que le reste. Une seule écriture sur une tâche, donc une seule surface à sécuriser
// et aucune couture non atomique entre deux appels.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
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

	// UpdateTask applique un patch et, si Note est fourni, écrit la note de progression dans la
	// MÊME transaction : « passer en done et dire pourquoi » est une seule intention, donc une
	// seule écriture, qui réussit ou échoue d'un bloc.
	//
	// C'est aussi ce qui LIBÈRE les arêtes de blocage : une tâche qui atteint son statut de
	// libération, ou qui est archivée, débloque celles qui l'attendaient, dans la même transaction.
	// Un chemin séparé aurait rendu possible « la bloquante est done, la bloquée l'ignore ».
	UpdateTask(ctx context.Context, in UpdateTaskInput) (Task, error)

	// BlockTask ouvre une arête « cette tâche est bloquée par une autre du MÊME projet, jusqu'à ce
	// que celle-ci atteigne Until ». Renvoie la tâche bloquée dans son état d'après.
	BlockTask(ctx context.Context, in BlockTaskInput) (Task, error)

	// UnblockTask libère une arête à la main, sans attendre que la bloquante avance. Le retour à
	// `todo` obéit à la même règle que la libération automatique : seulement si l'arête avait posé
	// le blocage et qu'aucune autre ne subsiste.
	UnblockTask(ctx context.Context, in UnblockTaskInput) (Task, error)
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

// TaskDetail est une tâche accompagnée de la FIN de son fil de notes, du plus ancien au plus
// récent.
//
// NotesTotal existe pour que l'agent sache qu'il ne lit qu'une fenêtre — même raison que
// MessagesTotal côté issue. Sans ce compteur, un fil tronqué est indiscernable d'un fil court, et
// l'agent conclut qu'il n'y a rien d'autre à savoir sur la tâche qu'il reprend.
type TaskDetail struct {
	Task
	Notes      []Note `json:"notes"`
	NotesTotal int    `json:"notes_total"`
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

	// Note ajoute une note de progression au fil, dans la même transaction que le patch.
	//
	// C'est un champ et non une opération séparée parce que l'intention réelle d'un agent est
	// « passer en done ET dire pourquoi » : deux écritures rendaient possible un statut changé
	// sans son motif, et coûtaient un aller-retour de plus à chaque tour.
	// Une chaîne vide est refusée : une note sans contenu n'apprend rien à la session suivante.
	Note *string `json:"note"`

	// Archive sort la tâche du backlog actif, dans la MÊME écriture que le reste du patch.
	//
	// Champ et non opération séparée, pour la raison qui a replié la note : l'archivage était un
	// second aller-retour HTTP, et l'atomicité s'arrêtait à cette frontière. Une panne entre les
	// deux commitait la note sans archiver, l'agent lisait une erreur et rejouait — ce qui écrivait
	// la note une seconde fois. Replié, l'appel réussit ou échoue d'un bloc.
	//
	// Archiver LIBÈRE aussi les arêtes que cette tâche bloquait : archivée, elle n'atteindra jamais
	// son statut de libération, et les laisser en place fabriquerait des tâches mortes-vivantes.
	Archive bool `json:"archive"`
}

// BlockTaskInput ouvre une arête « Number est bloquée par Blocker ».
//
// Blocker est un NUMÉRO, pas une référence : il n'existe pas de forme inter-repos, et ce n'est pas
// un manque. Une dépendance qui traverse un dépôt a déjà son objet, l'issue (D42). La garde tient
// en base — les deux extrémités de l'arête partagent la même colonne project_id — et non ici.
type BlockTaskInput struct {
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`
	Number    int64     `json:"-"`

	Blocker int64 `json:"blocker"`

	// Until est le statut que la bloquante doit atteindre pour libérer l'arête. Vide vaut `done`.
	// Seuls `in_progress` et `done` sont acceptés : les deux autres ne sont pas des progrès, et
	// une arête qui se libère sur `todo` naîtrait déjà libérée.
	Until string `json:"until"`
}

// UnblockTaskInput libère à la main l'arête entre Number et Blocker.
type UnblockTaskInput struct {
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`
	Number    int64     `json:"-"`

	Blocker int64 `json:"-"`
}
