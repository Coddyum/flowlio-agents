package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | Scope              | Portée d'un token : administration ou projet unique          | 37    |
// | Principal          | Identité authentifiée portée par une requête                 | 49    |
// | Principal.IsAdmin  | Vrai si le principal peut administrer la team                | 57    |
// | Service            | Contrat d'authentification exposé via CoreServices           | 62    |
// | service            | Implémentation, dépendante de l'interface Store              | 72    |
// | New                | Crée le service d'authentification                           | 80    |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRAT UNIQUEMENT — l'implémentation est dans authenticate.go et middleware.go.

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ErrUnauthenticated couvre TOUS les échecs d'authentification : préfixe inconnu, secret faux,
// token révoqué ou malformé. Un appelant ne doit jamais pouvoir distinguer ces cas — ce serait
// un oracle permettant d'énumérer les tokens valides.
var ErrUnauthenticated = errors.New("auth: unauthenticated")

// ErrForbidden signale un principal authentifié dont la portée ne couvre pas la ressource.
var ErrForbidden = errors.New("auth: forbidden")

// Scope décrit ce qu'un token a le droit de faire.
type Scope string

const (
	// ScopeAdmin administre les teams et les projets. Émis au bootstrap en mode local.
	ScopeAdmin Scope = "admin"
	// ScopeProject est le token d'un agent : un seul projet, dans une seule team.
	ScopeProject Scope = "project"
)

// Principal est l'identité authentifiée d'une requête. TeamID et ProjectID sont vides pour un
// token admin ; ils sont toujours renseignés pour un token de projet, et c'est cette paire qui
// scope chaque query du store.
type Principal struct {
	TokenID   uuid.UUID
	Scope     Scope
	TeamID    uuid.UUID
	ProjectID uuid.UUID
}

// IsAdmin indique si le principal peut créer des teams, des projets et des tokens.
func (p Principal) IsAdmin() bool {
	return p.Scope == ScopeAdmin
}

// Service authentifie les requêtes. Exposé à tous les modules via CoreServices.Auth().
type Service interface {
	// Authenticate résout un token présenté en Principal, ou renvoie ErrUnauthenticated.
	Authenticate(ctx context.Context, rawToken string) (Principal, error)
	// Middleware exige un token valide et dépose le Principal dans le contexte de la requête.
	Middleware(next http.Handler) http.Handler
	// AdminOnly exige en plus un token de portée admin.
	AdminOnly(next http.Handler) http.Handler
}

// service dépend de l'interface Store, jamais de sqlc.
type service struct {
	store Store
	// touchInterval limite la fréquence d'écriture de last_used_at : sans ça, chaque requête
	// authentifiée déclencherait un UPDATE.
	touchInterval time.Duration
}

// New crée le service d'authentification.
func New(store Store) Service {
	return &service{store: store, touchInterval: time.Minute}
}
