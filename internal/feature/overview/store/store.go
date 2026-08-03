package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                          | Ligne |
// |-----------------|-----------------------------------------------------------------|-------|
// | Team            | Identité d'une team, telle que le slug la résout                  | 55    |
// | ProjectCounters | Les cinq compteurs d'un repo                                      | 64    |
// | ProjectPulse    | Dernier appel authentifié d'un token du repo                      | 76    |
// | IssueDebt       | Une issue en vol, sans corps ni extrait                           | 87    |
// | TaskDebt        | Une tâche sur laquelle un humain peut agir                        | 102   |
// | Issue           | Le fil d'une issue, vu par un tiers                               | 116   |
// | Message         | Un message du fil, désigné par la clé de son auteur               | 128   |
// | Task            | Une tâche désignée par sa référence                               | 136   |
// | Note            | Une note de progression                                           | 150   |
// | Store           | Contrat de lecture team-scopée, sans aucune écriture              | 161   |
// | store           | Implémentation adossée aux queries générées par sqlc              | 177   |
// | New             | Crée le store overview                                            | 182   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRAT UNIQUEMENT — l'implémentation est dans team.go, projects.go, debts.go, thread.go et
// task.go.
//
// Pas de Transactor, et il n'y en aura pas : cette surface est en LECTURE SEULE. Aucune de ses
// méthodes n'écrit une ligne, aucune ne peut donc avoir besoin d'atomicité. La règle est gardée
// par scripts/check-overview-scope.sh, qui refuse tout INSERT/UPDATE/DELETE dans
// sql/queries/overview.sql.
//
// L'INVARIANT DE SIGNATURE EST LE VRAI GARDE-FOU. Le slug de team ne descend jamais sous le
// handler : toutes les méthodes prennent un `teamID uuid.UUID` non-nullable en premier paramètre,
// à la seule exception de TeamBySlug, qui est celle qui le PRODUIT. Un appelant ne peut donc pas
// oublier un scope — il ne peut même pas l'exprimer.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// ErrNotFound signale une ligne introuvable dans le scope demandé — team inconnue, ou référence
// qui n'appartient pas à la team résolue.
//
// UN SEUL SENTINEL POUR LES DEUX CAS, délibérément : « cette team existe mais pas pour toi » et
// « cette team n'existe pas » doivent être indiscernables du dehors, sinon un balayage de slugs
// énumère les teams de l'installation.
var ErrNotFound = errors.New("overview store: not found")

// Team est l'identité d'une team, et rien de plus. Elle ne grossira pas : cette forme est le
// produit de la seule query du fichier qui n'est pas scopée par team_id.
type Team struct {
	ID   uuid.UUID
	Slug string
	Name string
}

// ProjectCounters porte les cinq compteurs d'un repo. Une ligne existe TOUJOURS, y compris pour
// un repo qui n'a rien en vol : un repo qui disparaît de l'écran du superviseur est le seul
// défaut irrécupérable de cette surface, il ne peut pas chercher ce qu'il ne voit pas.
type ProjectCounters struct {
	Key            string
	OwesAnswer     int64
	AwaitingAnswer int64
	AnsweredUnread int64
	TasksRunning   int64
	TasksBlocked   int64
}

// ProjectPulse est le dernier appel authentifié d'un token du repo. Un repo dont aucun token n'a
// jamais servi n'a pas de ligne du tout : il n'a pas de pouls, ce qui n'est pas la même chose
// qu'un pouls à zéro.
type ProjectPulse struct {
	Key      string
	LastSeen time.Time
}

// IssueDebt est une issue en vol. Ni corps ni extrait : cinquante extraits ne se lisent pas en
// trois secondes, et le détail est à un appel de là.
//
// ProjectKey est le DESTINATAIRE, AuthorProjectKey l'émetteur. C'est le couple qui décide du
// débiteur : sur une issue `open` c'est le destinataire qui doit une réponse, sur une issue
// `answered` c'est l'émetteur qui doit aller la chercher.
type IssueDebt struct {
	Number           int64
	State            string
	Title            string
	ProjectKey       string
	AuthorProjectKey string
	UpdatedAt        time.Time
}

// TaskDebt est une tâche sur laquelle un humain peut agir.
//
// LastMove n'est pas updated_at : c'est le plus récent de updated_at et de la dernière note, sans
// quoi un agent qui documente activement sa progression serait signalé « session morte ».
// HasOpenQuestion distingue « bloqué et il a demandé » de « bloqué et il n'a rien demandé » — le
// second est le seul cul-de-sac que rien d'autre dans le produit ne rend visible.
type TaskDebt struct {
	Number          int64
	Status          string
	Priority        string
	Title           string
	ProjectKey      string
	Deadline        *time.Time
	LastMove        time.Time
	HasOpenQuestion bool
}

// Issue est le fil d'une issue, vu par un tiers qui n'en est ni l'auteur ni le destinataire.
// CreatedAt ET UpdatedAt : « ouverte depuis 5 jours, silence depuis 3 » sont deux informations
// différentes pour un superviseur.
type Issue struct {
	ID               uuid.UUID
	Number           int64
	State            string
	Title            string
	ProjectKey       string
	AuthorProjectKey string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Message est un message du fil, désigné par la clé du projet qui l'a écrit.
type Message struct {
	AuthorKey string
	BodyMd    string
	CreatedAt time.Time
}

// Task est une tâche désignée par sa référence, corps compris : c'est le seul endroit du produit
// où un humain le lit sans le token du repo.
type Task struct {
	ID         uuid.UUID
	Number     int64
	Status     string
	Priority   string
	Title      string
	BodyMd     string
	ProjectKey string
	Deadline   *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Note est une note de progression. C'est la vraie réponse à « pourquoi c'est bloqué ».
type Note struct {
	BodyMd    string
	CreatedAt time.Time
}

// Store lit l'état d'une team entière. C'est le SEUL store du dépôt dont les lectures ne portent
// pas de prédicat de projet, et c'est pour ça qu'il vit dans son propre module : voisiner une
// méthode team-seule et une méthode project-scopée est la configuration où le copier-coller fuit.
//
// Les méthodes qui bornent leur résultat rendent aussi le total AVANT la borne : sans lui, une
// liste tronquée ment, et l'écran est faux d'une manière silencieuse et crédible.
type Store interface {
	// TeamBySlug est la SEULE méthode sans teamID : c'est celle qui le produit.
	TeamBySlug(ctx context.Context, slug string) (Team, error)

	Projects(ctx context.Context, teamID uuid.UUID) ([]ProjectCounters, error)
	LastSeen(ctx context.Context, teamID uuid.UUID) ([]ProjectPulse, error)
	IssueDebts(ctx context.Context, teamID uuid.UUID, limit int32) ([]IssueDebt, int64, error)
	TaskDebts(ctx context.Context, teamID uuid.UUID, staleBefore time.Time, limit int32) ([]TaskDebt, int64, error)

	IssueByRef(ctx context.Context, teamID uuid.UUID, projectKey string, number int64) (Issue, error)
	IssueMessages(ctx context.Context, teamID, issueID uuid.UUID, limit int32) ([]Message, int64, error)
	TaskByRef(ctx context.Context, teamID uuid.UUID, projectKey string, number int64) (Task, error)
	TaskNotes(ctx context.Context, teamID, taskID uuid.UUID, limit int32) ([]Note, int64, error)
}

// store adosse le contrat aux queries générées. Pas de *sql.DB : aucune transaction, jamais.
type store struct {
	q *database.Queries
}

// New crée le store overview.
func New(q *database.Queries) Store {
	return &store{q: q}
}
