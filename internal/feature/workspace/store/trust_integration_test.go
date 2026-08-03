package store_test

// Ce que ce fichier verrouille : les trois queries d'administration du graphe.
//
// Les CONTRAINTES de la table sont couvertes par TestDatabaseRejectsIllegalTrustEdges
// (store_integration_test.go) ; ici on vérifie ce que les queries font DE ces contraintes —
// idempotence, résolution des clés, frontière de team — c'est-à-dire ce qu'un humain voit.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
)

// onlyPair rend l'unique arête d'un graphe sous une forme INDÉPENDANTE DE L'ORDRE des deux clés.
//
// L'ordre de `first_key`/`second_key` est celui des UUID en base, pas l'ordre alphabétique ni
// celui de la commande : asserter `SecondKey == "FRNT"` serait un tirage à pile ou face à chaque
// exécution. C'est exactement le défaut que la première version de ce fichier portait, et qui
// n'est apparu qu'en jouant toute la suite.
//
// Trier ici n'affaiblit rien : une arête est une PAIRE, et prétendre tester un sens sur une
// structure qui n'en a pas serait tester une propriété que le produit ne promet pas.
func onlyPair(t *testing.T, edges []store.TrustEdge) string {
	t.Helper()

	if len(edges) != 1 {
		t.Fatalf("%d arêtes, attendu exactement 1 : %+v", len(edges), edges)
	}
	keys := []string{edges[0].FirstKey, edges[0].SecondKey}
	sort.Strings(keys)
	return strings.Join(keys, "↔")
}

// Le cycle nominal : ouvrir, rejouer, lire, fermer, rejouer.
//
// Les deux verbes sont idempotents et le disent — `created` et `removed` distinguent « fait » de
// « c'était déjà le cas » SANS second aller-retour. Sans ces drapeaux, la CLI devrait relire le
// graphe après chaque écriture pour savoir quoi afficher.
func TestTrustLifecycle(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	team := createTeam(t, st, db)

	if _, err := st.CreateProject(ctx, team.ID, "CORE", "core"); err != nil {
		t.Fatalf("CreateProject CORE: %v", err)
	}
	if _, err := st.CreateProject(ctx, team.ID, "FRNT", "front"); err != nil {
		t.Fatalf("CreateProject FRNT: %v", err)
	}

	t.Run("ouverture", func(t *testing.T) {
		created, err := st.AllowTrust(ctx, team.ID, "CORE", "FRNT")
		if err != nil {
			t.Fatalf("AllowTrust: %v", err)
		}
		if !created {
			t.Error("created = false à la première ouverture")
		}
	})

	t.Run("rejeu de l'ouverture", func(t *testing.T) {
		created, err := st.AllowTrust(ctx, team.ID, "CORE", "FRNT")
		if err != nil {
			t.Fatalf("AllowTrust (rejeu): %v", err)
		}
		if created {
			t.Error("created = true au rejeu : la commande n'est pas idempotente")
		}
	})

	// L'arête est une PAIRE : la déclarer dans l'autre sens ne crée pas une seconde ligne, et le
	// dit. Sous une table orientée, ce cas aurait rendu created = true et le graphe aurait porté
	// deux lignes pour une seule autorisation.
	t.Run("rejeu dans l'autre sens", func(t *testing.T) {
		created, err := st.AllowTrust(ctx, team.ID, "FRNT", "CORE")
		if err != nil {
			t.Fatalf("AllowTrust (sens inverse): %v", err)
		}
		if created {
			t.Error("created = true dans l'autre sens : l'arête n'est pas symétrique")
		}
	})

	t.Run("lecture", func(t *testing.T) {
		edges, err := st.ListTrustEdges(ctx, team.ID)
		if err != nil {
			t.Fatalf("ListTrustEdges: %v", err)
		}
		// Une paire déclarée trois fois (deux sens, un rejeu) reste UNE ligne.
		if got := onlyPair(t, edges); got != "CORE↔FRNT" {
			t.Errorf("graphe = %s, attendu CORE↔FRNT", got)
		}
		if edges[0].CreatedAt.IsZero() {
			t.Error("created_at nul : la date de déclaration est perdue")
		}
	})

	t.Run("fermeture", func(t *testing.T) {
		removed, err := st.RevokeTrust(ctx, team.ID, "FRNT", "CORE")
		if err != nil {
			t.Fatalf("RevokeTrust: %v", err)
		}
		if !removed {
			t.Error("removed = false alors que la paire était déclarée")
		}
	})

	t.Run("rejeu de la fermeture", func(t *testing.T) {
		removed, err := st.RevokeTrust(ctx, team.ID, "CORE", "FRNT")
		if err != nil {
			t.Fatalf("RevokeTrust (rejeu): %v", err)
		}
		if removed {
			t.Error("removed = true au rejeu : la commande n'est pas idempotente")
		}
	})

	t.Run("le graphe est vide", func(t *testing.T) {
		edges, err := st.ListTrustEdges(ctx, team.ID)
		if err != nil {
			t.Fatalf("ListTrustEdges: %v", err)
		}
		if len(edges) != 0 {
			t.Errorf("%d arêtes après fermeture, attendu 0", len(edges))
		}
	})
}

// Une clé qui ne se résout pas rend ErrNotFound sur les DEUX verbes.
//
// C'est l'écart assumé avec docs/DESIGN-TRUST.md, qui prévoyait un `:execrows` pour RevokeTrust :
// un DELETE nu aurait rendu « rien à retirer » à un humain qui vient de taper une clé de travers,
// c'est-à-dire une réussite apparente. Ces routes sont ADMIN et un admin énumère déjà tous les
// projets de toutes les teams : il n'y a aucun oracle à protéger, donc rien à gagner à taire
// l'erreur.
//
// MUTATION : revenir à `DELETE ... USING projects a, projects b` sans la CTE `pair` fait tomber le
// sous-test « fermeture, clé inconnue ».
func TestTrustRefusesUnknownKeys(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	team := createTeam(t, st, db)

	if _, err := st.CreateProject(ctx, team.ID, "CORE", "core"); err != nil {
		t.Fatalf("CreateProject CORE: %v", err)
	}

	cas := []struct {
		name  string
		first string
		last  string
	}{
		{"seconde clé inconnue", "CORE", "NOPE"},
		{"première clé inconnue", "NOPE", "CORE"},
		{"les deux inconnues", "NOPE", "NADA"},
		// La casse n'est PAS normalisée par la query : c'est le service qui met en majuscules.
		// Ce cas fige la frontière — si la normalisation migrait dans le SQL, il virerait au
		// vert et il faudrait décider où elle vit, plutôt que de l'avoir aux deux endroits.
		{"clé en minuscules, non normalisée par la query", "core", "CORE"},
	}

	for _, c := range cas {
		t.Run("ouverture, "+c.name, func(t *testing.T) {
			if _, err := st.AllowTrust(ctx, team.ID, c.first, c.last); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("AllowTrust(%s, %s): erreur = %v, attendu ErrNotFound", c.first, c.last, err)
			}
		})
		t.Run("fermeture, "+c.name, func(t *testing.T) {
			if _, err := st.RevokeTrust(ctx, team.ID, c.first, c.last); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("RevokeTrust(%s, %s): erreur = %v, attendu ErrNotFound", c.first, c.last, err)
			}
		})
	}
}

// Le scope de tenancy vit dans la query : une clé qui existe DANS UNE AUTRE TEAM est introuvable,
// pas seulement interdite. Et un graphe ne fuit jamais chez le voisin.
//
// MUTATION : retirer `a.team_id = @team_id` de l'une des trois queries fait tomber ce test.
func TestTrustNeverCrossesTeams(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	mine := createTeam(t, st, db)
	other := createTeam(t, st, db)

	if _, err := st.CreateProject(ctx, mine.ID, "CORE", "core chez moi"); err != nil {
		t.Fatalf("CreateProject CORE: %v", err)
	}
	if _, err := st.CreateProject(ctx, mine.ID, "FRNT", "front chez moi"); err != nil {
		t.Fatalf("CreateProject FRNT: %v", err)
	}
	// Le voisin a un projet dont la clé existe aussi chez moi : c'est le cas qui piège une query
	// qui résoudrait par clé sans team.
	if _, err := st.CreateProject(ctx, other.ID, "CORE", "core chez le voisin"); err != nil {
		t.Fatalf("CreateProject CORE (voisin): %v", err)
	}
	if _, err := st.CreateProject(ctx, other.ID, "OPS", "ops chez le voisin"); err != nil {
		t.Fatalf("CreateProject OPS: %v", err)
	}

	// Depuis ma team, la clé OPS du voisin n'existe pas.
	if _, err := st.AllowTrust(ctx, mine.ID, "CORE", "OPS"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("AllowTrust vers un projet du voisin: erreur = %v, attendu ErrNotFound", err)
	}

	// Chacune ouvre chez elle, sans se voir.
	if _, err := st.AllowTrust(ctx, mine.ID, "CORE", "FRNT"); err != nil {
		t.Fatalf("AllowTrust chez moi: %v", err)
	}
	if _, err := st.AllowTrust(ctx, other.ID, "CORE", "OPS"); err != nil {
		t.Fatalf("AllowTrust chez le voisin: %v", err)
	}

	mineEdges, err := st.ListTrustEdges(ctx, mine.ID)
	if err != nil {
		t.Fatalf("ListTrustEdges (moi): %v", err)
	}
	if got := onlyPair(t, mineEdges); got != "CORE↔FRNT" {
		t.Errorf("mon graphe = %s, attendu CORE↔FRNT", got)
	}

	otherEdges, err := st.ListTrustEdges(ctx, other.ID)
	if err != nil {
		t.Fatalf("ListTrustEdges (voisin): %v", err)
	}
	if got := onlyPair(t, otherEdges); got != "CORE↔OPS" {
		t.Errorf("le graphe du voisin = %s, attendu CORE↔OPS", got)
	}

	// Et je ne peux pas fermer la sienne, même en nommant exactement ses clés.
	if _, err := st.RevokeTrust(ctx, mine.ID, "CORE", "OPS"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("RevokeTrust sur la paire du voisin: erreur = %v, attendu ErrNotFound", err)
	}
	stillThere, err := st.ListTrustEdges(ctx, other.ID)
	if err != nil {
		t.Fatalf("ListTrustEdges (voisin, après): %v", err)
	}
	if len(stillThere) != 1 {
		t.Errorf("le graphe du voisin a %d arêtes après ma tentative, attendu 1", len(stillThere))
	}
}
