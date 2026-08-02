package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | contextKey         | Type privé de clé de contexte, non collisionnable            | 25    |
// | FromContext        | Récupère le Principal déposé par le middleware               | 31    |
// | service.Middleware | Exige un token valide et dépose le Principal dans le contexte| 43    |
// | service.AdminOnly  | Exige en plus une portée admin                               | 109   |
// | bearerToken        | Extrait le token de l'en-tête Authorization                  | 122   |
// | deny               | Répond une erreur d'auth sans divulguer la cause             | 133   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"errors"
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
//
// C'est ici, et pas dans Authenticate, que le rate limiting s'applique : Authenticate ne voit
// pas la requête, donc pas l'IP source. Toute route authentifiée passe par Middleware — y
// compris via AdminOnly, qui l'enveloppe — donc une route ajoutée demain est protégée sans que
// son auteur ait à y penser.
func (s *service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := bearerToken(r)
		if !ok {
			// Aucun token présenté : ce n'est pas une tentative de deviner un token, donc ce
			// n'est pas compté. Sinon un client mal configuré consommerait le quota d'une IP
			// partagée avec des agents légitimes.
			deny(w, http.StatusUnauthorized)
			return
		}

		// L'empreinte identifie le token PRÉSENTÉ, préfixe et secret compris. Elle sert au
		// limiteur à reconnaître deux requêtes portant exactement le même token — pour ne les
		// compter qu'une fois, et pour exempter un token qui s'est déjà authentifié. Le token
		// brut ne quitte jamais cette fonction : voir trusted_tokens.go.
		fingerprint := tokenFingerprint(raw)

		// Le quota est consommé AVANT l'aller-retour store, pas après : compter le résultat
		// laissait passer toute une rafale concurrente pendant la latence de la base. Détail de
		// l'arbitrage, et de la restitution du quota plus bas : rate_limit.go.
		//
		// Quota dépassé : le CORPS, le CODE et les EN-TÊTES sont ceux d'un échec ordinaire. Pas
		// de 429, pas de Retry-After, pas d'en-tête de quota — un code distinct apprendrait à
		// l'attaquant que son balayage progresse.
		//
		// Ce que ce court-circuit ne masque PAS : la LATENCE. Le chemin bloqué calcule bien le
		// SHA-256 de l'empreinte, juste au-dessus, mais il ne fait aucun aller-retour Postgres :
		// il répond donc mesurablement plus vite qu'un échec normal, et un attaquant qui
		// chronomètre distingue les deux états. COMPROMIS ASSUMÉ : aligner les temps supposerait
		// d'aller quand même en base, c'est-à-dire d'offrir gratuitement la requête que le
		// limiteur existe précisément pour refuser. On préfère payer un oracle sur l'état
		// « limité » — qui ne dit rien sur la validité d'un token — plutôt que de rendre le
		// limiteur inopérant.
		reserved, allowed := s.limiter.reserve(clientIP(r), fingerprint)
		if !allowed {
			deny(w, http.StatusUnauthorized)
			return
		}

		principal, err := s.Authenticate(r.Context(), raw)
		if err != nil {
			// La charge reste due dans les deux cas. Elle n'est PAS rendue sur une panne du
			// store : l'attaquant provoque lui-même cette issue en abandonnant sa requête, et
			// s'en servait pour faire rembourser la charge de sa requête jumelle — le quota ne
			// montait alors jamais. Les deux issues restent distinguées pour la confiance : un
			// refus avéré retire la confiance d'un token, une panne ne prouve rien.
			outcome := outcomeRejected
			if !errors.Is(err, ErrUnauthenticated) {
				outcome = outcomeUnavailable
			}
			s.limiter.release(reserved, outcome)
			deny(w, http.StatusUnauthorized)
			return
		}

		// Succès : la seule issue qui rend la charge, et la seule qui rend le token de confiance
		// — il ne consommera plus de quota tant qu'il reste valide. Sans quoi un agent légitime
		// se bloquerait avec ses propres requêtes, ou se ferait bloquer par un voisin bruyant.
		s.limiter.release(reserved, outcomeAuthenticated)

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
