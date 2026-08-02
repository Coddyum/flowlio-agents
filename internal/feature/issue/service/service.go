package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                      | Ligne |
// |------------------|-------------------------------------------------------------|-------|
// | Service          | Contrat consommé par le handler issue                         | 48    |
// | service          | Implémentation, dépendante de l'interface store               | 61    |
// | New              | Crée le service issue                                         | 66    |
// | Issue            | Une issue telle qu'exposée par l'API                          | 75    |
// | Message          | Un message du fil, attribué au projet qui l'a écrit           | 86    |
// | IssueDetail      | Une issue et son fil de messages                              | 96    |
// | Ref              | Désigne CORE-34 pour l'appelant, scope compris                | 106   |
// | CreateIssueInput | Entrée d'ouverture d'une issue vers un projet frère           | 115   |
// | ListIssuesInput  | Critères de lecture des issues visibles                       | 129   |
// | AnswerInput      | Message à ajouter au fil, et clôture éventuelle               | 143   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRAT UNIQUEMENT — les implémentations sont dans create_issue.go, list_issues.go,
// get_issue.go et answer_issue.go.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-ia/internal/feature/issue/store"
	"github.com/google/uuid"
)

// Erreurs domaine, traduites en codes HTTP par le handler via errors.Is.
//
// Il n'existe volontairement PAS d'erreur « interdit » sur une clé d'issue : une issue hors de
// portée est introuvable. Distinguer les deux permettrait d'énumérer le backlog d'un repo frère
// en essayant des numéros.
var (
	ErrInvalidInput = errors.New("issue: invalid input")
	ErrNotFound     = errors.New("issue: not found")
	ErrConflict     = errors.New("issue: conflict")
)

// Service porte les questions inter-projets : ce qu'un repo demande à un repo frère.
//
// TeamID et ProjectID viennent du Principal du token, jamais du corps de la requête. C'est ce
// qui rend impossible d'agir au nom d'un autre projet.
type Service interface {
	// CreateIssue ouvre une question vers un projet frère, désigné par sa clé.
	CreateIssue(ctx context.Context, in CreateIssueInput) (Issue, error)

	ListIssues(ctx context.Context, in ListIssuesInput) ([]Issue, error)
	GetIssue(ctx context.Context, ref Ref) (IssueDetail, error)

	// Answer ajoute un message au fil et, si demandé, clôt l'issue. L'état résultant n'est pas
	// choisi par l'appelant : il est déduit de son rôle dans la conversation.
	Answer(ctx context.Context, in AnswerInput) (Issue, error)
}

// service dépend de l'interface store, jamais de sqlc.
type service struct {
	store store.Store
}

// New crée le service issue.
func New(st store.Store) Service {
	return &service{store: st}
}

// Issue est la vue API. Ref est la clé lisible complète (CORE-34), composée ici et nulle part
// ailleurs. Elle porte toujours la clé du DESTINATAIRE, qui possède l'issue et son numéro.
//
// Role et Peer sont relatifs à l'appelant : « qui suis-je dans cette conversation, et qui est en
// face ». Un agent n'a pas à recomposer cette information.
type Issue struct {
	Ref       string     `json:"ref"`
	Title     string     `json:"title"`
	State     string     `json:"state"`
	Role      string     `json:"role"`
	Peer      string     `json:"peer"`
	UpdatedAt time.Time  `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

// Message est une entrée du fil. L'auteur est un PROJET : c'est un dialogue entre deux repos.
type Message struct {
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// IssueDetail est une issue et son fil, du plus ancien au plus récent.
//
// MessagesTotal existe pour que l'agent sache qu'il ne lit qu'une fenêtre : un fil de cent
// messages ne doit pas entrer d'un bloc dans son contexte.
type IssueDetail struct {
	Issue
	Messages      []Message `json:"messages"`
	MessagesTotal int       `json:"messages_total"`
}

// Ref désigne CORE-34 pour l'appelant.
//
// ProjectKey est celle lue dans la référence, donc contrôlée par l'appelant ; TeamID et
// CallerProjectID viennent du token. La visibilité se décide sur ces deux derniers.
type Ref struct {
	TeamID          uuid.UUID `json:"-"`
	CallerProjectID uuid.UUID `json:"-"`
	ProjectKey      string    `json:"-"`
	Number          int64     `json:"-"`
}

// CreateIssueInput porte l'ouverture d'une issue. Le destinataire est une CLÉ de projet : un
// agent ne manipule pas d'UUID, donc il ne peut pas en injecter un.
type CreateIssueInput struct {
	TeamID          uuid.UUID `json:"-"`
	AuthorProjectID uuid.UUID `json:"-"`

	ToProject string `json:"to_project"`
	Title     string `json:"title"`
	Body      string `json:"body"`
}

// ListIssuesInput décrit une lecture.
//
// Role vaut "", "incoming" ou "outgoing" ; il restreint ce qui est déjà visible, il n'ouvre
// jamais l'accès. Les issues closes sont exclues par défaut : ce qui est clos n'appelle plus
// d'action, et le contexte d'un agent est une ressource rare.
type ListIssuesInput struct {
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`

	Role          string `json:"role"`
	State         string `json:"state"`
	IncludeClosed bool   `json:"include_closed"`
	Limit         int    `json:"limit"`
}

// AnswerInput porte un message à ajouter au fil, et la clôture éventuelle.
//
// Le corps est obligatoire même pour clore : une clôture sans motif ne dit rien au correspondant,
// qui découvrirait sa question fermée sans savoir pourquoi.
type AnswerInput struct {
	Ref Ref `json:"-"`

	Body  string `json:"body"`
	Close bool   `json:"close"`
}
