// Package authtest fournit le harnais qui permet à un test de route de présenter un token dont
// il choisit la portée.
//
// POURQUOI CE PAQUET EXISTE. `auth.contextKey` est privé : aucun paquet hors de
// `internal/core/auth` ne peut déposer un `Principal` dans un contexte de requête. C'est une bonne
// chose — un test de route DOIT exercer la vraie chaîne d'authentification, sinon il prouve la
// tenancy de son propre double. Mais chaque module qui teste une route doit donc fabriquer un
// `auth.Store` factice et frapper un vrai token, et c'est déjà arrivé deux fois dans ce dépôt.
//
// Un paquet `*test` est le seul moyen propre en Go d'exposer un double sans le faire entrer dans
// le binaire : rien ne l'importe hors des `_test.go`, et un garde-fou le vérifie
// (scripts/check-authtest-not-in-production.sh).
//
// CE QU'IL NE FAIT PAS, DÉLIBÉRÉMENT. Il ne fabrique aucun `Principal` directement et n'expose
// aucun raccourci pour en injecter un. Le seul chemin est le vrai middleware, sur un vrai token
// frappé par `crypto.NewToken()`. Un helper qui court-circuiterait l'authentification rendrait
// verts des tests qu'une régression d'auth devrait faire tomber.
package authtest

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément      | Résumé                                                             | Ligne |
// |--------------|--------------------------------------------------------------------|-------|
// | Store        | Faux auth.Store, qui vérifie le préfixe qu'on lui présente          | 51    |
// | Store.TokenByPrefix | Rend le token du test si le préfixe correspond               | 57    |
// | Store.TouchToken    | Enregistre l'usage, sans effet                               | 66    |
// | Token        | Un token frappé et le service d'auth qui le reconnaît               | 73    |
// | New          | Frappe un token de la portée demandée et monte le service d'auth    | 91    |
// | Admin        | Token admin, éventuellement épinglé à une team                      | 118   |
// | Project      | Token d'agent, scopé à une team et un projet                        | 124   |
// | Token.Authorize | Pose l'en-tête Authorization sur une requête                     | 135   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/pkg/crypto"
	"github.com/google/uuid"
)

// Store est un auth.Store qui ne connaît qu'un seul token.
//
// Il VÉRIFIE le préfixe présenté, et c'est le point qui compte : un double qui rendrait son token
// quoi qu'on lui demande ferait passer pour correct un middleware qui n'extrait pas le préfixe.
type Store struct {
	prefix string
	record auth.TokenRecord
}

// TokenByPrefix rend le token du test, et seulement pour son préfixe.
func (s *Store) TokenByPrefix(_ context.Context, prefix string) (auth.TokenRecord, error) {
	if prefix != s.prefix {
		return auth.TokenRecord{}, auth.ErrTokenNotFound
	}
	return s.record, nil
}

// TouchToken enregistre l'usage du token. Sans effet ici : la fraîcheur de `last_used_at` n'est
// pas ce que les tests de route établissent.
func (s *Store) TouchToken(_ context.Context, _ uuid.UUID) error { return nil }

// Token porte un token frappé, le service d'auth qui le reconnaît, et le principal qu'il
// résoudra.
//
// Plain est le secret en clair, à présenter dans l'en-tête Authorization. Il n'existe que dans le
// test : le Store n'en garde que le hash, exactement comme la base.
type Token struct {
	Plain string
	Auth  auth.Service

	// Record est le token tel que le store le rendra. Un test peut le lire pour asserter sur
	// l'identité attendue, jamais pour la contourner.
	Record auth.TokenRecord
}

// New frappe un token dont le test choisit la portée, et monte le service d'auth qui le reconnaît.
//
// Le secret et son hash sont fabriqués ICI, par le vrai crypto.NewToken : un test qui les
// fournirait prouverait la cohérence de ses propres constantes, pas celle de l'authentification.
//
// Les champs de record que l'appelant n'a pas renseignés sont complétés — un identifiant si absent,
// et l'horodatage d'usage. Tout le reste est ce que le test a demandé, sans correction : une
// portée incohérente doit être PRÉSENTABLE, parce que c'est exactement ce que les tests de
// confinement existent pour refuser.
func New(t *testing.T, record auth.TokenRecord) Token {
	t.Helper()

	tok, err := crypto.NewToken()
	if err != nil {
		t.Fatalf("authtest: frappe du token: %v", err)
	}

	if record.ID == uuid.Nil {
		record.ID = uuid.New()
	}
	record.SecretHash = tok.Hash
	record.LastUsedAt = time.Now()

	return Token{
		Plain:  tok.Plain,
		Auth:   auth.New(&Store{prefix: tok.Prefix, record: record}),
		Record: record,
	}
}

// Admin frappe un token de portée admin.
//
// teamID est la team que le token PORTE : uuid.Nil pour l'admin global, celui que l'amorçage crée
// réellement. Un admin porteur d'une team n'est plus insérable en base depuis la migration 000006,
// et c'est justement pour ça qu'un test doit pouvoir en présenter un : la défense du code ne doit
// pas reposer sur une contrainte écrite dans un autre fichier.
func Admin(t *testing.T, teamID uuid.UUID) Token {
	t.Helper()
	return New(t, auth.TokenRecord{Scope: auth.ScopeAdmin, TeamID: teamID})
}

// Project frappe un token d'agent, scopé à une team et un projet.
func Project(t *testing.T, teamID, projectID uuid.UUID) Token {
	t.Helper()
	return New(t, auth.TokenRecord{
		Scope:     auth.ScopeProject,
		TeamID:    teamID,
		ProjectID: projectID,
	})
}

// Authorize pose l'en-tête Authorization sur une requête et la rend, pour que l'appel tienne en
// une ligne au point d'usage.
func (tk Token) Authorize(req *http.Request) *http.Request {
	req.Header.Set("Authorization", "Bearer "+tk.Plain)
	return req
}
