package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément    | Résumé                                                             | Ligne |
// |------------|--------------------------------------------------------------------|-------|
// | Service    | Contrat consommé par le handler inbox                                | 54    |
// | service    | Implémentation, dépendante de l'interface store                      | 63    |
// | New        | Crée le service inbox                                                | 68    |
// | CheckInput | Scope de l'appel, entièrement issu du token                          | 74    |
// | IssueLine  | Une issue actionnable telle qu'exposée                               | 82    |
// | TaskLine   | Une tâche en cours telle qu'exposée                                  | 94    |
// | UnblockedLine | Une tâche que plus aucune dépendance interne ne bloque            | 105   |
// | More       | Ce qui n'a pas tenu dans les seaux                                   | 115   |
// | Inbox      | L'état actionnable du projet, en quatre seaux                        | 131   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRAT UNIQUEMENT — l'implémentation est dans check.go.
//
// POURQUOI UN ÉTAT ET NON UN FLUX — décision structurante de docs/DESIGN-M3.md.
//
// check_inbox ne renvoie PAS les événements survenus depuis le dernier appel. Il renvoie ce
// qu'il y a à faire MAINTENANT, recalculé à chaque appel depuis issues.state et tasks.status.
//
// Conséquence : aucune notification ne peut être perdue. Un flux devrait garantir une livraison
// exactement-une-fois — un identifiant de séquence est attribué à l'insertion et non au commit,
// donc une transaction lente peut committer un numéro déjà dépassé par un lecteur, et l'issue
// correspondante deviendrait invisible pour toujours. Ici ce défaut coûte au pire un drapeau
// « nouveau » manquant.
//
// Deux appels successifs renvoient donc la même chose s'il ne s'est rien passé. C'est une
// propriété, pas un défaut : un agent dont le contexte vient d'être compacté doit retrouver son
// travail en cours, pas conclure « rien à faire » parce qu'un autre appel l'avait déjà lu.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/inbox/store"
	"github.com/google/uuid"
)

// Erreurs domaine, traduites en codes HTTP par le handler.
var (
	ErrInvalidInput = errors.New("inbox: invalid input")
	ErrNotFound     = errors.New("inbox: not found")
)

// Service répond à la seule question qu'un agent pose en début de session : qu'est-ce qui
// m'attend ?
type Service interface {
	// Check renvoie l'état actionnable du projet et avance le curseur du token.
	//
	// L'avancement du curseur ne conditionne aucune ligne : il ne fait que retirer le drapeau
	// « nouveau » au prochain appel.
	Check(ctx context.Context, in CheckInput) (Inbox, error)
}

// service dépend de l'interface store, jamais de sqlc.
type service struct {
	store store.Store
}

// New crée le service inbox.
func New(st store.Store) Service {
	return &service{store: st}
}

// CheckInput porte le scope de l'appel. Tous ses champs viennent du token : cet appel n'a
// littéralement aucun paramètre d'entrée côté agent.
type CheckInput struct {
	TokenID   uuid.UUID `json:"-"`
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`
}

// IssueLine est une issue actionnable. Ref porte toujours la clé du projet DESTINATAIRE : c'est
// celle qu'il faudra réutiliser pour répondre.
type IssueLine struct {
	Ref       string    `json:"ref"`
	Title     string    `json:"title"`
	Peer      string    `json:"peer"`
	Excerpt   string    `json:"excerpt"`
	Truncated bool      `json:"truncated,omitempty"`
	New       bool      `json:"new"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TaskLine est une tâche en cours. Pas de drapeau « nouveau » : c'est le travail de l'agent
// lui-même.
type TaskLine struct {
	Ref      string `json:"ref"`
	Title    string `json:"title"`
	Priority string `json:"priority"`
}

// UnblockedLine est une tâche dont plus aucune dépendance interne ne bloque l'avancement.
//
// Status distingue les deux issues du déblocage, et c'est l'information utile du seau : `todo` dit
// « reprends-la », `blocked` dit « ton obstacle est levé, mais tu l'avais bloquée toi-même pour
// autre chose et personne n'a décidé à ta place ».
type UnblockedLine struct {
	Ref      string `json:"ref"`
	Title    string `json:"title"`
	Priority string `json:"priority"`
	Status   string `json:"status"`
	New      bool   `json:"new"`
}

// More compte ce qui n'a pas tenu dans les seaux, pour qu'un agent sache qu'il ne voit pas tout
// et aille chercher le reste avec list_issues ou list_tasks.
type More struct {
	NeedsAnswer int `json:"needs_answer,omitempty"`
	Answered    int `json:"answered,omitempty"`
	InProgress  int `json:"in_progress,omitempty"`
	Unblocked   int `json:"unblocked,omitempty"`
}

// Inbox est l'état actionnable du projet, en quatre seaux :
//   - NeedsAnswer : quelqu'un est bloqué sur moi ;
//   - Answered    : j'étais bloqué sur un AUTRE repo, je ne le suis plus ;
//   - InProgress  : mon propre travail interrompu ;
//   - Unblocked   : j'étais bloqué par une autre tâche de CE repo, je ne le suis plus.
//
// Les deux formes de déblocage sont des seaux distincts et doivent le rester : l'une se règle en
// répondant à un pair (answer_issue), l'autre en reprenant son propre travail. Les confondre
// obligerait l'agent à relire chaque ligne pour savoir laquelle des deux il regarde.
type Inbox struct {
	Project     string          `json:"project"`
	NeedsAnswer []IssueLine     `json:"needs_answer"`
	Answered    []IssueLine     `json:"answered"`
	InProgress  []TaskLine      `json:"in_progress"`
	Unblocked   []UnblockedLine `json:"unblocked"`
	More        *More           `json:"more,omitempty"`
}
