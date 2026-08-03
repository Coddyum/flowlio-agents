package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                              | Ligne |
// |-------------|---------------------------------------------------------------------|-------|
// | Service     | Contrat consommé par le handler overview                              | 77    |
// | service     | Implémentation, dépendante de l'interface store                       | 89    |
// | New         | Crée le service overview                                              | 94    |
// | Team        | Identité d'une team résolue par son slug                              | 100   |
// | ProjectLine | Un repo, ses cinq compteurs et son pouls                              | 111   |
// | Debt        | Une ligne de dette, déjà classée                                      | 129   |
// | TeamState   | L'écran d'ensemble : les repos, les dettes, ce qui est caché          | 144   |
// | Message     | Un message du fil, tel qu'exposé                                      | 152   |
// | Note        | Une note de progression, telle qu'exposée                             | 159   |
// | RefDetail   | Le détail d'une référence, polymorphe entre issue et tâche            | 175   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRAT UNIQUEMENT — l'implémentation est dans team_state.go, ref_detail.go et validate.go.
//
// CE QUE CETTE SURFACE NE REND PAS, ET POURQUOI. Chaque coupe ci-dessous a été payée une fois ;
// les rouvrir se fait avec un argument, pas par confort de client :
//
//   - aucun UUID, nulle part. La team est désignée par un slug, tout le reste par une référence
//     `CLÉ-numéro`. Un identifiant opaque rendu à un client devient un identifiant qu'on accepte
//     en entrée le jour d'après.
//   - pas de `health`, pas de `is_stale`, pas de durée en secondes, pas de couleur. « Trois jours
//     = rouge » est une politique de rendu : la coder ici la rend fausse pour la team suivante.
//   - pas de drapeau « nouveau ». Le curseur appartient à un token d'agent ; un « déjà vu »
//     humain serait une nouvelle table, donc une écriture sur une surface déclarée en lecture
//     seule.
//   - pas d'extrait sur les lignes de dette. Cinquante extraits ne se lisent pas en trois
//     secondes, et le détail est à un appel de là.
//   - pas d'écho du slug de team. L'appelant l'a fourni ; le lui renvoyer invite à faire de cette
//     réponse la source des métadonnées de team.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/overview/store"
	"github.com/google/uuid"
)

// Erreurs domaine, traduites en codes HTTP par le handler.
//
// ErrNotFound couvre indifféremment « slug inconnu », « team qui n'est pas la tienne » et
// « référence introuvable » : les distinguer donnerait un oracle permettant d'énumérer les teams
// de l'installation par balayage de slugs.
var (
	ErrInvalidInput = errors.New("overview: invalid input")
	ErrNotFound     = errors.New("overview: not found")
)

// Les quatre kinds de dette. La classification est TOTALE : toute ligne rendue par le store
// tombe dans exactement un kind, ou est omise pour une raison nommée (§ team_state.go).
const (
	// KindAnswer — un agent frère est bloqué sur ce repo.
	KindAnswer = "answer"
	// KindCollect — il a sa réponse et ne l'a pas consommée.
	KindCollect = "collect"
	// KindResume — session vraisemblablement morte en cours de tâche.
	KindResume = "resume"
	// KindAsk — il s'est déclaré coincé sans rien demander à personne.
	KindAsk = "ask"
)

// Service répond aux deux questions d'un superviseur humain : « où en sont mes repos » et
// « qu'est-ce qui se dit sur CORE-41 ».
//
// TeamBySlug est exposée parce que le handler doit résoudre le scope AVANT de le passer aux deux
// autres méthodes, et parce que c'est ce même appel qui refuse un admin épinglé à une autre team.
// Les deux autres méthodes ne prennent qu'un teamID déjà résolu : un slug ne descend jamais ici.
type Service interface {
	// TeamBySlug résout un slug en identité de team, ou rend ErrNotFound.
	TeamBySlug(ctx context.Context, slug string) (Team, error)

	// TeamState assemble les repos, leur pouls et la file de dettes de la team.
	TeamState(ctx context.Context, teamID uuid.UUID) (TeamState, error)

	// RefDetail rend le détail d'une référence : issue d'abord, tâche ensuite.
	RefDetail(ctx context.Context, teamID uuid.UUID, projectKey string, number int64) (RefDetail, error)
}

// service dépend de l'interface store, jamais de sqlc.
type service struct {
	store store.Store
}

// New crée le service overview.
func New(st store.Store) Service {
	return &service{store: st}
}

// Team est l'identité d'une team résolue par son slug. Elle ne sort jamais telle quelle vers le
// client : le handler n'en garde que l'ID, pour scoper les deux autres appels.
type Team struct {
	ID   uuid.UUID
	Slug string
	Name string
}

// ProjectLine est un repo, ses cinq compteurs et son pouls.
//
// LastAgentSeenAt est un HORODATAGE, jamais une durée : une durée périme dans le client. Il est
// absent — et non nul — pour un repo dont aucun token n'a encore servi : « pas de pouls » et
// « pouls au 1er janvier de l'an 1 » ne sont pas la même chose.
type ProjectLine struct {
	Key             string     `json:"key"`
	OwesAnswer      int64      `json:"owes_answer"`
	AwaitingAnswer  int64      `json:"awaiting_answer"`
	AnsweredUnread  int64      `json:"answered_unread"`
	TasksRunning    int64      `json:"tasks_running"`
	TasksBlocked    int64      `json:"tasks_blocked"`
	LastAgentSeenAt *time.Time `json:"last_agent_seen_at,omitempty"`
}

// Debt est une ligne de dette, déjà classée : le client n'a aucune règle métier à rejouer.
//
// Ref porte toujours la clé du DESTINATAIRE de l'issue ou du propriétaire de la tâche — c'est
// celle qu'on retape pour ouvrir le détail. Debtor est celui qui doit agir, et les deux diffèrent
// sur `collect` : l'issue est `CORE-41`, mais c'est WEB qui doit aller lire sa réponse.
//
// Peer est vide sur `resume` et `ask`, qui n'ont pas de correspondant. Since agrège deux colonnes
// différentes (`issues.updated_at` et `last_move`), d'où son nom plutôt qu'`updated_at`.
type Debt struct {
	Kind   string    `json:"kind"`
	Ref    string    `json:"ref"`
	Debtor string    `json:"debtor"`
	Peer   string    `json:"peer,omitempty"`
	Title  string    `json:"title"`
	Since  time.Time `json:"since"`
}

// TeamState est l'écran d'ensemble.
//
// GeneratedAt est l'horloge depuis laquelle le client calcule TOUS les âges : s'il utilisait la
// sienne, une dérive produirait « il y a −3 s ». Truncated compte les dettes que la borne a
// cachées ; sans lui, une liste tronquée ment, et l'écran est faux d'une manière silencieuse et
// crédible.
type TeamState struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Projects    []ProjectLine `json:"projects"`
	Debts       []Debt        `json:"debts"`
	Truncated   int           `json:"truncated"`
}

// Message est un message du fil, désigné par la clé du projet qui l'a écrit.
type Message struct {
	From      string    `json:"from"`
	CreatedAt time.Time `json:"created_at"`
	Body      string    `json:"body"`
}

// Note est une note de progression.
type Note struct {
	CreatedAt time.Time `json:"created_at"`
	Body      string    `json:"body"`
}

// RefDetail est le détail d'une référence, polymorphe entre issue et tâche.
//
// UN SEUL TYPE, ET NON DEUX. `kind` est le premier champ, et c'est lui qui dit quels champs sont
// renseignés — miroir exact de ce que la surface MCP fait déjà. Deux structs auraient forcé un
// `any` dans la signature du service, ce que code-conventions.md refuse.
//
// MessagesTotal et NotesTotal ne sont émis QUE s'ils dépassent le nombre de lignes rendues :
// « 3 notes, 3 rendues » n'apprend rien, « 3 rendues sur 47 » change la lecture.
//
// closed_at est coupé : `state` et `updated_at` le disent déjà, et une troisième source de la
// même information est une divergence en attente.
type RefDetail struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`

	// Issue uniquement.
	From  string `json:"from,omitempty"`
	State string `json:"state,omitempty"`

	// Tâche uniquement.
	Status   string `json:"status,omitempty"`
	Priority string `json:"priority,omitempty"`

	Title     string     `json:"title"`
	Body      string     `json:"body,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Deadline  *time.Time `json:"deadline,omitempty"`

	Messages      []Message `json:"messages,omitempty"`
	MessagesTotal int       `json:"messages_total,omitempty"`
	Notes         []Note    `json:"notes,omitempty"`
	NotesTotal    int       `json:"notes_total,omitempty"`
}
