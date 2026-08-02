package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | contextKey         | Type privé de clé de contexte, non collisionnable            | 24    |
// | FromContext        | Récupère le Principal déposé par le middleware               | 30    |
// | service.Middleware | Exige un token valide et dépose le Principal dans le contexte| 37    |
// | service.AdminOnly  | Exige en plus une portée admin                               | 57    |
// | bearerToken        | Extrait le token de l'en-tête Authorization                  | 70    |
// | deny               | Répond une erreur d'auth sans divulguer la cause             | 81    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"net/http"
	"strings"
)

// contextKey est privé au package : aucun autre paquet ne peut écrire ou écraser le Principal.
type contextKey struct{}

var principalKey contextKey

// FromContext renvoie le Principal déposé par le middleware. Le second retour est faux si la
// requête n'est pas passée par Middleware — auquel cas le handler ne doit rien servir.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}

// Middleware exige un token valide. Il est lié une seule fois, dans le module.go de chaque
// feature, jamais à l'intérieur d'un handler.
func (s *service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := bearerToken(r)
		if !ok {
			deny(w, http.StatusUnauthorized)
			return
		}

		principal, err := s.Authenticate(r.Context(), raw)
		if err != nil {
			deny(w, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), principalKey, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AdminOnly enveloppe Middleware et refuse tout principal non administrateur.
func (s *service) AdminOnly(next http.Handler) http.Handler {
	return s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := FromContext(r.Context())
		if !ok || !principal.IsAdmin() {
			deny(w, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// bearerToken extrait le token de `Authorization: Bearer <token>`. Un seul emplacement accepté :
// un token en query string finirait dans les logs d'accès et les historiques de shell.
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(header[len(prefix):]), true
}

// deny répond une erreur générique : le corps ne dit jamais pourquoi l'authentification a
// échoué, et ne renvoie évidemment jamais le token présenté.
func deny(w http.ResponseWriter, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"error":"` + http.StatusText(code) + `"}`))
}
