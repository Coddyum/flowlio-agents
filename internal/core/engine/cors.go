package engine

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément  | Résumé                                                             | Ligne |
// |----------|--------------------------------------------------------------------|-------|
// | CORS     | Middleware qui autorise une liste FERMÉE d'origines de navigateur    | 74    |
// | allows   | Dit si une origine est dans la liste, par égalité stricte            | 117   |
//
// Fin du sommaire.
// =====================================================================
//
// POURQUOI CE FICHIER EXISTE. La page de pont est servie par flowlio.me et appelle l'API sur
// `http://localhost:42058`, dans le navigateur de l'utilisateur. C'est un appel cross-origin :
// sans en-tête CORS, le navigateur refuse de rendre la réponse au JavaScript qui l'a demandée.
// Rien d'autre dans le produit n'en a besoin — la CLI et le serveur MCP parlent en HTTP direct,
// sans navigateur, et ne présentent aucun `Origin`.
//
// CE QUE CORS PROTÈGE ICI, ET CE QU'IL NE PROTÈGE PAS. Il ne remplace pas l'authentification :
// le token vit dans le localStorage du navigateur et part en `Authorization`, donc un site tiers
// ne peut pas l'emprunter — il n'a pas accès au localStorage d'une autre origine, et aucune
// requête n'est authentifiée par un cookie. CORS ferme l'autre porte : celle du site qui, ouvert
// dans un onglet voisin, ferait parler VOTRE navigateur à VOTRE API locale et lirait la réponse.
//
// TROIS RÈGLES QUI NE SE NÉGOCIENT PAS :
//
//  1. **Jamais `*`.** Cette API répond à un token d'administration qui vit sur la machine de
//     l'utilisateur. Une origine autorisée est une origine ÉCRITE, jamais une origine devinée.
//  2. **Égalité stricte sur l'origine.** Pas de préfixe, pas de suffixe, pas de sous-domaine
//     implicite : `https://flowlio.me.evil.com` et `https://evilflowlio.me` passent n'importe
//     quel test de sous-chaîne, et c'est le contournement le plus banal du web.
//  3. **Aucun `Access-Control-Allow-Credentials`.** Il n'y a pas de cookie dans ce produit. Le
//     poser autoriserait un jour un cookie à voyager sans que personne ne se rappelle pourquoi.
//
// `Vary: Origin` est posé dès qu'une requête porte une origine, autorisée ou non. Sans lui, un
// cache intermédiaire peut servir à une origine la réponse — en-têtes compris — calculée pour une
// autre, ce qui transforme une liste fermée en passoire sans qu'aucun code ne change.

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// preflightMaxAge borne la durée pendant laquelle le navigateur peut réutiliser la réponse au
// preflight. Dix minutes : assez pour ne pas doubler chaque appel de l'écran, assez peu pour
// qu'un changement de liste d'origines prenne effet sans vider un cache à la main.
const preflightMaxAge = 10 * time.Minute

// allowedMethods et allowedHeaders décrivent ce que la surface accepte réellement, et rien de
// plus. `Authorization` est le seul en-tête que la page de pont ajoute ; `Content-Type` sert aux
// écritures des autres modules.
const (
	allowedMethods = "GET, POST, PATCH, DELETE"
	allowedHeaders = "Authorization, Content-Type"
)

// CORS autorise une liste FERMÉE d'origines de navigateur à appeler l'API.
//
// Une requête sans en-tête `Origin` traverse sans être touchée : c'est le cas de la CLI, du
// serveur MCP et de tout appel qui ne vient pas d'un navigateur. Y répondre des en-têtes CORS
// n'aurait aucun effet utile et brouillerait la lecture des logs.
//
// Une origine inconnue reçoit une réponse SANS en-tête d'autorisation. Le navigateur refusera
// alors de rendre le corps au JavaScript appelant, ce qui est exactement le comportement voulu :
// le refus appartient au navigateur, et le serveur n'a pas à inventer un code d'erreur pour une
// requête qui, elle, est parfaitement légitime — `curl` la fait tous les jours.
//
// Le preflight, lui, est tranché ICI : il ne sert qu'au navigateur, donc une origine inconnue le
// reçoit en 403, et c'est journalisé. C'est le seul endroit où un refus d'origine est visible
// côté serveur, et c'est ce qui rend une mauvaise configuration diagnosticable.
func CORS(allowed []string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Add("Vary", "Origin")
			ok := allows(allowed, origin)
			if ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}

			if r.Method != http.MethodOptions || r.Header.Get("Access-Control-Request-Method") == "" {
				next.ServeHTTP(w, r)
				return
			}

			// À partir d'ici, c'est un preflight : il n'atteint jamais le handler, et n'a donc
			// jamais besoin d'être authentifié — le navigateur ne joint pas l'`Authorization` à
			// un preflight, par construction.
			if !ok {
				log.Printf("engine: preflight refusé pour l'origine %q sur %s", origin, r.URL.Path)
				w.WriteHeader(http.StatusForbidden)
				return
			}

			w.Header().Set("Access-Control-Allow-Methods", allowedMethods)
			w.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
			w.Header().Set("Access-Control-Max-Age", strconv.Itoa(int(preflightMaxAge.Seconds())))
			w.WriteHeader(http.StatusNoContent)
		})
	}
}

// allows dit si une origine figure dans la liste, par ÉGALITÉ STRICTE.
//
// La comparaison est sensible à la casse sur le chemin mais pas sur le schéma ni l'hôte, que la
// spécification veut en minuscules : les navigateurs les émettent déjà ainsi, et normaliser à la
// réception évite qu'une entrée de configuration écrite `HTTPS://Flowlio.me` ne fonctionne jamais
// sans que personne ne comprenne pourquoi.
func allows(allowed []string, origin string) bool {
	origin = strings.ToLower(origin)
	for _, a := range allowed {
		if strings.ToLower(strings.TrimSpace(a)) == origin {
			return true
		}
	}
	return false
}
