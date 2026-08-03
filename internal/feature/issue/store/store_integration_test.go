package store_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-ia/internal/database"
	"github.com/Coddyum/flowlio-ia/internal/feature/issue/store"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// project est un projet de test et son scope de tenancy : exactement ce qu'un token porte.
type project struct {
	teamID uuid.UUID
	id     uuid.UUID
	key    string
}

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

	return store.New(database.New(db), db), db
}

// newTeam crée une team jetable. La suppression emporte tout en cascade.
func newTeam(t *testing.T, db *sql.DB) uuid.UUID {
	t.Helper()

	slug := "test-" + strings.ToLower(uuid.NewString()[:8])
	var teamID uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO teams (slug, name) VALUES ($1, $2) RETURNING id", slug, "Team de test",
	).Scan(&teamID); err != nil {
		t.Fatalf("création de la team: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM teams WHERE id = $1", teamID); err != nil {
			t.Errorf("nettoyage de la team %s: %v", teamID, err)
		}
	})
	return teamID
}

// newProject crée un projet dans une team. Les fixtures passent par du SQL direct : la feature
// issue ne doit dépendre d'aucune autre feature, pas même dans ses tests.
func newProject(t *testing.T, db *sql.DB, teamID uuid.UUID, key string) project {
	t.Helper()

	var id uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
		teamID, key, "Projet "+key,
	).Scan(&id); err != nil {
		t.Fatalf("création du projet %s: %v", key, err)
	}
	return project{teamID: teamID, id: id, key: key}
}

// trust déclare une confiance entre deux projets de test.
//
// Le graphe est posé À LA MAIN dans chaque test qui en a besoin, exactement comme le scope de
// tenancy : le cacher dans newProject masquerait la garantie que ces tests existent pour prouver.
// C'est la seule occurrence de `project_trust` dans du Go de ce dépôt hors code généré, et elle
// est dans un fichier de test — une fixture, jamais une décision.
func trust(t *testing.T, db *sql.DB, a, b project) {
	t.Helper()

	if a.teamID != b.teamID {
		t.Fatalf("confiance %s ↔ %s: teams différentes, la paire est non insérable", a.key, b.key)
	}
	if _, err := db.Exec(
		`INSERT INTO project_trust (team_id, low_project_id, high_project_id)
		 VALUES ($1, least($2::uuid, $3::uuid), greatest($2::uuid, $3::uuid))`,
		a.teamID, a.id, b.id,
	); err != nil {
		t.Fatalf("confiance %s ↔ %s: %v", a.key, b.key, err)
	}
}

// open ouvre une issue de from vers to, en passant par le chemin nominal.
func open(t *testing.T, st store.Store, from, to project, title string) store.Issue {
	t.Helper()

	ctx := context.Background()
	var created store.Issue
	err := st.WithTx(ctx, func(tx store.Store) error {
		var err error
		created, err = tx.CreateIssue(ctx, store.NewIssue{
			TeamID:          from.teamID,
			AuthorProjectID: from.id,
			ToProjectKey:    to.key,
			Title:           title,
		})
		if err != nil {
			return err
		}
		if err := tx.AddFirstMessage(ctx, created.ID, from.id, "corps de la question"); err != nil {
			return err
		}
		return tx.AppendEvent(ctx, store.Event{
			TeamID:         from.teamID,
			ProjectID:      created.ProjectID,
			ActorProjectID: from.id,
			Kind:           store.KindIssueOpened,
			SubjectID:      created.ID,
		})
	})
	if err != nil {
		t.Fatalf("ouverture de l'issue %q: %v", title, err)
	}
	return created
}

// refFor compose la référence d'une issue pour un appelant donné.
func refFor(caller project, target project, number int64) store.Ref {
	return store.Ref{
		TeamID:          caller.teamID,
		CallerProjectID: caller.id,
		ProjectKey:      target.key,
		Number:          number,
	}
}

func TestIssueConversation(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)
	web := newProject(t, db, teamID, "WEB")
	core := newProject(t, db, teamID, "CORE")
	trust(t, db, web, core)

	issue := open(t, st, web, core, "Le endpoint renvoie 500 sur un slug vide")
	if issue.Number != 1 {
		t.Errorf("numéro = %d, attendu 1 (compteur du DESTINATAIRE)", issue.Number)
	}
	if issue.State != "open" {
		t.Errorf("état = %q, attendu open", issue.State)
	}

	ref := refFor(core, core, issue.Number)

	// Le destinataire répond : l'issue passe en `answered`, l'auteur n'est plus bloqué.
	answered, err := st.Answer(ctx, store.Answer{Ref: ref, Body: "corrigé en 1a2b3c"})
	if err != nil {
		t.Fatalf("Answer (destinataire): %v", err)
	}
	if answered.State != "answered" {
		t.Errorf("état = %q après réponse du destinataire, attendu answered", answered.State)
	}

	// L'auteur relance : l'issue repasse en `open`, le destinataire redevient en dette.
	reopened, err := st.Answer(ctx, store.Answer{
		Ref:  refFor(web, core, issue.Number),
		Body: "toujours reproductible chez moi",
	})
	if err != nil {
		t.Fatalf("Answer (auteur): %v", err)
	}
	if reopened.State != "open" {
		t.Errorf("état = %q après relance de l'auteur, attendu open", reopened.State)
	}

	// Le fil contient les trois messages, dans l'ordre.
	found, err := st.IssueByRef(ctx, ref)
	if err != nil {
		t.Fatalf("IssueByRef: %v", err)
	}
	messages, err := st.ListMessages(ctx, ref, found.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("%d messages, attendu 3", len(messages))
	}
	if messages[0].AuthorKey != "WEB" || messages[1].AuthorKey != "CORE" {
		t.Errorf("auteurs du fil = %s puis %s, attendu WEB puis CORE",
			messages[0].AuthorKey, messages[1].AuthorKey)
	}

	// Clôture, puis refus de toute réponse ultérieure : sans ce garde, une réponse tardive
	// ressusciterait une discussion terminée dans l'inbox du correspondant.
	closed, err := st.Answer(ctx, store.Answer{Ref: ref, Body: "on ferme", Close: true})
	if err != nil {
		t.Fatalf("Answer (clôture): %v", err)
	}
	if closed.State != "closed" {
		t.Errorf("état = %q après clôture, attendu closed", closed.State)
	}

	if _, err := st.Answer(ctx, store.Answer{Ref: ref, Body: "encore un mot"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("réponse à une issue close: erreur = %v, attendu ErrNotFound", err)
	}
}

// Le cœur du produit : un projet tiers de la MÊME team ne voit rien d'une conversation à
// laquelle il ne participe pas, et ne peut pas y écrire.
func TestIssuesAreInvisibleToThirdProjects(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)

	web := newProject(t, db, teamID, "WEB")
	core := newProject(t, db, teamID, "CORE")
	spy := newProject(t, db, teamID, "SPY")

	// SPY est de confiance avec CORE : ce test prouve l'invisibilité d'une conversation, pas le
	// graphe. Sans cette arête, les assertions de lecture resteraient vertes par simple absence
	// d'autorisation d'écriture, ce qui masquerait la propriété qu'elles existent pour établir.
	trust(t, db, web, core)
	trust(t, db, spy, core)

	issue := open(t, st, web, core, "question privée entre WEB et CORE")
	spyRef := refFor(spy, core, issue.Number)

	t.Run("lecture", func(t *testing.T) {
		if _, err := st.IssueByRef(ctx, spyRef); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("SPY lit l'issue: erreur = %v, attendu ErrNotFound", err)
		}
	})

	t.Run("listing", func(t *testing.T) {
		issues, err := st.ListIssues(ctx, store.IssueFilter{
			TeamID: teamID, ProjectID: spy.id, Limit: 50,
		})
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}
		if len(issues) != 0 {
			t.Errorf("SPY liste %d issues, attendu 0", len(issues))
		}
	})

	t.Run("fil de messages", func(t *testing.T) {
		messages, err := st.ListMessages(ctx, spyRef, issue.ID)
		if err != nil {
			t.Fatalf("ListMessages: %v", err)
		}
		if len(messages) != 0 {
			t.Errorf("SPY lit %d messages, attendu 0 (même en connaissant l'identifiant)", len(messages))
		}
	})

	t.Run("réponse", func(t *testing.T) {
		if _, err := st.Answer(ctx, store.Answer{Ref: spyRef, Body: "je m'invite"}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("SPY répond: erreur = %v, attendu ErrNotFound", err)
		}
	})

	t.Run("clôture", func(t *testing.T) {
		if _, err := st.Answer(ctx, store.Answer{Ref: spyRef, Body: "je ferme", Close: true}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("SPY ferme: erreur = %v, attendu ErrNotFound", err)
		}
	})

	// La conversation est intacte pour ses deux participants.
	for _, caller := range []project{web, core} {
		still, err := st.IssueByRef(ctx, refFor(caller, core, issue.Number))
		if err != nil {
			t.Fatalf("IssueByRef pour %s: %v", caller.key, err)
		}
		if still.State != "open" {
			t.Errorf("l'issue vue par %s est en %q, attendu open", caller.key, still.State)
		}
	}
}

// Une issue ne franchit jamais la frontière d'une team, et une tentative ne doit pas révéler
// l'existence du projet visé — ni faire avancer son compteur.
func TestIssuesCannotCrossTeams(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	teamA := newTeam(t, db)
	teamB := newTeam(t, db)
	web := newProject(t, db, teamA, "WEB")
	sibling := newProject(t, db, teamA, "OPS")
	foreign := newProject(t, db, teamB, "CORE")

	// WEB est de confiance avec un frère de SA team. Sans cette arête, ce test resterait vert
	// pour la mauvaise raison : le graphe vide masquerait la frontière de team qu'il existe pour
	// prouver, et le rendrait indifférent à une régression de scope.
	trust(t, db, web, sibling)

	// La frontière n'est pas seulement une absence de résultat : la paire inter-team est NON
	// INSÉRABLE, quel que soit le team_id passé. Aucun humain ne peut donc ouvrir ce canal.
	for _, claimed := range []struct {
		name   string
		teamID uuid.UUID
	}{
		{"team de l'émetteur", teamA},
		{"team du destinataire", teamB},
	} {
		if _, err := db.Exec(
			`INSERT INTO project_trust (team_id, low_project_id, high_project_id)
			 VALUES ($1, least($2::uuid, $3::uuid), greatest($2::uuid, $3::uuid))`,
			claimed.teamID, web.id, foreign.id,
		); err == nil {
			t.Fatalf("arête inter-team insérée en annonçant la %s, attendu une violation de clé étrangère", claimed.name)
		}
	}

	err := st.WithTx(ctx, func(tx store.Store) error {
		_, err := tx.CreateIssue(ctx, store.NewIssue{
			TeamID:          teamA,
			AuthorProjectID: web.id,
			ToProjectKey:    "CORE", // existe, mais dans la team B
			Title:           "traversée de team",
		})
		return err
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("issue vers une autre team: erreur = %v, attendu ErrNotFound", err)
	}

	// Une clé qui n'existe nulle part échoue de la MÊME façon : « inexistant » et « hors team »
	// restent indiscernables, sinon on pourrait cartographier les projets des autres teams.
	err = st.WithTx(ctx, func(tx store.Store) error {
		_, err := tx.CreateIssue(ctx, store.NewIssue{
			TeamID:          teamA,
			AuthorProjectID: web.id,
			ToProjectKey:    "NOPE",
			Title:           "clé inexistante",
		})
		return err
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("issue vers une clé inconnue: erreur = %v, attendu ErrNotFound", err)
	}

	// Le compteur du projet étranger n'a pas bougé : on ne peut pas le faire avancer à distance.
	var next int64
	if err := db.QueryRow("SELECT next_number FROM projects WHERE id = $1", foreign.id).Scan(&next); err != nil {
		t.Fatalf("lecture du compteur: %v", err)
	}
	if next != 1 {
		t.Errorf("compteur du projet étranger = %d, attendu 1 (aucun numéro consommé)", next)
	}
}

// tasks et issues partagent le compteur du projet : une référence désigne toujours un seul objet.
func TestIssuesAndTasksShareTheProjectCounter(t *testing.T) {
	st, db := newStore(t)
	teamID := newTeam(t, db)
	web := newProject(t, db, teamID, "WEB")
	core := newProject(t, db, teamID, "CORE")
	trust(t, db, web, core)

	first := open(t, st, web, core, "première issue chez CORE")
	if first.Number != 1 {
		t.Fatalf("numéro de la première issue = %d, attendu 1", first.Number)
	}

	// Une tâche créée ensuite dans CORE prend le numéro suivant, pas le même.
	var claimed int64
	if err := db.QueryRow(
		"UPDATE projects SET next_number = next_number + 1 WHERE id = $1 RETURNING next_number - 1",
		core.id,
	).Scan(&claimed); err != nil {
		t.Fatalf("réservation d'un numéro de tâche: %v", err)
	}
	if claimed != 2 {
		t.Errorf("numéro de la tâche = %d, attendu 2 (compteur partagé avec les issues)", claimed)
	}

	second := open(t, st, web, core, "seconde issue chez CORE")
	if second.Number != 3 {
		t.Errorf("numéro de la seconde issue = %d, attendu 3", second.Number)
	}

	// Le compteur de WEB, lui, n'a pas bougé : chaque projet a le sien.
	var webNext int64
	if err := db.QueryRow("SELECT next_number FROM projects WHERE id = $1", web.id).Scan(&webNext); err != nil {
		t.Fatalf("lecture du compteur de WEB: %v", err)
	}
	if webNext != 1 {
		t.Errorf("compteur de WEB = %d, attendu 1 : ouvrir une issue consomme le numéro du destinataire", webNext)
	}
}

// Une question à son propre projet n'a pas de sens : elle serait à la fois entrante et sortante,
// et ne pourrait jamais atteindre `answered` puisque la transition dépend de l'émetteur.
//
// Depuis le graphe de confiance, le refus est ErrNotFound et non plus ErrConflict : l'auto-adressage
// donne least = greatest, forme que project_trust_ordered rend NON INSÉRABLE, donc jamais présente
// dans le graphe. La CHECK issues_not_self n'est plus atteinte — le refus devient uniforme avec
// celui d'une clé inconnue, d'une autre team ou d'une paire non déclarée, et c'est le but.
func TestSelfIssueIsRejectedByTheDatabase(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)
	web := newProject(t, db, teamID, "WEB")
	core := newProject(t, db, teamID, "CORE")

	// WEB a un voisin déclaré : le refus ci-dessous ne peut donc pas venir d'un graphe vide.
	trust(t, db, web, core)

	err := st.WithTx(ctx, func(tx store.Store) error {
		_, err := tx.CreateIssue(ctx, store.NewIssue{
			TeamID:          teamID,
			AuthorProjectID: web.id,
			ToProjectKey:    "WEB",
			Title:           "je me parle à moi-même",
		})
		return err
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("issue vers soi-même: erreur = %v, attendu ErrNotFound (aucune auto-arête n'est insérable)", err)
	}

	// L'auto-arête est refusée par la base elle-même : la propriété ci-dessus ne repose pas sur
	// une convention d'écriture, mais sur une contrainte.
	if _, err := db.Exec(
		`INSERT INTO project_trust (team_id, low_project_id, high_project_id) VALUES ($1, $2, $2)`,
		teamID, web.id,
	); err == nil {
		t.Fatal("une auto-arête a été insérée, attendu une violation de project_trust_ordered")
	}

	// Et le compteur de WEB n'a pas bougé : un refus ne consomme aucun numéro, y compris quand
	// l'émetteur et le destinataire sont le même projet.
	var next int64
	if err := db.QueryRow("SELECT next_number FROM projects WHERE id = $1", web.id).Scan(&next); err != nil {
		t.Fatalf("lecture du compteur de WEB: %v", err)
	}
	if next != 1 {
		t.Errorf("compteur de WEB = %d, attendu 1 (aucun numéro consommé par un auto-adressage refusé)", next)
	}
}

func TestListIssuesFiltersByRoleAndState(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)
	web := newProject(t, db, teamID, "WEB")
	core := newProject(t, db, teamID, "CORE")
	trust(t, db, web, core)

	outgoing := open(t, st, web, core, "WEB demande à CORE")
	open(t, st, core, web, "CORE demande à WEB")

	base := store.IssueFilter{TeamID: teamID, ProjectID: web.id, Limit: 50}

	t.Run("les deux sens sans filtre", func(t *testing.T) {
		issues, err := st.ListIssues(ctx, base)
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}
		if len(issues) != 2 {
			t.Fatalf("%d issues, attendu 2", len(issues))
		}
	})

	t.Run("entrantes", func(t *testing.T) {
		filter := base
		filter.Role = "incoming"
		issues, err := st.ListIssues(ctx, filter)
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}
		if len(issues) != 1 || !issues[0].Incoming {
			t.Fatalf("%d issues entrantes pour WEB, attendu 1 marquée entrante", len(issues))
		}
	})

	t.Run("sortantes", func(t *testing.T) {
		filter := base
		filter.Role = "outgoing"
		issues, err := st.ListIssues(ctx, filter)
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}
		if len(issues) != 1 || issues[0].Incoming {
			t.Fatalf("%d issues sortantes pour WEB, attendu 1 marquée sortante", len(issues))
		}
		if issues[0].ProjectKey != "CORE" {
			t.Errorf("clé de la référence = %s, attendu CORE (le destinataire possède l'issue)",
				issues[0].ProjectKey)
		}
	})

	t.Run("les closes sont exclues par défaut", func(t *testing.T) {
		if _, err := st.Answer(ctx, store.Answer{
			Ref:   refFor(web, core, outgoing.Number),
			Body:  "abandon",
			Close: true,
		}); err != nil {
			t.Fatalf("clôture: %v", err)
		}

		issues, err := st.ListIssues(ctx, base)
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}
		if len(issues) != 1 {
			t.Errorf("%d issues actives, attendu 1", len(issues))
		}

		filter := base
		filter.IncludeClosed = true
		all, err := st.ListIssues(ctx, filter)
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}
		if len(all) != 2 {
			t.Errorf("%d issues au total, attendu 2", len(all))
		}
	})
}

// Le rôle restreint ce qui est déjà visible ; il ne peut jamais l'élargir. Un projet tiers qui
// demanderait « les entrantes » ne doit pas pour autant voir celles des autres.
func TestRoleFilterNeverWidensVisibility(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)

	web := newProject(t, db, teamID, "WEB")
	core := newProject(t, db, teamID, "CORE")
	spy := newProject(t, db, teamID, "SPY")

	// Même raison qu'au test précédent : SPY est de confiance avec CORE, donc ce qu'il ne voit
	// pas, il ne le voit pas parce qu'il ne participe pas — pas parce qu'il n'a pas le droit
	// d'écrire.
	trust(t, db, web, core)
	trust(t, db, spy, core)

	open(t, st, web, core, "conversation privée")

	for _, role := range []string{"", "incoming", "outgoing"} {
		issues, err := st.ListIssues(ctx, store.IssueFilter{
			TeamID: teamID, ProjectID: spy.id, Role: role, IncludeClosed: true, Limit: 50,
		})
		if err != nil {
			t.Fatalf("ListIssues(role=%q): %v", role, err)
		}
		if len(issues) != 0 {
			t.Errorf("SPY voit %d issues avec role=%q, attendu 0", len(issues), role)
		}
	}
}

// L'imbrication de transactions doit échouer bruyamment : une seconde transaction attendrait sur
// une autre connexion le verrou détenu par la première.
func TestNestedTransactionIsRefused(t *testing.T) {
	st, _ := newStore(t)

	err := st.WithTx(context.Background(), func(tx store.Store) error {
		return tx.WithTx(context.Background(), func(store.Store) error { return nil })
	})
	if err == nil {
		t.Fatal("une transaction imbriquée a été acceptée")
	}
	if !strings.Contains(err.Error(), "imbriquée") {
		t.Errorf("erreur = %v, attendu un refus explicite d'imbrication", err)
	}
}

// Miroir de TestCancelledRequestCreatesNothing côté issue, sur le chemin le plus coûteux : une
// issue dupliquée pollue l'inbox d'un AUTRE repo. Quand le client abandonne, le contexte est
// annulé et la transaction avec lui — aucune issue, et surtout aucun numéro consommé chez le
// destinataire, dont le compteur n'appartient pas à l'émetteur.
func TestCancelledRequestOpensNothing(t *testing.T) {
	st, db := newStore(t)
	teamID := newTeam(t, db)
	frnt := newProject(t, db, teamID, "FRNT")
	core := newProject(t, db, teamID, "CORE")
	trust(t, db, frnt, core)

	ctx, cancel := context.WithCancel(context.Background())
	err := st.WithTx(ctx, func(tx store.Store) error {
		created, err := tx.CreateIssue(ctx, store.NewIssue{
			TeamID:          teamID,
			AuthorProjectID: frnt.id,
			ToProjectKey:    core.key,
			Title:           "question dont la réponse se perdra",
		})
		if err != nil {
			return err
		}

		// Le client abandonne une fois le numéro du destinataire réservé.
		cancel()

		return tx.AddFirstMessage(ctx, created.ID, frnt.id, "corps de la question")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("erreur = %v, attendu context.Canceled", err)
	}

	var count int
	if err := db.QueryRow("SELECT count(*) FROM issues WHERE team_id = $1", teamID).Scan(&count); err != nil {
		t.Fatalf("comptage des issues: %v", err)
	}
	if count != 0 {
		t.Fatalf("%d issue(s) créée(s) par une requête annulée, attendu 0", count)
	}

	// Le rejeu de l'agent prend le numéro 1 : le compteur du destinataire n'a pas bougé.
	replayed := open(t, st, frnt, core, "question dont la réponse se perdra")
	if replayed.Number != 1 {
		t.Errorf("numéro = %d après annulation, attendu 1 (compteur du destinataire intact)", replayed.Number)
	}
}

// M5 — le refus vit dans la QUERY, pas dans un `if` de service.
//
// Ce test court-circuite entièrement le service : il fabrique un store.NewIssue à la main et
// appelle le store directement, exactement comme le ferait un appelant fautif d'un futur module.
// Il doit rendre ErrNotFound quand même. Si le prédicat migrait un jour dans un `if` en amont, ce
// test serait le seul du dépôt à virer au rouge.
//
// Il prouve aussi la SYMÉTRIE de l'arête : une seule ligne, et le canal s'ouvre dans les deux
// sens. Une table orientée aurait laissé passer un sens et refusé l'autre.
func TestTrustPredicateLivesInTheQueryNotInAService(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)

	frnt := newProject(t, db, teamID, "FRNT")
	core := newProject(t, db, teamID, "CORE")
	ops := newProject(t, db, teamID, "OPS")

	// FRNT ↔ CORE seulement. OPS existe, est dans la même team, et n'a aucune arête.
	trust(t, db, frnt, core)

	createDirectly := func(from, to project) error {
		return st.WithTx(ctx, func(tx store.Store) error {
			_, err := tx.CreateIssue(ctx, store.NewIssue{
				TeamID:          from.teamID,
				AuthorProjectID: from.id,
				ToProjectKey:    to.key,
				Title:           "appel direct au store, service court-circuité",
			})
			return err
		})
	}

	t.Run("paire non déclarée", func(t *testing.T) {
		if err := createDirectly(frnt, ops); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("FRNT → OPS: erreur = %v, attendu ErrNotFound", err)
		}
	})

	t.Run("paire non déclarée, sens inverse", func(t *testing.T) {
		if err := createDirectly(ops, frnt); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("OPS → FRNT: erreur = %v, attendu ErrNotFound", err)
		}
	})

	// L'arête est une PAIRE, pas une flèche : elle a été posée dans le sens FRNT → CORE, et les
	// deux sens passent.
	t.Run("paire déclarée, sens de déclaration", func(t *testing.T) {
		if err := createDirectly(frnt, core); err != nil {
			t.Errorf("FRNT → CORE: %v, attendu succès", err)
		}
	})

	t.Run("paire déclarée, sens inverse", func(t *testing.T) {
		if err := createDirectly(core, frnt); err != nil {
			t.Errorf("CORE → FRNT: %v, attendu succès (l'arête est symétrique)", err)
		}
	})

	// Le refus est indiscernable d'une clé inconnue : même erreur, sur le même chemin.
	t.Run("clé inconnue", func(t *testing.T) {
		err := st.WithTx(ctx, func(tx store.Store) error {
			_, err := tx.CreateIssue(ctx, store.NewIssue{
				TeamID:          teamID,
				AuthorProjectID: frnt.id,
				ToProjectKey:    "NOPE",
				Title:           "clé qui n'existe nulle part",
			})
			return err
		})
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("FRNT → NOPE: erreur = %v, attendu ErrNotFound, identique à une paire non déclarée", err)
		}
	})
}

// M2 — un refus de confiance ne laisse AUCUNE trace.
//
// C'est le test que la mutation « déplacer l'EXISTS sur l'INSERT ... SELECT » fait virer au rouge.
// Sous cette mutation l'UPDATE matche toujours : le compteur du destinataire avance, et la ligne
// projet reste verrouillée pendant toute la transaction du refusé — un émetteur non autorisé
// gagnerait un canal d'écriture chez sa victime ET un déni de service sur un tiers légitime.
//
// Les quatre compteurs sont relevés avant et après, et comparés à l'identique. Aucune assertion
// de latence : ce que la mutation ouvre se mesure déterministement.
func TestRefusedIssueLeavesNoTrace(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)

	frnt := newProject(t, db, teamID, "FRNT")
	ops := newProject(t, db, teamID, "OPS")

	type snapshot struct {
		nextNumber                      int64
		issues, messages, events, edges int64
	}
	take := func() snapshot {
		t.Helper()
		var s snapshot
		if err := db.QueryRow(
			`SELECT (SELECT next_number FROM projects WHERE id = $1),
			        (SELECT count(*) FROM issues         WHERE team_id = $2),
			        (SELECT count(*) FROM issue_messages m JOIN issues i ON i.id = m.issue_id WHERE i.team_id = $2),
			        (SELECT count(*) FROM events         WHERE team_id = $2),
			        (SELECT count(*) FROM project_trust  WHERE team_id = $2)`,
			ops.id, teamID,
		).Scan(&s.nextNumber, &s.issues, &s.messages, &s.events, &s.edges); err != nil {
			t.Fatalf("relevé de l'état: %v", err)
		}
		return s
	}

	before := take()

	// LA TRANSACTION EST COMMITÉE, et c'est tout l'intérêt du test.
	//
	// Renvoyer l'erreur ferait ROLLBACK, et le rollback masquerait la mutation : sous « EXISTS
	// déplacé sur l'INSERT », l'UPDATE matche, next_number avance, puis la transaction annule
	// tout — et le test resterait vert en observant une propriété que le prédicat ne fournit pas.
	// La garantie du canal 3 est « fermé PAR LE PRÉDICAT, sûr même si la transaction est
	// commitée » : on la vérifie donc en commitant.
	var refusal error
	if err := st.WithTx(ctx, func(tx store.Store) error {
		_, refusal = tx.CreateIssue(ctx, store.NewIssue{
			TeamID:          teamID,
			AuthorProjectID: frnt.id,
			ToProjectKey:    ops.key,
			Title:           "tentative vers une paire non déclarée",
		})
		return nil // commit délibéré
	}); err != nil {
		t.Fatalf("la transaction du refus n'a pas commité: %v", err)
	}
	if !errors.Is(refusal, store.ErrNotFound) {
		t.Fatalf("erreur = %v, attendu ErrNotFound", refusal)
	}

	after := take()

	if after.nextNumber != before.nextNumber {
		t.Errorf("next_number d'OPS = %d, attendu %d : un refus ne réserve aucun numéro chez la victime",
			after.nextNumber, before.nextNumber)
	}
	if after.issues != before.issues {
		t.Errorf("%d issues, attendu %d", after.issues, before.issues)
	}
	if after.messages != before.messages {
		t.Errorf("%d messages, attendu %d", after.messages, before.messages)
	}
	if after.events != before.events {
		t.Errorf("%d événements, attendu %d", after.events, before.events)
	}
	// Un refus n'écrit JAMAIS dans le graphe : ni pour se souvenir, ni pour « apprendre » la
	// paire. Le graphe n'a qu'un seul auteur, l'humain sous token admin.
	if after.edges != before.edges {
		t.Errorf("%d arêtes, attendu %d : un refus n'écrit jamais dans project_trust", after.edges, before.edges)
	}

	// Et le canal reste ouvrable dès que l'humain déclare la paire : le refus n'a rien cassé.
	trust(t, db, frnt, ops)
	opened := open(t, st, frnt, ops, "la même question, une fois la paire déclarée")
	if opened.Number != 1 {
		t.Errorf("numéro = %d après déclaration, attendu 1 : le refus n'avait consommé aucun numéro", opened.Number)
	}
}

// M2, second canal — un refus de confiance ne POSE AUCUN VERROU sur la ligne du destinataire.
//
// C'est la propriété qui empêche un repo non autorisé de faire un déni de service ciblé sur un
// tiers légitime : sans elle, il lui suffit d'ouvrir une transaction, de tenter une issue vers sa
// victime et de traîner, pour bloquer tout créateur légitime pendant ce temps. Mesuré à la
// conception : 1933 ms contre 73 ms.
//
// La sonde est FOR NO KEY UPDATE ... NOWAIT depuis une SECONDE connexion, pendant que la
// transaction du refus est encore ouverte. Elle est déterministe : soit le verrou est détenu et
// Postgres lève 55P03 immédiatement, soit il ne l'est pas et la sonde passe. Aucune assertion de
// latence — un test de latence en CI est rouge un jour sur trois, donc un test qu'on désactive.
func TestRefusedIssueLocksNothing(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)

	frnt := newProject(t, db, teamID, "FRNT")
	ops := newProject(t, db, teamID, "OPS")

	var refusal, probe error
	if err := st.WithTx(ctx, func(tx store.Store) error {
		_, refusal = tx.CreateIssue(ctx, store.NewIssue{
			TeamID:          teamID,
			AuthorProjectID: frnt.id,
			ToProjectKey:    ops.key,
			Title:           "tentative vers une paire non déclarée",
		})

		// Toujours DANS la transaction du refus : c'est le seul moment où le verrou existerait.
		// db est un pool, donc cette requête part sur une autre connexion que celle de la
		// transaction.
		var id uuid.UUID
		probe = db.QueryRow(
			"SELECT id FROM projects WHERE id = $1 FOR NO KEY UPDATE NOWAIT", ops.id,
		).Scan(&id)

		return nil
	}); err != nil {
		t.Fatalf("la transaction du refus n'a pas commité: %v", err)
	}

	if !errors.Is(refusal, store.ErrNotFound) {
		t.Fatalf("erreur = %v, attendu ErrNotFound", refusal)
	}
	if probe != nil {
		t.Errorf("la ligne d'OPS était verrouillée pendant le refus (%v) : un émetteur non autorisé "+
			"peut bloquer un créateur légitime en traînant dans sa transaction", probe)
	}
}
