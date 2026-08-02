package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                            | Ligne |
// |-------------|-------------------------------------------------------------------|-------|
// | Issue       | Une issue telle que persistée, avec les clés de ses deux projets    | 46    |
// | Message     | Un message du fil, attribué au projet qui l'a écrit                 | 67    |
// | Ref         | Désigne CORE-34 pour un appelant donné, scope compris               | 77    |
// | NewIssue    | Données d'ouverture d'une issue vers un projet frère                | 86    |
// | IssueFilter | Critères de lecture des issues visibles par un projet               | 96    |
// | Answer      | Un message à ajouter, et la clôture éventuelle                      | 110   |
// | Event       | Une entrée du journal, écrite avec ce qui la produit                | 117   |
// | Store       | Contrat de persistance de la feature issue                          | 138   |
// | store       | Implémentation adossée aux queries générées par sqlc                | 160   |
// | New         | Crée le store issue                                                 | 168   |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRAT UNIQUEMENT — les implémentations sont dans issue.go, message.go, event.go et tx.go.

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-ia/internal/database"
	"github.com/google/uuid"
)

// ErrNotFound couvre indifféremment « n'existe pas » et « hors de portée » — les deux cas sont
// délibérément indiscernables. ErrConflict couvre les violations de contrainte ; ErrCorrupted
// signale une incohérence interne, qui n'est jamais la faute de l'appelant.
var (
	ErrNotFound  = errors.New("issue store: not found")
	ErrConflict  = errors.New("issue store: conflict")
	ErrCorrupted = errors.New("issue store: corrupted state")
)

// Issue est une question posée par un projet à un projet frère de la même team.
//
// ProjectKey est celle du DESTINATAIRE, qui possède l'issue et lui a donné son numéro :
// c'est donc elle qui compose la référence lisible.
type Issue struct {
	ID               uuid.UUID
	TeamID           uuid.UUID
	ProjectID        uuid.UUID
	AuthorProjectID  uuid.UUID
	Number           int64
	Title            string
	State            string
	ProjectKey       string
	AuthorProjectKey string
	// Incoming vaut vrai quand l'appelant est le DESTINATAIRE. Il est calculé par la query, qui
	// connaît déjà le projet appelant : le déduire plus haut demanderait de faire remonter des
	// UUID jusqu'à une couche qui n'en manipule pas.
	Incoming  bool
	CreatedAt time.Time
	UpdatedAt        time.Time
	ClosedAt         *time.Time
}

// Message est une entrée du fil. L'auteur est un PROJET, pas une personne : c'est un dialogue
// entre deux repos.
type Message struct {
	AuthorKey string
	Body      string
	CreatedAt time.Time
}

// Ref désigne une issue pour un appelant donné.
//
// CallerProjectID vient du token et n'est jamais un paramètre de requête : c'est lui qui décide
// de la visibilité. ProjectKey est celle du destinataire, telle que lue dans la référence.
type Ref struct {
	TeamID          uuid.UUID
	CallerProjectID uuid.UUID
	ProjectKey      string
	Number          int64
}

// NewIssue porte l'ouverture d'une issue. Le destinataire est désigné par sa CLÉ : un agent ne
// manipule pas d'UUID, donc il ne peut pas en injecter un.
type NewIssue struct {
	TeamID          uuid.UUID
	AuthorProjectID uuid.UUID
	ToProjectKey    string
	Title           string
	Body            string
}

// IssueFilter décrit une lecture. Role vaut "", "incoming" ou "outgoing" ; il restreint la
// clause de visibilité, il ne l'autorise jamais.
type IssueFilter struct {
	TeamID    uuid.UUID
	ProjectID uuid.UUID

	Role          string
	State         string
	IncludeClosed bool
	Limit         int32
}

// Answer porte un message à ajouter au fil et, éventuellement, la clôture.
//
// L'état résultant n'est PAS exprimable ici : il est calculé en base depuis le rôle de
// l'appelant. Un agent ne peut donc pas mentir sur l'état qu'il produit.
type Answer struct {
	Ref   Ref
	Body  string
	Close bool
}

// Event est une entrée du journal, écrite dans la même transaction que ce qui la produit.
type Event struct {
	TeamID         uuid.UUID
	ProjectID      uuid.UUID
	ActorProjectID uuid.UUID
	Kind           string
	SubjectID      uuid.UUID
}

// Genres d'événements émis par la feature. Le journal ne sert en v1 qu'au drapeau « nouveau »
// de l'inbox : l'état de référence reste issues.state.
const (
	KindIssueOpened   = "issue.opened"
	KindIssueAnswered = "issue.answered"
	KindIssueReopened = "issue.reopened"
	KindIssueClosed   = "issue.closed"
)

// Store est le contrat consommé par le service.
//
// Toute méthode porte son scope : team, et projet appelant. Il n'existe aucune lecture ni
// écriture d'issue qui prenne un identifiant nu.
type Store interface {
	// WithTx exécute fn dans une transaction. L'issue, son message et son événement s'écrivent
	// ensemble ou pas du tout : un événement perdu est une notification jamais reçue.
	WithTx(ctx context.Context, fn func(Store) error) error

	// CreateIssue résout le destinataire par sa clé, réserve son numéro et insère l'issue en une
	// seule instruction. Une clé inconnue, ou d'une autre team, ne consomme aucun numéro.
	CreateIssue(ctx context.Context, in NewIssue) (Issue, error)
	IssueByRef(ctx context.Context, ref Ref) (Issue, error)
	ListIssues(ctx context.Context, filter IssueFilter) ([]Issue, error)

	// Answer ajoute le message ET applique la transition d'état en une seule instruction.
	// Séparer les deux laisserait un message atterrir dans une issue fermée entre-temps.
	Answer(ctx context.Context, in Answer) (Issue, error)

	AddFirstMessage(ctx context.Context, issueID, authorProjectID uuid.UUID, body string) error
	ListMessages(ctx context.Context, ref Ref, issueID uuid.UUID) ([]Message, error)

	AppendEvent(ctx context.Context, event Event) error
}

// store adosse le contrat aux queries générées. db ne sert qu'à ouvrir une transaction.
type store struct {
	q  *database.Queries
	db *sql.DB
	// inTx marque un store qui porte déjà une transaction, pour que WithTx refuse l'imbrication.
	inTx bool
}

// New crée le store issue.
func New(q *database.Queries, db *sql.DB) Store {
	return &store{q: q, db: db}
}
