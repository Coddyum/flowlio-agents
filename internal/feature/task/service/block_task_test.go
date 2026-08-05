package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
	"github.com/google/uuid"
)

// Ce que le SERVICE refuse tout seul, avant que la base ait son mot à dire.
//
// Ces refus ne remplacent pas les contraintes — l'auto-blocage a son CHECK, le doublon son index
// unique partiel, la dépendance inter-repos sa clé étrangère composite. Ils existent pour rendre un
// MOTIF : un agent qui lit `pq: violates constraint task_dependencies_not_self` ne sait pas quoi
// corriger, alors qu'il sait quoi faire d'une phrase.
func TestBlockTaskRefusals(t *testing.T) {
	teamID, projectID := uuid.New(), uuid.New()
	base := service.BlockTaskInput{TeamID: teamID, ProjectID: projectID, Number: 55, Blocker: 56}

	tests := []struct {
		name  string
		setup func(*fakeStore)
		in    service.BlockTaskInput
	}{
		{
			name: "une tâche ne se bloque pas elle-même",
			in:   service.BlockTaskInput{TeamID: teamID, ProjectID: projectID, Number: 55, Blocker: 55},
		},
		{
			name: "condition de libération hors vocabulaire",
			in:   service.BlockTaskInput{TeamID: teamID, ProjectID: projectID, Number: 55, Blocker: 56, Until: "archived"},
		},
		{
			// `todo` et `blocked` sont refusés pour la même raison : ce ne sont pas des progrès.
			// Une arête qui les attend naîtrait libérée, ou ne le serait jamais.
			name: "condition de libération qui n'est pas un progrès",
			in:   service.BlockTaskInput{TeamID: teamID, ProjectID: projectID, Number: 55, Blocker: 56, Until: "todo"},
		},
		{
			name:  "la bloquée est archivée",
			setup: func(f *fakeStore) { f.archivedNumbers = map[int64]bool{55: true} },
			in:    base,
		},
		{
			// Archivée, elle n'atteindra jamais rien : l'arête serait impossible à libérer
			// autrement qu'à la main, et la tâche resterait bloquée sans que rien ne le dise.
			name:  "la bloquante est archivée",
			setup: func(f *fakeStore) { f.archivedNumbers = map[int64]bool{56: true} },
			in:    base,
		},
		{
			// Une arête née libérée est un blocage qui ne bloque pas : la tâche passerait `blocked`
			// sans que rien ne soit jamais journalisé pour l'en sortir.
			name:  "la bloquante est déjà done",
			setup: func(f *fakeStore) { f.statusByNumber = map[int64]string{56: "done"} },
			in:    base,
		},
		{
			name:  "la bloquante est déjà in_progress et c'est ce qu'on attendait",
			setup: func(f *fakeStore) { f.statusByNumber = map[int64]string{56: "in_progress"} },
			in:    service.BlockTaskInput{TeamID: teamID, ProjectID: projectID, Number: 55, Blocker: 56, Until: "in_progress"},
		},
		{
			name: "scope incomplet",
			in:   service.BlockTaskInput{ProjectID: projectID, Number: 55, Blocker: 56},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeStore{}
			if tc.setup != nil {
				tc.setup(fake)
			}
			svc := service.New(fake)

			if _, err := svc.BlockTask(context.Background(), tc.in); !errors.Is(err, service.ErrInvalidInput) {
				t.Fatalf("erreur = %v, attendu ErrInvalidInput", err)
			}
			if fake.lastDependency != (store.NewDependency{}) {
				t.Error("une arête a été écrite malgré le refus")
			}
		})
	}
}

// Le cycle. A bloque B qui bloque A laisserait les deux `blocked` pour toujours, sans que rien ne
// le dise — c'est le contraire exact de ce que cette feature promet.
//
// Le parcours est une fonction pure appelée sur le graphe actif du projet, donc cette garantie
// tient sans base de données : elle ne peut pas être perdue par un environnement de test.
func TestBlockTaskRefusesCycles(t *testing.T) {
	teamID, projectID := uuid.New(), uuid.New()
	fake := &fakeStore{}

	// 56 est déjà bloquée par 55. Bloquer 55 sur 56 refermerait la boucle.
	fake.activeEdges = []store.Edge{{TaskID: fake.taskID(56), BlockerTaskID: fake.taskID(55)}}

	svc := service.New(fake)
	_, err := svc.BlockTask(context.Background(), service.BlockTaskInput{
		TeamID: teamID, ProjectID: projectID, Number: 55, Blocker: 56,
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("erreur = %v, attendu ErrInvalidInput", err)
	}
	if fake.lastDependency != (store.NewDependency{}) {
		t.Error("l'arête du cycle a été écrite")
	}
}

// Le cycle INDIRECT : 55 → 56 → 57, et on tente 57 → 55. Un contrôle qui ne regarderait que les
// voisins immédiats laisserait passer celui-là, et c'est exactement la forme qu'un graphe de
// dépendances prend au bout de trois cartes.
func TestBlockTaskRefusesIndirectCycles(t *testing.T) {
	teamID, projectID := uuid.New(), uuid.New()
	fake := &fakeStore{}
	fake.activeEdges = []store.Edge{
		{TaskID: fake.taskID(55), BlockerTaskID: fake.taskID(56)},
		{TaskID: fake.taskID(56), BlockerTaskID: fake.taskID(57)},
	}

	svc := service.New(fake)
	_, err := svc.BlockTask(context.Background(), service.BlockTaskInput{
		TeamID: teamID, ProjectID: projectID, Number: 57, Blocker: 55,
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("erreur = %v, attendu ErrInvalidInput sur un cycle à trois", err)
	}
}

// La contrepartie : une chaîne qui ne boucle pas doit passer. Un contrôle de cycle qui refuse tout
// ce qui a un ancêtre commun serait vert au test précédent et inutilisable en vrai.
func TestBlockTaskAcceptsDiamond(t *testing.T) {
	teamID, projectID := uuid.New(), uuid.New()
	fake := &fakeStore{}
	// 55 et 56 dépendent toutes deux de 57. Ajouter 55 → 56 ne ferme rien.
	fake.activeEdges = []store.Edge{
		{TaskID: fake.taskID(55), BlockerTaskID: fake.taskID(57)},
		{TaskID: fake.taskID(56), BlockerTaskID: fake.taskID(57)},
	}

	svc := service.New(fake)
	if _, err := svc.BlockTask(context.Background(), service.BlockTaskInput{
		TeamID: teamID, ProjectID: projectID, Number: 55, Blocker: 56,
	}); err != nil {
		t.Fatalf("BlockTask sur un losange: %v", err)
	}
	if fake.lastDependency.BlockerTaskID != fake.taskID(56) {
		t.Errorf("arête écrite vers %v, attendu la tâche 56", fake.lastDependency.BlockerTaskID)
	}
}

// set_blocked est le champ que le premier refactor voudra supprimer. Ce test dit pourquoi il ne
// doit pas : c'est la SEULE trace de qui a posé le blocage, et elle n'est calculable qu'au moment
// de l'écriture — après, « bloquée par l'arête » et « bloquée par un agent pour une autre raison »
// sont indiscernables.
func TestBlockTaskRecordsWhoBlocked(t *testing.T) {
	teamID, projectID := uuid.New(), uuid.New()

	tests := []struct {
		name           string
		blockedStatus  string
		wantSetBlocked bool
		wantPatched    bool
	}{
		{"la tâche était libre : c'est cette arête qui la bloque", "todo", true, true},
		{"la tâche était déjà bloquée : l'arête ne s'en attribue pas le mérite", "blocked", false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeStore{statusByNumber: map[int64]string{55: tc.blockedStatus}}
			svc := service.New(fake)

			if _, err := svc.BlockTask(context.Background(), service.BlockTaskInput{
				TeamID: teamID, ProjectID: projectID, Number: 55, Blocker: 56,
			}); err != nil {
				t.Fatalf("BlockTask: %v", err)
			}

			if fake.lastDependency.SetBlocked != tc.wantSetBlocked {
				t.Errorf("set_blocked = %v, attendu %v", fake.lastDependency.SetBlocked, tc.wantSetBlocked)
			}
			patched := fake.lastPatch.Status != nil
			if patched != tc.wantPatched {
				t.Errorf("statut patché = %v, attendu %v", patched, tc.wantPatched)
			}
			if patched && *fake.lastPatch.Status != "blocked" {
				t.Errorf("statut patché = %q, attendu blocked", *fake.lastPatch.Status)
			}
		})
	}
}

// La condition par défaut est `done`, et elle doit atteindre le store telle quelle : une arête
// écrite sans condition serait une arête que la base remplirait à sa place, donc une règle de plus
// à aller lire ailleurs.
func TestBlockTaskDefaultsToDone(t *testing.T) {
	fake := &fakeStore{}
	svc := service.New(fake)

	if _, err := svc.BlockTask(context.Background(), service.BlockTaskInput{
		TeamID: uuid.New(), ProjectID: uuid.New(), Number: 55, Blocker: 56,
	}); err != nil {
		t.Fatalf("BlockTask: %v", err)
	}
	if fake.lastDependency.UntilStatus != "done" {
		t.Errorf("until = %q, attendu done", fake.lastDependency.UntilStatus)
	}
}

// Rejouer unblock_task sur une arête déjà libérée ne doit pas échouer : un agent qui a perdu son
// contexte et rejoue ne fait pas de faute, et refuser casserait une reprise de session sur une
// action déjà faite.
func TestUnblockTaskIsReplayable(t *testing.T) {
	fake := &fakeStore{}
	svc := service.New(fake)

	task, err := svc.UnblockTask(context.Background(), service.UnblockTaskInput{
		TeamID: uuid.New(), ProjectID: uuid.New(), Number: 55, Blocker: 56,
	})
	if err != nil {
		t.Fatalf("UnblockTask sur une arête absente: %v", err)
	}
	if task.Number != 55 {
		t.Errorf("tâche renvoyée = #%d, attendu #55", task.Number)
	}
	if len(fake.events) != 0 {
		t.Errorf("%d événement(s) pour un déblocage sans effet, attendu 0", len(fake.events))
	}
}

// Un déblocage réel journalise, et le sujet est la tâche DÉBLOQUÉE — pas la bloquante. C'est ce
// que check_inbox rendra au projet : mettre la bloquante en sujet ferait remonter l'événement sur
// la mauvaise carte.
func TestUnblockTaskAnnouncesOnTheFreedTask(t *testing.T) {
	fake := &fakeStore{}
	fake.activeEdges = []store.Edge{{TaskID: fake.taskID(55), BlockerTaskID: fake.taskID(56)}}
	svc := service.New(fake)

	if _, err := svc.UnblockTask(context.Background(), service.UnblockTaskInput{
		TeamID: uuid.New(), ProjectID: uuid.New(), Number: 55, Blocker: 56,
	}); err != nil {
		t.Fatalf("UnblockTask: %v", err)
	}

	if len(fake.events) != 1 {
		t.Fatalf("%d événement(s), attendu 1", len(fake.events))
	}
	if fake.events[0].Kind != "task.unblocked" {
		t.Errorf("kind = %q, attendu task.unblocked", fake.events[0].Kind)
	}
	if fake.events[0].SubjectID != fake.taskID(55) {
		t.Error("le sujet de l'événement n'est pas la tâche débloquée")
	}
}
