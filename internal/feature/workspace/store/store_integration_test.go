package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// newStore ouvre la base de test. Sans FLOWLIO_TEST_DATABASE_URL, le test est ignoré : la
// suite unitaire doit rester exécutable sans infrastructure.
func newStore(t *testing.T) (store.Store, *sql.DB) {
	t.Helper()

	dsn := os.Getenv("FLOWLIO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("FLOWLIO_TEST_DATABASE_URL non renseigné — test d'intégration ignoré")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("ouverture de la base: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("base injoignable: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return store.New(database.New(db)), db
}

// createTeam crée une team jetable et programme sa suppression : les lignes liées partent en
// cascade, la base de test ne dérive pas d'une exécution à l'autre.
func createTeam(t *testing.T, st store.Store, db *sql.DB) store.Team {
	t.Helper()

	slug := "test-" + strings.ToLower(uuid.NewString()[:8])
	team, err := st.CreateTeam(context.Background(), slug, "Team de test")
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM teams WHERE id = $1", team.ID); err != nil {
			t.Errorf("nettoyage de la team %s: %v", team.ID, err)
		}
	})
	return team
}

func TestTeamLifecycle(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	team := createTeam(t, st, db)

	bySlug, err := st.TeamBySlug(ctx, team.Slug)
	if err != nil {
		t.Fatalf("TeamBySlug: %v", err)
	}
	if bySlug.ID != team.ID {
		t.Errorf("TeamBySlug renvoie %s, attendu %s", bySlug.ID, team.ID)
	}

	byID, err := st.TeamByID(ctx, team.ID)
	if err != nil {
		t.Fatalf("TeamByID: %v", err)
	}
	if byID.Slug != team.Slug {
		t.Errorf("TeamByID renvoie %s, attendu %s", byID.Slug, team.Slug)
	}

	if _, err := st.CreateTeam(ctx, team.Slug, "Doublon"); !errors.Is(err, store.ErrConflict) {
		t.Errorf("slug dupliqué: erreur = %v, attendu ErrConflict", err)
	}

	if _, err := st.TeamBySlug(ctx, "slug-inexistant-"+uuid.NewString()[:8]); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("slug inconnu: erreur = %v, attendu ErrNotFound", err)
	}
}

// Propriété de sécurité centrale : une clé de projet appartenant à une autre team est
// introuvable, pas seulement interdite.
func TestProjectsAreIsolatedAcrossTeams(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	teamA := createTeam(t, st, db)
	teamB := createTeam(t, st, db)

	projectA, err := st.CreateProject(ctx, teamA.ID, "CORE", "core de A")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if _, err := st.ProjectByKey(ctx, teamB.ID, "CORE"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("la team B voit le projet de A: erreur = %v, attendu ErrNotFound", err)
	}
	if _, err := st.ProjectByID(ctx, teamB.ID, projectA.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("la team B lit le projet de A par identifiant: erreur = %v, attendu ErrNotFound", err)
	}

	projects, err := st.ListProjects(ctx, teamB.ID)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("la team B liste %d projets, attendu 0", len(projects))
	}

	// La même clé reste disponible dans une autre team : l'unicité est par team, pas globale.
	if _, err := st.CreateProject(ctx, teamB.ID, "CORE", "core de B"); err != nil {
		t.Errorf("clé CORE refusée dans la team B: %v", err)
	}
	if _, err := st.CreateProject(ctx, teamA.ID, "CORE", "doublon dans A"); !errors.Is(err, store.ErrConflict) {
		t.Errorf("clé dupliquée dans la team A: erreur = %v, attendu ErrConflict", err)
	}
}

func TestTokenLifecycle(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	team := createTeam(t, st, db)
	other := createTeam(t, st, db)

	project, err := st.CreateProject(ctx, team.ID, "CORE", "core")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	prefix := strings.ToLower(uuid.NewString()[:8]) + "abcd"
	token, err := st.CreateToken(ctx, team.ID, project.ID, "agent", prefix, "hash-de-test")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	tokens, err := st.ListTokens(ctx, team.ID, project.ID)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(tokens) != 1 || tokens[0].ID != token.ID {
		t.Fatalf("ListTokens renvoie %d tokens, attendu celui qui vient d'être créé", len(tokens))
	}

	// Une autre team ne peut pas révoquer ce token.
	if _, err := st.RevokeToken(ctx, other.ID, token.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("révocation croisée: erreur = %v, attendu ErrNotFound", err)
	}

	revoked, err := st.RevokeToken(ctx, team.ID, token.ID)
	if err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if !revoked.Revoked {
		t.Error("le token révoqué doit être marqué comme tel")
	}

	if _, err := st.RevokeToken(ctx, team.ID, token.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("seconde révocation: erreur = %v, attendu ErrNotFound", err)
	}
}

// Un token admin ne porte NI team NI projet, et la base le refuse — pas seulement le code.
//
// La forme `scope='admin' AND team_id IS NOT NULL` était légale jusqu'à la migration 000006 :
// rien ne la produisait, donc elle était invisible, et elle armait un piège pour la première
// session qui aurait une raison de créer un « admin épinglé à une team ». La doctrine du dépôt
// est de rendre la forme illégale NON INSÉRABLE plutôt que seulement non produite.
//
// Le SQL est écrit ici en direct, sans passer par le store : le store n'expose aucun chemin
// pour créer cette ligne, et c'est justement ce qu'on ne veut pas avoir à croire sur parole.
//
// MUTATION : rétablir la contrainte de 000002 (`scope='admin' AND project_id IS NULL`, sans la
// clause sur team_id) fait passer le premier cas, donc tomber ce test.
func TestDatabaseRejectsAdminTokenCarryingATeam(t *testing.T) {
	st, db := newStore(t)
	team := createTeam(t, st, db)

	project, err := st.CreateProject(context.Background(), team.ID, "CORE", "core")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	interdits := []struct {
		name      string
		teamID    any
		projectID any
	}{
		{"admin avec une team", team.ID, nil},
		{"admin avec un projet", nil, project.ID},
		{"admin complètement scopé", team.ID, project.ID},
	}

	for _, tc := range interdits {
		t.Run(tc.name, func(t *testing.T) {
			prefix := strings.ToLower(uuid.NewString()[:8]) + "abcd"
			_, err := db.Exec(
				`INSERT INTO tokens (scope, team_id, project_id, name, prefix, secret_hash)
				 VALUES ('admin', $1, $2, 'piège', $3, 'hash-de-test')`,
				tc.teamID, tc.projectID, prefix,
			)
			if err == nil {
				// La ligne ne devrait jamais exister : on la retire pour ne pas polluer la base
				// de dev, puis on échoue.
				_, _ = db.Exec("DELETE FROM tokens WHERE prefix = $1", prefix)
				t.Fatalf("la base a accepté un token admin (%s) : la contrainte tokens_scope_shape ne borne pas cette forme", tc.name)
			}
			if !strings.Contains(err.Error(), "tokens_scope_shape") {
				t.Errorf("refusé par autre chose que tokens_scope_shape: %v", err)
			}
		})
	}

	// Contre-épreuve : la forme légale, elle, passe. Sans elle, une contrainte qui refuserait
	// TOUT token admin passerait pour correcte.
	prefix := strings.ToLower(uuid.NewString()[:8]) + "abcd"
	if _, err := db.Exec(
		`INSERT INTO tokens (scope, team_id, project_id, name, prefix, secret_hash)
		 VALUES ('admin', NULL, NULL, 'admin global', $1, 'hash-de-test')`, prefix,
	); err != nil {
		t.Fatalf("la base refuse l'admin global, qui est la seule forme que l'amorçage produit: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM tokens WHERE prefix = $1", prefix); err != nil {
			t.Errorf("nettoyage du token %s: %v", prefix, err)
		}
	})
}

// La contrainte de format des clés est portée par la base : la validation applicative donne le
// message, la base donne la garantie.
func TestDatabaseRejectsMalformedKey(t *testing.T) {
	st, db := newStore(t)
	team := createTeam(t, st, db)

	for _, key := range []string{"minuscule", "C", "TROP-LONGUE-CLE", ""} {
		t.Run(fmt.Sprintf("clé %q", key), func(t *testing.T) {
			if _, err := st.CreateProject(context.Background(), team.ID, key, "x"); !errors.Is(err, store.ErrConflict) {
				t.Errorf("erreur = %v, attendu ErrConflict (violation de contrainte)", err)
			}
		})
	}
}

// ordered rend la paire canonique d'une arête de confiance : low < high, au sens de Postgres.
//
// uuid.UUID est un [16]byte, donc l'opérateur < de Go ne s'applique pas ; la comparaison octet par
// octet est exactement celle que Postgres fait sur le type uuid.
func ordered(a, b uuid.UUID) (low, high uuid.UUID) {
	if bytes.Compare(a[:], b[:]) < 0 {
		return a, b
	}
	return b, a
}

// allowTrust pose une arête de confiance par SQL direct, en la normalisant.
func allowTrust(t *testing.T, db *sql.DB, teamID, a, b uuid.UUID) error {
	t.Helper()

	low, high := ordered(a, b)
	_, err := db.Exec(
		"INSERT INTO project_trust (team_id, low_project_id, high_project_id) VALUES ($1, $2, $3)",
		teamID, low, high,
	)
	return err
}

// La base refuse les neuf formes illégales du graphe de confiance — pas le code, la BASE.
//
// C'est la doctrine du dépôt appliquée à `project_trust` : on rend la forme illégale NON
// INSÉRABLE plutôt que seulement non produite. Les FK COMPOSITES `(project_id, team_id)` sont ce
// qui fait le travail : l'unique colonne `team_id` doit satisfaire les DEUX à la fois, donc une
// arête entre deux projets de teams différentes est impossible, y compris si l'appelant MENT sur
// `team_id` — les deux sens du mensonge sont testés.
//
// MUTATION : retirer `project_trust_ordered` fait passer l'auto-arête et le miroir ; retirer une
// des deux FK composites fait passer l'arête inter-team correspondante.
func TestDatabaseRejectsIllegalTrustEdges(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	teamA := createTeam(t, st, db)
	teamB := createTeam(t, st, db)
	teamC := createTeam(t, st, db)

	core, err := st.CreateProject(ctx, teamA.ID, "CORE", "core de A")
	if err != nil {
		t.Fatalf("CreateProject CORE: %v", err)
	}
	front, err := st.CreateProject(ctx, teamA.ID, "FRNT", "front de A")
	if err != nil {
		t.Fatalf("CreateProject FRNT: %v", err)
	}
	voisin, err := st.CreateProject(ctx, teamB.ID, "CORE", "core de B")
	if err != nil {
		t.Fatalf("CreateProject voisin: %v", err)
	}

	lowAB, highAB := ordered(core.ID, voisin.ID)

	interdits := []struct {
		name     string
		exec     func() error
		contains string
	}{
		{
			"arête inter-team, team_id du premier projet",
			func() error { return allowTrust(t, db, teamA.ID, core.ID, voisin.ID) },
			"project_trust_",
		},
		{
			"arête inter-team, en mentant sur team_id",
			func() error { return allowTrust(t, db, teamB.ID, core.ID, voisin.ID) },
			"project_trust_",
		},
		{
			"arête inter-team, team_id d'une troisième team",
			func() error { return allowTrust(t, db, teamC.ID, core.ID, voisin.ID) },
			"project_trust_",
		},
		{
			"auto-arête",
			func() error {
				_, err := db.Exec(
					"INSERT INTO project_trust (team_id, low_project_id, high_project_id) VALUES ($1, $2, $2)",
					teamA.ID, core.ID)
				return err
			},
			"project_trust_ordered",
		},
		{
			"paire non canonique (miroir)",
			func() error {
				_, err := db.Exec(
					"INSERT INTO project_trust (team_id, low_project_id, high_project_id) VALUES ($1, $2, $3)",
					teamA.ID, highAB, lowAB)
				return err
			},
			"project_trust_ordered",
		},
	}

	for _, tc := range interdits {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.exec()
			if err == nil {
				t.Fatalf("la base a accepté : %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("refusé par autre chose que %s: %v", tc.contains, err)
			}
		})
	}

	// L'arête légale, elle, passe — sans contre-épreuve, une contrainte qui refuserait TOUT
	// passerait pour correcte.
	if err := allowTrust(t, db, teamA.ID, core.ID, front.ID); err != nil {
		t.Fatalf("arête légale refusée: %v", err)
	}

	t.Run("doublon", func(t *testing.T) {
		if err := allowTrust(t, db, teamA.ID, front.ID, core.ID); err == nil {
			t.Fatal("la base a accepté un doublon (la paire est déjà déclarée dans l'autre sens)")
		} else if !strings.Contains(err.Error(), "project_trust_pkey") {
			t.Errorf("refusé par autre chose que la clé primaire: %v", err)
		}
	})

	t.Run("déplacer un projet vers une autre team", func(t *testing.T) {
		if _, err := db.Exec("UPDATE projects SET team_id = $1 WHERE id = $2", teamB.ID, core.ID); err == nil {
			t.Error("un projet portant une arête a pu changer de team")
		}
	})

	t.Run("déplacer une arête vers une autre team", func(t *testing.T) {
		if _, err := db.Exec("UPDATE project_trust SET team_id = $1 WHERE team_id = $2", teamB.ID, teamA.ID); err == nil {
			t.Error("une arête a pu être déplacée dans une autre team")
		}
	})

	t.Run("suppression d'un projet : cascade", func(t *testing.T) {
		if _, err := db.Exec("DELETE FROM projects WHERE id = $1", front.ID); err != nil {
			t.Fatalf("suppression du projet: %v", err)
		}
		var restantes int
		if err := db.QueryRow(
			"SELECT count(*) FROM project_trust WHERE team_id = $1", teamA.ID,
		).Scan(&restantes); err != nil {
			t.Fatalf("comptage: %v", err)
		}
		if restantes != 0 {
			t.Errorf("%d arête(s) survivent au projet supprimé, attendu 0", restantes)
		}
	})
}
