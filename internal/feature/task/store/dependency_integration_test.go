package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
)

// LA garantie intra-repo, prouvée là où elle vit : en base.
//
// Ce test appelle le store DIRECTEMENT, en contournant le service. C'est tout son intérêt — la
// carte demande qu'une arête vers un autre projet soit refusée « en base, pas seulement dans le
// service » (D42), et une garde qui n'existe que dans le service tombe au premier chemin
// d'écriture ajouté à côté d'elle.
//
// Ce qui refuse n'est pas un prédicat mais la FORME de la table : les deux clés étrangères
// composites de task_dependencies partagent la même colonne project_id. La dépendance inter-repos
// n'est pas interdite, elle est inexprimable.
func TestDependencyCannotCrossProjects(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	mine := newProject(t, db, "OWN")
	sibling := newProjectIn(t, db, mine.teamID, "SIB")

	blocked := createTask(t, st, mine, "ce que je dois faire")
	foreign := createTask(t, st, sibling, "ce que le voisin doit faire")

	_, err := st.CreateDependency(ctx, store.NewDependency{
		TeamID:        mine.teamID,
		ProjectID:     mine.projectID,
		TaskID:        blocked.ID,
		BlockerTaskID: foreign.ID,
		UntilStatus:   "done",
		SetBlocked:    true,
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("erreur = %v, attendu ErrConflict : la base doit refuser une arête inter-projets", err)
	}

	// Et rien n'a été écrit : un refus qui laisserait une ligne serait pire qu'une absence de refus.
	var count int
	if err := db.QueryRow(
		"SELECT count(*) FROM task_dependencies WHERE task_id = $1", blocked.ID,
	).Scan(&count); err != nil {
		t.Fatalf("comptage des arêtes: %v", err)
	}
	if count != 0 {
		t.Errorf("%d arête(s) écrite(s) malgré le refus", count)
	}
}

// Une tâche ne peut pas se bloquer elle-même, et c'est le CHECK qui le dit. Le service rend le
// motif lisible ; la contrainte est ce qui tient si un jour un autre chemin d'écriture apparaît.
func TestDependencyCannotBeSelfReferential(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	sc := newProject(t, db, "SELF")
	task := createTask(t, st, sc, "moi-même")

	_, err := st.CreateDependency(ctx, store.NewDependency{
		TeamID:        sc.teamID,
		ProjectID:     sc.projectID,
		TaskID:        task.ID,
		BlockerTaskID: task.ID,
		UntilStatus:   "done",
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("erreur = %v, attendu ErrConflict sur l'auto-blocage", err)
	}
}

// Une arête ACTIVE est unique par couple : rejouer block_task ne fabrique pas un second blocage à
// libérer. Une fois libérée, en revanche, le même couple redevient ouvrable — l'unicité est
// partielle pour cette raison exacte, sinon débloquer puis rebloquer serait refusé pour toujours.
func TestDependencyPairIsUniqueWhileActiveOnly(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	sc := newProject(t, db, "PAIR")
	blocked := createTask(t, st, sc, "bloquée")
	blocker := createTask(t, st, sc, "bloquante")

	edge := store.NewDependency{
		TeamID:        sc.teamID,
		ProjectID:     sc.projectID,
		TaskID:        blocked.ID,
		BlockerTaskID: blocker.ID,
		UntilStatus:   "done",
		SetBlocked:    true,
	}

	if _, err := st.CreateDependency(ctx, edge); err != nil {
		t.Fatalf("première arête: %v", err)
	}
	if _, err := st.CreateDependency(ctx, edge); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("erreur = %v, attendu ErrConflict sur le doublon actif", err)
	}

	if _, err := st.ReleaseEdge(ctx, sc.projectID, blocked.ID, blocker.ID); err != nil {
		t.Fatalf("libération: %v", err)
	}
	if _, err := st.CreateDependency(ctx, edge); err != nil {
		t.Fatalf("réouverture après libération: %v — l'unicité doit être partielle", err)
	}
}

// La règle de retour à `todo`, prouvée là où elle vit : dans la query ClearTaskBlock. Les trois
// conditions y tiennent ensemble pour qu'aucun appelant ne puisse en oublier une, donc c'est ici
// qu'il faut les vérifier — pas dans un double en mémoire, qui prouverait la réimplémentation.
func TestClearBlockObeysItsThreeConditions(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		// setBlocked dit si l'arête est celle qui a posé le blocage.
		setBlocked bool
		// pendingEdge ajoute une seconde arête, non libérée.
		pendingEdge bool
		// status est celui de la tâche bloquée au moment de la libération.
		status string
		want   bool
	}{
		{
			name:       "l'arête avait bloqué, plus rien ne subsiste : retour à todo",
			setBlocked: true,
			status:     "blocked",
			want:       true,
		},
		{
			// Le cas que set_blocked existe pour distinguer. Sans lui, on écraserait une décision
			// humaine par une déduction.
			name:       "l'agent avait bloqué pour une autre raison : le statut ne bouge pas",
			setBlocked: false,
			status:     "blocked",
			want:       false,
		},
		{
			name:        "une autre arête bloque encore : rien ne bouge",
			setBlocked:  true,
			pendingEdge: true,
			status:      "blocked",
			want:        false,
		},
		{
			// Un agent qui a déjà repris la tâche à la main ne doit pas être renvoyé à `todo` par
			// une libération arrivée après coup.
			name:       "la tâche n'est plus bloquée : on ne la ramène pas en arrière",
			setBlocked: true,
			status:     "in_progress",
			want:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, db := newStore(t)
			sc := newProject(t, db, "CLR")

			blocked := createTask(t, st, sc, "bloquée")
			blocker := createTask(t, st, sc, "bloquante")

			if _, err := st.CreateDependency(ctx, store.NewDependency{
				TeamID: sc.teamID, ProjectID: sc.projectID,
				TaskID: blocked.ID, BlockerTaskID: blocker.ID,
				UntilStatus: "done", SetBlocked: tc.setBlocked,
			}); err != nil {
				t.Fatalf("arête principale: %v", err)
			}

			if tc.pendingEdge {
				other := createTask(t, st, sc, "autre bloquante")
				if _, err := st.CreateDependency(ctx, store.NewDependency{
					TeamID: sc.teamID, ProjectID: sc.projectID,
					TaskID: blocked.ID, BlockerTaskID: other.ID,
					UntilStatus: "done", SetBlocked: false,
				}); err != nil {
					t.Fatalf("arête secondaire: %v", err)
				}
			}

			status := tc.status
			if _, err := st.UpdateTask(ctx, store.TaskPatch{
				TeamID: sc.teamID, ProjectID: sc.projectID, Number: blocked.Number, Status: &status,
			}); err != nil {
				t.Fatalf("statut initial de la bloquée: %v", err)
			}

			// La bloquante atteint `done`, ce qui libère l'arête principale.
			done := "done"
			if _, err := st.UpdateTask(ctx, store.TaskPatch{
				TeamID: sc.teamID, ProjectID: sc.projectID, Number: blocker.Number, Status: &done,
			}); err != nil {
				t.Fatalf("bloquante en done: %v", err)
			}
			freed, err := st.ReleaseBlockerEdges(ctx, sc.projectID, blocker.ID, "done", false)
			if err != nil {
				t.Fatalf("libération: %v", err)
			}
			if len(freed) != 1 {
				t.Fatalf("%d arête(s) libérée(s), attendu 1", len(freed))
			}

			cleared, err := st.ClearBlock(ctx, sc.teamID, sc.projectID, blocked.ID)
			if err != nil {
				t.Fatalf("ClearBlock: %v", err)
			}
			if cleared != tc.want {
				t.Fatalf("retour à todo = %v, attendu %v", cleared, tc.want)
			}

			after, err := st.TaskByNumber(ctx, sc.teamID, sc.projectID, blocked.Number)
			if err != nil {
				t.Fatalf("relecture: %v", err)
			}
			wantStatus := tc.status
			if tc.want {
				wantStatus = "todo"
			}
			if after.Status != wantStatus {
				t.Errorf("statut = %q, attendu %q", after.Status, wantStatus)
			}
		})
	}
}

// « Atteindre » un statut est monotone et non une égalité : une bloquante qui saute de `todo` à
// `done` doit libérer aussi les arêtes qui n'attendaient que `in_progress`.
//
// L'égalité stricte est le piège naturel de cette query, et elle fabriquerait des arêtes que plus
// rien ne peut libérer — la tâche morte-vivante que cette feature existe pour empêcher.
func TestReleaseIsMonotone(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	sc := newProject(t, db, "MONO")
	waitsStart := createTask(t, st, sc, "attend le démarrage")
	waitsEnd := createTask(t, st, sc, "attend la fin")
	blocker := createTask(t, st, sc, "bloquante")

	for _, edge := range []struct {
		task  store.Task
		until string
	}{
		{waitsStart, "in_progress"},
		{waitsEnd, "done"},
	} {
		if _, err := st.CreateDependency(ctx, store.NewDependency{
			TeamID: sc.teamID, ProjectID: sc.projectID,
			TaskID: edge.task.ID, BlockerTaskID: blocker.ID,
			UntilStatus: edge.until, SetBlocked: true,
		}); err != nil {
			t.Fatalf("arête %s: %v", edge.until, err)
		}
	}

	// La bloquante passe directement en `done`, sans jamais avoir été `in_progress`.
	freed, err := st.ReleaseBlockerEdges(ctx, sc.projectID, blocker.ID, "done", false)
	if err != nil {
		t.Fatalf("libération: %v", err)
	}
	if len(freed) != 2 {
		t.Fatalf("%d arête(s) libérée(s), attendu 2 : `done` a dépassé `in_progress`", len(freed))
	}
}

// L'inverse, qui borne la règle précédente : atteindre `in_progress` ne libère PAS ce qui attendait
// `done`. Sans cette borne, « bloquée jusqu'à ce que ce soit fini » voudrait dire « jusqu'à ce que
// ce soit commencé », et la promesse de la feature serait fausse dans le cas le plus courant.
func TestReleaseDoesNotOvershoot(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	sc := newProject(t, db, "OVER")
	blocked := createTask(t, st, sc, "attend la fin")
	blocker := createTask(t, st, sc, "bloquante")

	if _, err := st.CreateDependency(ctx, store.NewDependency{
		TeamID: sc.teamID, ProjectID: sc.projectID,
		TaskID: blocked.ID, BlockerTaskID: blocker.ID,
		UntilStatus: "done", SetBlocked: true,
	}); err != nil {
		t.Fatalf("arête: %v", err)
	}

	freed, err := st.ReleaseBlockerEdges(ctx, sc.projectID, blocker.ID, "in_progress", false)
	if err != nil {
		t.Fatalf("libération: %v", err)
	}
	if len(freed) != 0 {
		t.Fatalf("%d arête(s) libérée(s) sur `in_progress`, attendu 0", len(freed))
	}
}
