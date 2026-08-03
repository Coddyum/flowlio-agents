package engine

// Ce que ce fichier verrouille : la liste d'origines est FERMÉE, et elle se compare par égalité
// stricte.
//
// Le vrai risque n'est pas d'oublier CORS — la page de pont ne marcherait pas, on le verrait en
// trente secondes. Il est d'écrire un test de sous-chaîne, ou un `*` « le temps de déboguer »,
// et de laisser n'importe quel site ouvert dans un onglet voisin parler à l'API locale de
// l'utilisateur avec son token d'administration.

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// origines est la liste autorisée des tests. Écrite ici et pas tirée de la config : ce fichier
// teste le middleware, pas les valeurs par défaut du produit.
var origines = []string{"https://flowlio.me", "https://www.flowlio.me"}

// serveCORS joue une requête à travers le middleware et dit si le handler en aval a été atteint.
func serveCORS(t *testing.T, req *http.Request, allowed []string) (*httptest.ResponseRecorder, bool) {
	t.Helper()

	atteint := false
	h := CORS(allowed)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atteint = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, atteint
}

// preflight fabrique la requête qu'un navigateur envoie AVANT un appel portant un en-tête
// `Authorization` — c'est-à-dire avant chaque appel de la page de pont.
func preflight(origin string) *http.Request {
	req := httptest.NewRequest(http.MethodOptions, "/api/overview/", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "authorization")
	return req
}

// Sans `Origin`, la requête n'est pas touchée. C'est le cas de la CLI et du serveur MCP, qui
// représentent la quasi-totalité du trafic de ce produit.
func TestCORSPassesThroughWithoutOrigin(t *testing.T) {
	rec, atteint := serveCORS(t, httptest.NewRequest(http.MethodGet, "/api/task/", nil), origines)

	if !atteint {
		t.Fatal("le handler n'a pas été atteint — CORS bloque un appel qui ne vient pas d'un navigateur")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q sur une requête sans Origin", got)
	}
}

// Une origine listée est renvoyée telle quelle, et `Vary: Origin` est posé.
//
// MUTATION : retirer le `Vary` → ce test rouge. Sans lui, un cache intermédiaire sert à une
// origine les en-têtes calculés pour une autre, et la liste fermée ne ferme plus rien.
func TestCORSAllowsListedOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/overview/", nil)
	req.Header.Set("Origin", "https://flowlio.me")

	rec, atteint := serveCORS(t, req, origines)

	if !atteint {
		t.Fatal("le handler n'a pas été atteint pour une origine autorisée")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://flowlio.me" {
		t.Errorf("Access-Control-Allow-Origin = %q, attendu l'origine appelante", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, attendu Origin", got)
	}
}

// Une origine inconnue n'obtient PAS d'autorisation — mais sa requête est servie : le refus
// appartient au navigateur, qui refusera de rendre le corps au JavaScript appelant. Le serveur
// n'a pas à inventer un code d'erreur pour une requête que `curl` fait légitimement tous les
// jours.
func TestCORSIgnoresUnknownOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/overview/", nil)
	req.Header.Set("Origin", "https://exemple.test")

	rec, atteint := serveCORS(t, req, origines)

	if !atteint {
		t.Fatal("le handler n'a pas été atteint : le refus doit venir du navigateur, pas du serveur")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q pour une origine inconnue", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, attendu Origin même sur un refus", got)
	}
}

// L'ÉGALITÉ EST STRICTE. Chacune de ces origines passe un test de sous-chaîne, et aucune ne doit
// être autorisée.
//
// MUTATION JOUÉE : remplacer l'égalité par `strings.Contains` → cinq de ces lignes rouges. C'est
// le contournement le plus banal du web, et le principal que ce fichier existe pour interdire.
//
// UNE MUTATION VOISINE SURVIT, ET IL VAUT MIEUX DIRE POURQUOI. `strings.HasSuffix(origine,
// autorisée)` laisse la suite verte, parce que la chaîne comparée porte le SCHÉMA :
// `https://evil-flowlio.me` ne se termine pas par `https://flowlio.me`. Le suffixe n'est un
// trou que si l'on compare les hôtes seuls — la faute classique est `HasSuffix(hôte,
// "flowlio.me")`, une forme que ce code n'a jamais eue puisqu'il ne découpe pas l'origine.
// La dernière ligne du tableau la tue tout de même : aucun navigateur n'émet cette origine, mais
// elle garde la comparaison une ÉGALITÉ, ce qui est la propriété annoncée.
func TestCORSNeverMatchesLookalikeOrigin(t *testing.T) {
	sosies := []string{
		"https://flowlio.me.evil.test",   // suffixe ajouté
		"https://evil-flowlio.me",        // préfixe ajouté
		"http://flowlio.me",              // schéma différent
		"https://flowlio.me:8080",        // port ajouté
		"https://sous.flowlio.me",        // sous-domaine non listé
		"null",                           // origine opaque d'une iframe sandboxée
		"https://flowlio.me/../evil",     // chemin, qu'une origine ne porte jamais
		"https://www.flowlio.me.evil.co", // le second listé, sosié pareil
		"xhttps://flowlio.me",            // se TERMINE par l'origine autorisée
	}

	for _, sosie := range sosies {
		t.Run(sosie, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/overview/", nil)
			req.Header.Set("Origin", sosie)

			rec, _ := serveCORS(t, req, origines)

			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("origine %q autorisée (%q) — la comparaison n'est plus une égalité", sosie, got)
			}
		})
	}
}

// Le preflight est tranché par le middleware et n'atteint JAMAIS le handler.
//
// C'est nécessaire, pas optimisant : un navigateur ne joint pas l'`Authorization` à un preflight,
// donc le middleware d'auth le refuserait en 401, et l'appel réel n'aurait jamais lieu.
//
// MUTATION : laisser le preflight descendre vers `next` → ce test rouge.
func TestCORSPreflightIsAnsweredWithoutAuth(t *testing.T) {
	rec, atteint := serveCORS(t, preflight("https://flowlio.me"), origines)

	if atteint {
		t.Error("le preflight a atteint le handler — il sera refusé par le middleware d'auth, " +
			"qui n'a aucun token à lire dans un preflight")
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("code = %d, attendu %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != allowedHeaders {
		t.Errorf("Access-Control-Allow-Headers = %q, attendu %q", got, allowedHeaders)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != allowedMethods {
		t.Errorf("Access-Control-Allow-Methods = %q, attendu %q", got, allowedMethods)
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Errorf("Access-Control-Max-Age = %q, attendu 600", got)
	}
}

// PRIVATE NETWORK ACCESS — la permission qui décide si Chrome laisse passer le pont.
//
// Une page servie par flowlio.me qui appelle `http://localhost` sort du réseau public vers le
// réseau privé de la machine. Chrome traite ce saut à part : il demande la permission dans le
// preflight, et l'exige en réponse. Sans elle, l'appel échoue alors que tous les en-têtes CORS
// ordinaires sont corrects — un mode d'échec indébogable depuis l'extérieur, et qui ne touche
// qu'un navigateur sur trois.
//
// MUTATION : retirer l'en-tête de réponse → ce test rouge, et le pont ne marche plus sous Chrome.
func TestCORSGrantsPrivateNetworkAccessToAllowedOrigin(t *testing.T) {
	req := preflight("https://flowlio.me")
	req.Header.Set("Access-Control-Request-Private-Network", "true")

	rec, _ := serveCORS(t, req, origines)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, attendu %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Private-Network"); got != "true" {
		t.Errorf("Access-Control-Allow-Private-Network = %q, attendu \"true\" — Chrome refusera "+
			"l'appel vers localhost alors que tous les autres en-têtes sont bons", got)
	}
}

// La permission de réseau privé n'est JAMAIS accordée à une origine inconnue, ni offerte à un
// preflight qui ne l'a pas demandée.
//
// Le premier cas est le seul qui compte pour la sécurité : accorder ce saut à n'importe qui
// reviendrait à laisser un site tiers atteindre les services locaux de la machine.
func TestCORSNeverGrantsPrivateNetworkAccessUnasked(t *testing.T) {
	inconnue := preflight("https://exemple.test")
	inconnue.Header.Set("Access-Control-Request-Private-Network", "true")

	rec, _ := serveCORS(t, inconnue, origines)
	if got := rec.Header().Get("Access-Control-Allow-Private-Network"); got != "" {
		t.Errorf("origine inconnue : permission de réseau privé accordée (%q)", got)
	}

	rec, _ = serveCORS(t, preflight("https://flowlio.me"), origines)
	if got := rec.Header().Get("Access-Control-Allow-Private-Network"); got != "" {
		t.Errorf("permission accordée sans être demandée (%q) — un en-tête qu'on ne comprend "+
			"pas est un en-tête qu'on n'émet pas", got)
	}
}

// Un preflight d'une origine inconnue est refusé côté SERVEUR, en 403.
//
// C'est la seule asymétrie du fichier, et elle est voulue : un preflight ne sert qu'au
// navigateur, il n'a aucun usage légitime hors de lui. Le refuser explicitement est ce qui rend
// une liste d'origines mal configurée diagnosticable — sans ça, le seul symptôme est une erreur
// dans la console d'un navigateur, côté client.
func TestCORSPreflightFromUnknownOriginIsRefused(t *testing.T) {
	rec, atteint := serveCORS(t, preflight("https://exemple.test"), origines)

	if atteint {
		t.Error("le preflight d'une origine inconnue a atteint le handler")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("code = %d, attendu %d", rec.Code, http.StatusForbidden)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "" {
		t.Errorf("Access-Control-Allow-Methods = %q pour une origine inconnue", got)
	}
}

// NI `*`, NI CREDENTIALS. Deux en-têtes qu'il ne faut jamais voir sortir d'ici.
//
// `*` ouvrirait l'API à tout site ouvert dans le navigateur de l'utilisateur.
// `Allow-Credentials` autoriserait un cookie à voyager — ce produit n'en a aucun, et le jour où
// quelqu'un en ajoutera un, il ne doit pas hériter d'une permission écrite aujourd'hui.
func TestCORSNeverAllowsWildcardNorCredentials(t *testing.T) {
	for _, origin := range []string{"https://flowlio.me", "https://exemple.test"} {
		req := httptest.NewRequest(http.MethodGet, "/api/overview/", nil)
		req.Header.Set("Origin", origin)

		rec, _ := serveCORS(t, req, origines)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "*" {
			t.Errorf("origine %q : Access-Control-Allow-Origin = *", origin)
		}
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("origine %q : Access-Control-Allow-Credentials = %q", origin, got)
		}
	}
}

// Une liste VIDE ferme complètement la surface au navigateur. C'est la valeur qu'un utilisateur
// pose s'il ne veut aucun pont web, et elle doit être exprimable.
func TestCORSEmptyListRefusesEveryOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/overview/", nil)
	req.Header.Set("Origin", "https://flowlio.me")

	rec, _ := serveCORS(t, req, nil)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q avec une liste vide", got)
	}

	rec, _ = serveCORS(t, preflight("https://flowlio.me"), nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("preflight = %d avec une liste vide, attendu %d", rec.Code, http.StatusForbidden)
	}
}

// Un OPTIONS SANS `Access-Control-Request-Method` n'est pas un preflight : c'est une requête
// ordinaire, qui doit descendre. Sans cette distinction, le middleware avalerait un OPTIONS
// applicatif — le jour où il en existe un.
func TestCORSPlainOptionsIsNotAPreflight(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/overview/", nil)
	req.Header.Set("Origin", "https://flowlio.me")

	_, atteint := serveCORS(t, req, origines)

	if !atteint {
		t.Error("un OPTIONS sans Access-Control-Request-Method a été avalé par le middleware")
	}
}
