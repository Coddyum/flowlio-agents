package service_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// projectScope est la paire de tenancy d'un projet de test : ce qu'un token de projet porte.
type projectScope struct {
	teamID    uuid.UUID
	projectID uuid.UUID
}

// newRealService monte le service sur la VRAIE base. Les doubles en mémoire prouvent ce que le
// service décide seul ; ce fichier prouve la chaîne entière — service, store, queries, contraintes
// — parce que la règle de libération n'existe nulle part en Go : elle est dans le SQL.
func newRealService(t *testing.T) (service.Service, *sql.DB) {
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

	return service.New(store.New(database.New(db), db)), db
}

// newRealProject crée une team et un projet jetables par SQL direct. Les fixtures n'empruntent
// aucune autre feature : la suppression de la team emporte le reste en cascade.
func newRealProject(t *testing.T, db *sql.DB, key string) projectScope {
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

	var projectID uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
		teamID, key, "Projet de test",
	).Scan(&projectID); err != nil {
		t.Fatalf("création du projet %s: %v", key, err)
	}

	return projectScope{teamID: teamID, projectID: projectID}
}

// openTask ouvre une tâche par le chemin nominal du service.
func openTask(t *testing.T, svc service.Service, sc projectScope, title string) service.Task {
	t.Helper()

	task, err := svc.CreateTask(context.Background(), service.CreateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Title: title,
	})
	if err != nil {
		t.Fatalf("création de %q: %v", title, err)
	}
	return task
}

// unblockedEvents compte les `task.unblocked` journalisés sur une tâche donnée.
func unblockedEvents(t *testing.T, db *sql.DB, sc projectScope, number int64) int {
	t.Helper()

	var count int
	err := db.QueryRow(`
		SELECT count(*) FROM events e
		JOIN tasks t ON t.id = e.subject_id
		WHERE e.kind = 'task.unblocked'
		  AND e.subject_type = 'task'
		  AND t.project_id = $1
		  AND t.number = $2`, sc.projectID, number).Scan(&count)
	if err != nil {
		t.Fatalf("comptage des événements: %v", err)
	}
	return count
}

// LE critère de la carte, de bout en bout : block_task, puis passage de la bloquante à `done`,
// donne une bloquée qui repasse `todo` ET un `task.unblocked` dans le journal.
//
// Les deux moitiés comptent. Sans le retour à `todo`, l'agent lit un blocage que rien ne lève ;
// sans l'événement, check_inbox n'a rien à lui rendre et la tâche ne dit toujours rien — ce qui
// était le manque d'origine.
func TestBlockThenDoneReleasesAndAnnounces(t *testing.T) {
	svc, db := newRealService(t)
	ctx := context.Background()
	sc := newRealProject(t, db, "E2E")

	blocked := openTask(t, svc, sc, "attend la migration")
	blocker := openTask(t, svc, sc, "la migration")

	after, err := svc.BlockTask(ctx, service.BlockTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID,
		Number: blocked.Number, Blocker: blocker.Number,
	})
	if err != nil {
		t.Fatalf("BlockTask: %v", err)
	}
	if after.Status != "blocked" {
		t.Fatalf("statut après blocage = %q, attendu blocked", after.Status)
	}

	done := "done"
	if _, err := svc.UpdateTask(ctx, service.UpdateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: blocker.Number, Status: &done,
	}); err != nil {
		t.Fatalf("passage de la bloquante en done: %v", err)
	}

	released, err := svc.GetTask(ctx, sc.teamID, sc.projectID, blocked.Number)
	if err != nil {
		t.Fatalf("relecture de la bloquée: %v", err)
	}
	if released.Status != "todo" {
		t.Errorf("statut après libération = %q, attendu todo", released.Status)
	}
	if n := unblockedEvents(t, db, sc, blocked.Number); n != 1 {
		t.Errorf("%d événement(s) task.unblocked, attendu 1", n)
	}
}

// L'autre moitié du critère « seulement si l'arête l'avait bloquée » : une tâche que l'agent avait
// mise en `blocked` LUI-MÊME garde son statut, et est quand même notifiée.
//
// Notifier et décider sont deux gestes distincts, et un seul est automatisé. Confondre les deux
// écraserait une information humaine par une déduction.
func TestReleaseNotifiesWithoutOverridingAnAgentsBlock(t *testing.T) {
	svc, db := newRealService(t)
	ctx := context.Background()
	sc := newRealProject(t, db, "KEEP")

	blocked := openTask(t, svc, sc, "bloquée pour une autre raison")
	blocker := openTask(t, svc, sc, "la migration")

	// L'agent bloque d'abord la tâche à la main : l'arête ouverte ensuite ne s'attribuera pas
	// le blocage, donc sa libération ne le lèvera pas.
	manual := "blocked"
	if _, err := svc.UpdateTask(ctx, service.UpdateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: blocked.Number, Status: &manual,
	}); err != nil {
		t.Fatalf("blocage manuel: %v", err)
	}
	if _, err := svc.BlockTask(ctx, service.BlockTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID,
		Number: blocked.Number, Blocker: blocker.Number,
	}); err != nil {
		t.Fatalf("BlockTask: %v", err)
	}

	done := "done"
	if _, err := svc.UpdateTask(ctx, service.UpdateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: blocker.Number, Status: &done,
	}); err != nil {
		t.Fatalf("passage de la bloquante en done: %v", err)
	}

	after, err := svc.GetTask(ctx, sc.teamID, sc.projectID, blocked.Number)
	if err != nil {
		t.Fatalf("relecture: %v", err)
	}
	if after.Status != "blocked" {
		t.Errorf("statut = %q, attendu blocked : la décision de l'agent ne s'écrase pas", after.Status)
	}
	if n := unblockedEvents(t, db, sc, blocked.Number); n != 1 {
		t.Errorf("%d événement(s), attendu 1 : on notifie même quand on ne décide pas", n)
	}
}

// Archiver une bloquante libère ses arêtes. Archivée, elle n'atteindra jamais `done` : sans cette
// règle, on fabriquerait des tâches que plus rien ne peut débloquer.
func TestArchivingABlockerReleasesItsEdges(t *testing.T) {
	svc, db := newRealService(t)
	ctx := context.Background()
	sc := newRealProject(t, db, "ARCH")

	blocked := openTask(t, svc, sc, "attend une tâche qui va disparaître")
	blocker := openTask(t, svc, sc, "abandonnée")

	if _, err := svc.BlockTask(ctx, service.BlockTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID,
		Number: blocked.Number, Blocker: blocker.Number,
	}); err != nil {
		t.Fatalf("BlockTask: %v", err)
	}

	if _, err := svc.UpdateTask(ctx, service.UpdateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: blocker.Number, Archive: true,
	}); err != nil {
		t.Fatalf("archivage de la bloquante: %v", err)
	}

	after, err := svc.GetTask(ctx, sc.teamID, sc.projectID, blocked.Number)
	if err != nil {
		t.Fatalf("relecture: %v", err)
	}
	if after.Status != "todo" {
		t.Errorf("statut = %q, attendu todo : une bloquante archivée n'atteindra jamais done", after.Status)
	}
	if n := unblockedEvents(t, db, sc, blocked.Number); n != 1 {
		t.Errorf("%d événement(s), attendu 1", n)
	}
}

// Deux bloquantes : la première qui tombe ne libère rien. Le retour à `todo` demande que TOUTES les
// arêtes soient levées — être libéré d'un obstacle sur deux, c'est encore être bloqué.
func TestPartialReleaseKeepsTheTaskBlocked(t *testing.T) {
	svc, db := newRealService(t)
	ctx := context.Background()
	sc := newRealProject(t, db, "PART")

	blocked := openTask(t, svc, sc, "attend deux choses")
	first := openTask(t, svc, sc, "première")
	second := openTask(t, svc, sc, "seconde")

	for _, blocker := range []service.Task{first, second} {
		if _, err := svc.BlockTask(ctx, service.BlockTaskInput{
			TeamID: sc.teamID, ProjectID: sc.projectID,
			Number: blocked.Number, Blocker: blocker.Number,
		}); err != nil {
			t.Fatalf("BlockTask sur #%d: %v", blocker.Number, err)
		}
	}

	done := "done"
	if _, err := svc.UpdateTask(ctx, service.UpdateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: first.Number, Status: &done,
	}); err != nil {
		t.Fatalf("première bloquante en done: %v", err)
	}

	half, err := svc.GetTask(ctx, sc.teamID, sc.projectID, blocked.Number)
	if err != nil {
		t.Fatalf("relecture intermédiaire: %v", err)
	}
	if half.Status != "blocked" {
		t.Fatalf("statut = %q, attendu blocked : une arête subsiste", half.Status)
	}

	if _, err := svc.UpdateTask(ctx, service.UpdateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: second.Number, Status: &done,
	}); err != nil {
		t.Fatalf("seconde bloquante en done: %v", err)
	}

	full, err := svc.GetTask(ctx, sc.teamID, sc.projectID, blocked.Number)
	if err != nil {
		t.Fatalf("relecture finale: %v", err)
	}
	if full.Status != "todo" {
		t.Errorf("statut = %q, attendu todo une fois les deux arêtes levées", full.Status)
	}
}

// Une clé d'un autre projet n'a aucun chemin jusqu'ici : le service ne résout que des NUMÉROS de
// son propre projet, et un numéro qui n'y existe pas est introuvable. C'est le pendant, côté
// service, de ce que la contrainte composite garantit en base.
func TestBlockTaskCannotNameAnotherProjectsTask(t *testing.T) {
	svc, db := newRealService(t)
	ctx := context.Background()
	sc := newRealProject(t, db, "MINE")

	blocked := openTask(t, svc, sc, "la mienne")

	// Un projet voisin dans la MÊME team, avec sa propre tâche numéro 1.
	var siblingID uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
		sc.teamID, "OTHR", "Projet voisin",
	).Scan(&siblingID); err != nil {
		t.Fatalf("création du projet voisin: %v", err)
	}
	if _, err := svc.CreateTask(ctx, service.CreateTaskInput{
		TeamID: sc.teamID, ProjectID: siblingID, Title: "la sienne",
	}); err != nil {
		t.Fatalf("tâche du voisin: %v", err)
	}

	// Le numéro 999 n'existe dans aucun des deux : le service ne peut pas le résoudre, et il
	// n'existe aucune forme d'entrée capable de désigner le projet voisin.
	_, err := svc.BlockTask(ctx, service.BlockTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: blocked.Number, Blocker: 999,
	})
	if err == nil {
		t.Fatal("BlockTask a accepté une bloquante inexistante")
	}
}
