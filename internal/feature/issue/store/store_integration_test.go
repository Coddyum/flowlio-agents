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
	foreign := newProject(t, db, teamB, "CORE")

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
func TestSelfIssueIsRejectedByTheDatabase(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)
	web := newProject(t, db, teamID, "WEB")

	err := st.WithTx(ctx, func(tx store.Store) error {
		_, err := tx.CreateIssue(ctx, store.NewIssue{
			TeamID:          teamID,
			AuthorProjectID: web.id,
			ToProjectKey:    "WEB",
			Title:           "je me parle à moi-même",
		})
		return err
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("issue vers soi-même: erreur = %v, attendu ErrConflict (contrainte issues_not_self)", err)
	}
}

func TestListIssuesFiltersByRoleAndState(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	teamID := newTeam(t, db)
	web := newProject(t, db, teamID, "WEB")
	core := newProject(t, db, teamID, "CORE")

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
