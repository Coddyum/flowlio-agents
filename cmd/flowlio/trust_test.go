package main

// Ce que ce fichier verrouille : ce que l'HUMAIN LIT.
//
// Les trois commandes `trust` n'ont aucune logique métier — elles composent un chemin, décodent
// une réponse et impriment. La seule chose qui puisse y être fausse est donc le texte, et c'est
// exactement ce que personne ne teste d'habitude. Or ce texte est la seule surface où la vérité du
// graphe est lisible, et la première chose que lit quelqu'un dont l'agent vient de recevoir
// `not found`.
//
// Le serveur est un httptest : les chemins et les corps réellement émis sont vérifiés, pas
// supposés.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

// capture détourne os.Stdout le temps de fn et rend ce qui y a été écrit.
//
// Les commandes impriment avec fmt.Printf plutôt que sur un io.Writer injecté, comme les six
// autres commandes du binaire. Détourner le descripteur garde ce test aligné sur la forme du
// reste de la CLI au lieu d'imposer une injection à une seule famille de commandes.
func capture(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	saved := os.Stdout
	os.Stdout = w
	// defer, et pas une restauration en ligne : si fn panique, os.Stdout resterait branché sur un
	// tube que personne ne lit et TOUTE la suite du paquet perdrait sa sortie — un échec ailleurs
	// deviendrait alors indébogable.
	defer func() { os.Stdout = saved }()

	runErr := fn()

	os.Stdout = saved
	if err := w.Close(); err != nil {
		t.Fatalf("fermeture du tube: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("lecture du tube: %v", err)
	}
	return string(out), runErr
}

// trustAPI monte une API de test. Il enregistre chaque requête reçue, ce qui permet d'asserter le
// CHEMIN réellement émis — la partie qu'une relecture ne voit pas.
type trustAPI struct {
	edges    []service.TrustEdge
	projects []service.Project
	decision service.TrustDecision
	status   int

	seen []string
}

func (a *trustAPI) serve(t *testing.T) *client.Client {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.seen = append(a.seen, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")

		if a.status != 0 && a.status >= http.StatusBadRequest {
			w.WriteHeader(a.status)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
			return
		}

		switch {
		case strings.HasPrefix(r.URL.Path, "/api/workspace/projects"):
			_ = json.NewEncoder(w).Encode(a.projects)
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(a.edges)
		default:
			_ = json.NewEncoder(w).Encode(a.decision)
		}
	}))
	t.Cleanup(srv.Close)

	return client.New(srv.URL, "flw_test_secret")
}

func projets(keys ...string) []service.Project {
	out := make([]service.Project, 0, len(keys))
	for _, k := range keys {
		out = append(out, service.Project{Key: k, Name: "Projet " + k})
	}
	return out
}

// Un graphe VIDE est le cas qui compte : c'est l'état par défaut de toute team après la migration,
// donc celui dans lequel un humain arrive après qu'un agent lui a rendu `not found`.
//
// La sortie doit nommer les projets ET donner la commande exacte à taper. Ne rien afficher serait
// techniquement correct et pratiquement inutilisable.
func TestTrustListSurUnGrapheVideDitQuoiTaper(t *testing.T) {
	api := &trustAPI{projects: projets("CORE", "FRNT", "OPS")}

	out, err := capture(t, func() error {
		return trustList(context.Background(), api.serve(t), "acme")
	})
	if err != nil {
		t.Fatalf("trustList: %v", err)
	}

	for _, attendu := range []string{
		"aucune confiance déclarée",
		"CORE, FRNT, OPS",
		"flowlio trust allow CORE FRNT --team acme",
	} {
		if !strings.Contains(out, attendu) {
			t.Errorf("la sortie ne contient pas %q :\n%s", attendu, out)
		}
	}
}

// Une team d'UN SEUL projet n'a aucune paire possible : lui proposer d'en ouvrir une serait
// proposer une commande qui ne peut pas exister.
func TestTrustListNeProposeRienSansPairePossible(t *testing.T) {
	api := &trustAPI{projects: projets("CORE")}

	out, err := capture(t, func() error {
		return trustList(context.Background(), api.serve(t), "acme")
	})
	if err != nil {
		t.Fatalf("trustList: %v", err)
	}

	if strings.Contains(out, "trust allow") {
		t.Errorf("une commande est proposée alors qu'aucune paire n'est possible :\n%s", out)
	}
	if !strings.Contains(out, "moins de deux projets") {
		t.Errorf("la sortie n'explique pas pourquoi il n'y a rien à faire :\n%s", out)
	}
}

// Le compte « X sur Y » est ce qui montre d'un coup d'œil qu'il reste quelque chose à ouvrir.
// Trois projets font trois paires possibles ; deux déclarées en laissent une.
func TestTrustListCompteLesPairesPossibles(t *testing.T) {
	jour := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	api := &trustAPI{
		projects: projets("CORE", "FRNT", "OPS"),
		edges: []service.TrustEdge{
			{First: "CORE", Second: "FRNT", CreatedAt: jour},
			{First: "CORE", Second: "OPS", CreatedAt: jour},
		},
	}

	out, err := capture(t, func() error {
		return trustList(context.Background(), api.serve(t), "acme")
	})
	if err != nil {
		t.Fatalf("trustList: %v", err)
	}

	for _, attendu := range []string{"CORE ↔ FRNT", "CORE ↔ OPS", "2026-08-04", "2 paire(s) sur 3 possible(s)"} {
		if !strings.Contains(out, attendu) {
			t.Errorf("la sortie ne contient pas %q :\n%s", attendu, out)
		}
	}
}

// allow et deny disent ce qui a CHANGÉ. Un rejeu n'est pas une erreur, mais laisser croire à
// l'humain qu'il vient de changer quelque chose en serait une.
func TestTrustAnnonceCeQuiAChange(t *testing.T) {
	cas := []struct {
		name     string
		changed  bool
		run      func(*client.Client) error
		attendu  string
		interdit string
	}{
		{
			"allow, première fois", true,
			func(c *client.Client) error { return trustAllow(context.Background(), c, "acme", "CORE", "FRNT") },
			"peuvent désormais s'adresser des issues", "déjà autorisés",
		},
		{
			"allow, rejeu", false,
			func(c *client.Client) error { return trustAllow(context.Background(), c, "acme", "CORE", "FRNT") },
			"déjà autorisés, rien à faire", "désormais",
		},
		{
			"deny, première fois", true,
			func(c *client.Client) error { return trustDeny(context.Background(), c, "acme", "CORE", "FRNT") },
			"confiance retirée", "rien à retirer",
		},
		{
			"deny, rejeu", false,
			func(c *client.Client) error { return trustDeny(context.Background(), c, "acme", "CORE", "FRNT") },
			"aucune confiance déclarée, rien à retirer", "confiance retirée",
		},
	}

	for _, c := range cas {
		t.Run(c.name, func(t *testing.T) {
			api := &trustAPI{decision: service.TrustDecision{First: "CORE", Second: "FRNT", Changed: c.changed}}

			out, err := capture(t, func() error { return c.run(api.serve(t)) })
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if !strings.Contains(out, c.attendu) {
				t.Errorf("la sortie ne contient pas %q :\n%s", c.attendu, out)
			}
			if strings.Contains(out, c.interdit) {
				t.Errorf("la sortie contient %q, qui est le message de l'autre cas :\n%s", c.interdit, out)
			}
		})
	}
}

// LES TROIS LIGNES LES PLUS IMPORTANTES DE LA COMMANDE.
//
// `trust deny` n'est pas un outil de confinement : les fils déjà ouverts restent répondables, sans
// borne de temps. Sans ces lignes, quelqu'un qui vient de découvrir un repo compromis croirait
// l'avoir coupé. Le message nomme donc explicitement l'outil qui coupe vraiment.
//
// MUTATION : retirer l'un des deux Println de trustDeny fait tomber ce test.
func TestTrustDenyNommeLeVraiCoupeCircuit(t *testing.T) {
	api := &trustAPI{decision: service.TrustDecision{First: "CORE", Second: "OPS", Changed: true}}

	out, err := capture(t, func() error {
		return trustDeny(context.Background(), api.serve(t), "acme", "CORE", "OPS")
	})
	if err != nil {
		t.Fatalf("trustDeny: %v", err)
	}

	for _, attendu := range []string{
		"Les fils déjà ouverts restent lisibles et répondables.",
		"flowlio token revoke",
	} {
		if !strings.Contains(out, attendu) {
			t.Errorf("la sortie ne dit pas %q — l'humain croira avoir confiné le repo :\n%s", attendu, out)
		}
	}
}

// Les chemins réellement émis. C'est la partie qu'une relecture ne voit pas : une clé oubliée dans
// l'URL du DELETE, ou un ?team= perdu, ne se remarque qu'à l'exécution.
func TestTrustEmetLesBonsChemins(t *testing.T) {
	cas := []struct {
		name    string
		run     func(*client.Client) error
		attendu string
	}{
		{
			"allow",
			func(c *client.Client) error { return trustAllow(context.Background(), c, "acme", "CORE", "FRNT") },
			"POST /api/workspace/trust?team=acme",
		},
		{
			"deny",
			func(c *client.Client) error { return trustDeny(context.Background(), c, "acme", "CORE", "FRNT") },
			"DELETE /api/workspace/trust/CORE/FRNT?team=acme",
		},
		{
			"deny sans team (token de projet)",
			func(c *client.Client) error { return trustDeny(context.Background(), c, "", "CORE", "FRNT") },
			"DELETE /api/workspace/trust/CORE/FRNT",
		},
	}

	for _, c := range cas {
		t.Run(c.name, func(t *testing.T) {
			api := &trustAPI{decision: service.TrustDecision{First: "CORE", Second: "FRNT"}}
			cl := api.serve(t)

			if _, err := capture(t, func() error { return c.run(cl) }); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if len(api.seen) == 0 || api.seen[0] != c.attendu {
				t.Errorf("chemin émis = %v, attendu %q", api.seen, c.attendu)
			}
		})
	}
}

// Un 403 nu, une commande après qu'on a fait exporter FLOWLIO_TOKEN, est le pire message que ce
// produit puisse rendre : l'humain vient de suivre les instructions et se fait refuser.
//
// MUTATION : retirer explainAdminToken de runTrust fait tomber ce test.
func TestUn403DitQuelTokenUtiliser(t *testing.T) {
	api := &trustAPI{status: http.StatusForbidden}
	cl := api.serve(t)

	_, err := capture(t, func() error {
		return explainAdminToken(trustList(context.Background(), cl, "acme"))
	})
	if err == nil {
		t.Fatal("aucune erreur alors que l'API a rendu 403")
	}

	for _, attendu := range []string{"token d'ADMINISTRATION", "credentials.json", "FLOWLIO_TOKEN"} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("le message ne contient pas %q :\n%v", attendu, err)
		}
	}
}

// Une erreur qui n'est PAS un 403 traverse explainAdminToken sans être maquillée. Sans ce cas, un
// helper qui réécrirait tout en « mauvais token » passerait pour correct.
func TestUneAutreErreurNestPasMaquillee(t *testing.T) {
	api := &trustAPI{status: http.StatusNotFound}
	cl := api.serve(t)

	_, err := capture(t, func() error {
		return explainAdminToken(trustList(context.Background(), cl, "acme"))
	})
	if err == nil {
		t.Fatal("aucune erreur alors que l'API a rendu 404")
	}
	if strings.Contains(err.Error(), "token d'ADMINISTRATION") {
		t.Errorf("un 404 est présenté comme un problème de token :\n%v", err)
	}
}

func TestPossiblePairs(t *testing.T) {
	for _, c := range []struct{ n, want int }{{0, 0}, {1, 0}, {2, 1}, {3, 3}, {4, 6}, {30, 435}} {
		if got := possiblePairs(c.n); got != c.want {
			t.Errorf("possiblePairs(%d) = %d, attendu %d", c.n, got, c.want)
		}
	}
}
