package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-ia/internal/feature/task/service"
	"github.com/Coddyum/flowlio-ia/internal/feature/task/store"
	"github.com/google/uuid"
)

// fakeStore enregistre ce que le service lui demande, sans base de données. Ces tests portent
// sur la validation et sur les valeurs par défaut : l'isolation, elle, se prouve contre Postgres
// (voir store/store_integration_test.go), parce qu'elle est portée par les queries.
type fakeStore struct {
	claimed   int64
	lastTask  store.NewTask
	lastPatch store.TaskPatch
	lastFiler store.TaskFilter
	lastNote  string

	// noteLimit est la borne que le service a demandée au store pour le fil de notes. C'est le
	// seul endroit d'où on peut vérifier qu'il n'en réclame pas la totalité.
	noteLimit int32

	// archived reproduit la clause `archived_at IS NULL` que portent les VRAIES queries d'écriture.
	// Sans elle, ce double accepterait une note sur une tâche archivée là où Postgres la refuse,
	// et l'ordre des écritures dans UpdateTask ne serait vérifié par rien.
	archived bool

	// txCalls compte les ouvertures de transaction. C'est ce qui prouve qu'une écriture composée
	// en ouvre une, et qu'une écriture simple n'en paie pas le coût.
	txCalls int

	claimErr error
	writeErr error
	noteErr  error
}

func (f *fakeStore) WithTx(ctx context.Context, fn func(store.Store) error) error {
	f.txCalls++
	return fn(f)
}

func (f *fakeStore) ClaimNumber(context.Context, uuid.UUID, uuid.UUID) (int64, error) {
	if f.claimErr != nil {
		return 0, f.claimErr
	}
	f.claimed++
	return f.claimed, nil
}

func (f *fakeStore) CreateTask(_ context.Context, in store.NewTask) (store.Task, error) {
	if f.writeErr != nil {
		return store.Task{}, f.writeErr
	}
	f.lastTask = in
	return store.Task{
		Number:   in.Number,
		Title:    in.Title,
		Body:     in.Body,
		Status:   in.Status,
		Priority: in.Priority,
		Deadline: in.Deadline,
	}, nil
}

func (f *fakeStore) TaskByNumber(_ context.Context, _, _ uuid.UUID, number int64) (store.Task, error) {
	if f.writeErr != nil {
		return store.Task{}, f.writeErr
	}
	return store.Task{Number: number, Title: "tâche", Status: "todo", Priority: "normal"}, nil
}

func (f *fakeStore) ListTasks(_ context.Context, filter store.TaskFilter) ([]store.Task, error) {
	f.lastFiler = filter
	return []store.Task{{Number: 1, Title: "tâche", Body: "corps volumineux"}}, nil
}

func (f *fakeStore) UpdateTask(_ context.Context, patch store.TaskPatch) (store.Task, error) {
	if f.writeErr != nil {
		return store.Task{}, f.writeErr
	}
	if f.archived {
		return store.Task{}, store.ErrNotFound
	}
	f.lastPatch = patch
	if patch.Archive {
		f.archived = true
	}
	return store.Task{Number: patch.Number, Title: "tâche", Status: "todo", Priority: "normal"}, nil
}

func (f *fakeStore) AddNote(_ context.Context, _, _ uuid.UUID, _ int64, body string) (store.Note, error) {
	if f.noteErr != nil {
		return store.Note{}, f.noteErr
	}
	if f.writeErr != nil {
		return store.Note{}, f.writeErr
	}
	if f.archived {
		return store.Note{}, store.ErrNotFound
	}
	f.lastNote = body
	return store.Note{Body: body}, nil
}

// ListNotes enregistre la borne reçue : c'est elle qui prouve que le service ne demande PAS le
// fil entier, et le faux store est le seul endroit d'où on peut l'observer.
func (f *fakeStore) ListNotes(_ context.Context, _, _ uuid.UUID, _ int64, limit int32) ([]store.Note, int, error) {
	f.noteLimit = limit
	return []store.Note{{Body: "note"}}, 42, nil
}

// newService renvoie un service adossé au faux store, avec un scope de projet valide.
func newService() (service.Service, *fakeStore, uuid.UUID, uuid.UUID) {
	fake := &fakeStore{}
	return service.New(fake), fake, uuid.New(), uuid.New()
}

func TestCreateTaskValidation(t *testing.T) {
	teamID, projectID := uuid.New(), uuid.New()

	tests := []struct {
		name string
		in   service.CreateTaskInput
	}{
		{"titre vide", service.CreateTaskInput{TeamID: teamID, ProjectID: projectID}},
		{"titre en blancs", service.CreateTaskInput{TeamID: teamID, ProjectID: projectID, Title: "   "}},
		{
			"titre démesuré",
			service.CreateTaskInput{TeamID: teamID, ProjectID: projectID, Title: strings.Repeat("a", 201)},
		},
		{
			"statut inconnu",
			service.CreateTaskInput{TeamID: teamID, ProjectID: projectID, Title: "x", Status: "wontfix"},
		},
		{
			"priorité inconnue",
			service.CreateTaskInput{TeamID: teamID, ProjectID: projectID, Title: "x", Priority: "critique"},
		},
		{"team absente", service.CreateTaskInput{ProjectID: projectID, Title: "x"}},
		{"projet absent", service.CreateTaskInput{TeamID: teamID, Title: "x"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := service.New(&fakeStore{})
			if _, err := svc.CreateTask(context.Background(), tc.in); !errors.Is(err, service.ErrInvalidInput) {
				t.Errorf("erreur = %v, attendu ErrInvalidInput", err)
			}
		})
	}
}

// Un scope incomplet ne doit jamais atteindre le store : la query serait alors filtrée sur un
// UUID nul, ce qui ne protège plus rien.
func TestScopeIsRequiredEverywhere(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()

	tests := map[string]func(service.Service) error{
		"CreateTask": func(s service.Service) error {
			_, err := s.CreateTask(ctx, service.CreateTaskInput{Title: "x"})
			return err
		},
		"ListTasks": func(s service.Service) error {
			_, err := s.ListTasks(ctx, service.ListTasksInput{TeamID: id})
			return err
		},
		"GetTask": func(s service.Service) error {
			_, err := s.GetTask(ctx, id, uuid.Nil, 1)
			return err
		},
		"UpdateTask": func(s service.Service) error {
			_, err := s.UpdateTask(ctx, service.UpdateTaskInput{TeamID: id, Number: 1})
			return err
		},
		"UpdateTask avec note": func(s service.Service) error {
			note := "x"
			_, err := s.UpdateTask(ctx, service.UpdateTaskInput{ProjectID: id, Number: 1, Note: &note})
			return err
		},
		"UpdateTask (archive)": func(s service.Service) error {
			_, err := s.UpdateTask(ctx, service.UpdateTaskInput{ProjectID: id, Number: 1, Archive: true})
			return err
		},
	}

	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			if err := call(service.New(&fakeStore{})); !errors.Is(err, service.ErrInvalidInput) {
				t.Errorf("erreur = %v, attendu ErrInvalidInput", err)
			}
		})
	}
}

// Un agent qui ouvre une tâche sans préciser l'état veut le cas nominal.
func TestCreateTaskDefaults(t *testing.T) {
	svc, fake, teamID, projectID := newService()

	task, err := svc.CreateTask(context.Background(), service.CreateTaskInput{
		TeamID:    teamID,
		ProjectID: projectID,
		Title:     "  tâche avec des blancs  ",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if task.Status != "todo" || task.Priority != "normal" {
		t.Errorf("défauts = %s/%s, attendu todo/normal", task.Status, task.Priority)
	}
	if fake.lastTask.Title != "tâche avec des blancs" {
		t.Errorf("titre = %q, attendu sans blancs de bord", fake.lastTask.Title)
	}
	if fake.lastTask.Number != 1 {
		t.Errorf("numéro = %d, attendu 1 (réservé dans la transaction)", fake.lastTask.Number)
	}
}

// Le listing ne doit pas transporter la description : un agent qui parcourt son backlog paierait
// en contexte ce qu'il ne lit pas.
func TestListTasksOmitsBody(t *testing.T) {
	svc, fake, teamID, projectID := newService()

	tasks, err := svc.ListTasks(context.Background(), service.ListTasksInput{
		TeamID:    teamID,
		ProjectID: projectID,
	})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("%d tâches, attendu 1", len(tasks))
	}
	if tasks[0].Body != "" {
		t.Errorf("le listing transporte la description: %q", tasks[0].Body)
	}
	if fake.lastFiler.Limit != 50 {
		t.Errorf("limite par défaut = %d, attendu 50", fake.lastFiler.Limit)
	}
}

func TestListTasksLimitIsClamped(t *testing.T) {
	tests := []struct {
		name     string
		limit    int
		expected int32
	}{
		{"absente", 0, 50},
		{"négative", -3, 50},
		{"dans les bornes", 10, 10},
		{"au-dessus du maximum", 5000, 200},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake, teamID, projectID := newService()
			if _, err := svc.ListTasks(context.Background(), service.ListTasksInput{
				TeamID:    teamID,
				ProjectID: projectID,
				Limit:     tc.limit,
			}); err != nil {
				t.Fatalf("ListTasks: %v", err)
			}
			if fake.lastFiler.Limit != tc.expected {
				t.Errorf("limite = %d, attendu %d", fake.lastFiler.Limit, tc.expected)
			}
		})
	}
}

// Un champ absent du patch reste nil jusqu'au store : c'est ce qui garantit qu'une mise à jour
// partielle n'écrase pas ce qu'elle ne mentionne pas.
func TestUpdateTaskPatchIsPartial(t *testing.T) {
	svc, fake, teamID, projectID := newService()

	status := "done"
	if _, err := svc.UpdateTask(context.Background(), service.UpdateTaskInput{
		TeamID:    teamID,
		ProjectID: projectID,
		Number:    7,
		Status:    &status,
	}); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	if fake.lastPatch.Title != nil || fake.lastPatch.Body != nil || fake.lastPatch.Priority != nil {
		t.Errorf("des champs absents du patch ont été transmis: %+v", fake.lastPatch)
	}
	if fake.lastPatch.Status == nil || *fake.lastPatch.Status != "done" {
		t.Errorf("statut transmis = %v, attendu done", fake.lastPatch.Status)
	}
	if fake.lastPatch.Number != 7 {
		t.Errorf("numéro transmis = %d, attendu 7", fake.lastPatch.Number)
	}
}

func TestUpdateTaskValidation(t *testing.T) {
	teamID, projectID := uuid.New(), uuid.New()
	blank := "  "
	unknownStatus := "archived"
	unknownPriority := "p0"

	tests := map[string]service.UpdateTaskInput{
		"titre vide":        {TeamID: teamID, ProjectID: projectID, Number: 1, Title: &blank},
		"statut inconnu":    {TeamID: teamID, ProjectID: projectID, Number: 1, Status: &unknownStatus},
		"priorité inconnue": {TeamID: teamID, ProjectID: projectID, Number: 1, Priority: &unknownPriority},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			svc := service.New(&fakeStore{})
			if _, err := svc.UpdateTask(context.Background(), in); !errors.Is(err, service.ErrInvalidInput) {
				t.Errorf("erreur = %v, attendu ErrInvalidInput", err)
			}
		})
	}
}

// Une note explicitement vide est une erreur, pas un no-op : l'agent croirait avoir laissé une
// trace là où la session suivante ne lira rien. Un champ ABSENT, lui, veut dire « pas de note ».
func TestUpdateTaskRejectsEmptyNote(t *testing.T) {
	svc, _, teamID, projectID := newService()

	for _, body := range []string{"", "   ", "\n\t "} {
		note := body
		if _, err := svc.UpdateTask(context.Background(), service.UpdateTaskInput{
			TeamID:    teamID,
			ProjectID: projectID,
			Number:    1,
			Note:      &note,
		}); !errors.Is(err, service.ErrInvalidInput) {
			t.Errorf("note %q: erreur = %v, attendu ErrInvalidInput", body, err)
		}
	}
}

// « Passer en done et dire pourquoi » est UNE écriture. Si le patch et la note partaient
// séparément, l'état « statut changé, motif perdu » resterait atteignable : la note tombe alors
// que le done est déjà en base, et la session suivante lit un done que rien n'explique.
//
// MUTATION : remplacer le WithTx d'update_task.go par deux appels directs au store fait tomber
// ce test sur txCalls == 0.
func TestUpdateTaskWritesNoteInTheSameTransaction(t *testing.T) {
	svc, fake, teamID, projectID := newService()

	status, note := "done", "  migration appliquée, reste la doc  "
	task, err := svc.UpdateTask(context.Background(), service.UpdateTaskInput{
		TeamID:    teamID,
		ProjectID: projectID,
		Number:    7,
		Status:    &status,
		Note:      &note,
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	if fake.txCalls != 1 {
		t.Errorf("%d transaction(s) ouverte(s), attendu 1 : le patch et la note doivent être atomiques",
			fake.txCalls)
	}
	if fake.lastPatch.Status == nil || *fake.lastPatch.Status != "done" {
		t.Errorf("statut transmis = %v, attendu done", fake.lastPatch.Status)
	}
	if fake.lastNote != "migration appliquée, reste la doc" {
		t.Errorf("note transmise = %q, attendue sans blancs de bord", fake.lastNote)
	}
	if task.Number != 7 {
		t.Errorf("tâche renvoyée = #%d, attendu #7 : c'est le patch qui est renvoyé, pas la note",
			task.Number)
	}
}

// Le cas fréquent — un patch sans note — ne doit pas payer l'aller-retour d'une transaction.
func TestUpdateTaskWithoutNoteDoesNotOpenTransaction(t *testing.T) {
	svc, fake, teamID, projectID := newService()

	status := "in_progress"
	if _, err := svc.UpdateTask(context.Background(), service.UpdateTaskInput{
		TeamID:    teamID,
		ProjectID: projectID,
		Number:    3,
		Status:    &status,
	}); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	if fake.txCalls != 0 {
		t.Errorf("%d transaction(s) pour un patch sans note, attendu 0", fake.txCalls)
	}
	if fake.lastNote != "" {
		t.Errorf("note écrite sans qu'on en demande une: %q", fake.lastNote)
	}
}

// Une note qui échoue doit faire échouer TOUT l'appel : un patch appliqué seul rendrait le
// « et dire pourquoi » facultatif à l'insu de l'appelant.
func TestUpdateTaskFailsWholeWhenNoteFails(t *testing.T) {
	fake := &fakeStore{noteErr: store.ErrNotFound}
	svc := service.New(fake)

	note := "trace"
	status := "done"
	_, err := svc.UpdateTask(context.Background(), service.UpdateTaskInput{
		TeamID:    uuid.New(),
		ProjectID: uuid.New(),
		Number:    1,
		Status:    &status,
		Note:      &note,
	})
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("erreur = %v, attendu ErrNotFound remontée par la note", err)
	}
	if fake.txCalls != 1 {
		t.Errorf("%d transaction(s), attendu 1 — c'est elle qui annule le patch", fake.txCalls)
	}
}

// Une absence en base doit remonter en ErrNotFound domaine, pas en erreur interne : le handler
// en dépend pour répondre 404 plutôt que 500.
func TestStoreErrorsAreTranslated(t *testing.T) {
	fake := &fakeStore{writeErr: store.ErrNotFound}
	svc := service.New(fake)
	teamID, projectID := uuid.New(), uuid.New()

	if _, err := svc.GetTask(context.Background(), teamID, projectID, 42); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("GetTask: erreur = %v, attendu ErrNotFound", err)
	}

	fake.writeErr = store.ErrConflict
	if _, err := svc.UpdateTask(context.Background(), service.UpdateTaskInput{
		TeamID: teamID, ProjectID: projectID, Number: 42, Archive: true,
	}); !errors.Is(err, service.ErrConflict) {
		t.Errorf("UpdateTask(archive): erreur = %v, attendu ErrConflict", err)
	}
}

// Une échéance dont l'année sort de [0, 9999] doit être refusée à l'entrée.
//
// Sans cette barrière, la tâche s'insère très bien puis rend illisible le listing du projet
// entier : time.Time refuse d'encoder une telle année, et l'encodage a lieu APRÈS l'écriture en
// base. Le serveur répondait 200 avec un corps vide, et un agent en concluait « backlog vide ».
func TestDeadlineYearIsBounded(t *testing.T) {
	teamID, projectID := uuid.New(), uuid.New()

	// La charge exacte qui reproduisait le défaut : l'année écrite est 9999, mais le décalage de
	// fuseau la fait basculer en l'an 10000 une fois ramenée en UTC.
	overflow := time.Date(9999, 12, 31, 23, 30, 0, 0, time.FixedZone("test", -5*60*60))
	if overflow.UTC().Year() != 10000 {
		t.Fatalf("la charge de test ne déborde plus : année UTC = %d", overflow.UTC().Year())
	}

	svc := service.New(&fakeStore{})
	if _, err := svc.CreateTask(context.Background(), service.CreateTaskInput{
		TeamID: teamID, ProjectID: projectID, Title: "x", Deadline: &overflow,
	}); !errors.Is(err, service.ErrInvalidInput) {
		t.Errorf("CreateTask: erreur = %v, attendu ErrInvalidInput", err)
	}

	if _, err := svc.UpdateTask(context.Background(), service.UpdateTaskInput{
		TeamID: teamID, ProjectID: projectID, Number: 1, Deadline: &overflow,
	}); !errors.Is(err, service.ErrInvalidInput) {
		t.Errorf("UpdateTask: erreur = %v, attendu ErrInvalidInput", err)
	}

	// Une échéance ordinaire reste acceptée, et ce qui est accepté doit être sérialisable :
	// c'est l'invariant réel que la validation protège.
	sane := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	task, err := svc.CreateTask(context.Background(), service.CreateTaskInput{
		TeamID: teamID, ProjectID: projectID, Title: "x", Deadline: &sane,
	})
	if err != nil {
		t.Fatalf("échéance ordinaire refusée: %v", err)
	}
	if _, err := json.Marshal(task); err != nil {
		t.Errorf("tâche acceptée mais non sérialisable: %v", err)
	}
}

// GetTask demande au store une FENÊTRE, pas le fil entier, et dit à l'agent ce qu'il ne lit pas.
//
// La borne doit être portée par la query : un `[:10]` en Go aurait quand même tiré 62,6 Mio
// depuis Postgres, ce qui est exactement le coût qu'on refuse. Le seul endroit d'où on peut
// vérifier que le service ne réclame pas tout, c'est ce que le store a reçu.
//
// MUTATION : passer 0 (ou rien) comme borne à store.ListNotes fait tomber ce test.
func TestGetTaskAsksForABoundedThread(t *testing.T) {
	svc, fake, teamID, projectID := newService()

	detail, err := svc.GetTask(context.Background(), teamID, projectID, 1)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	if fake.noteLimit <= 0 {
		t.Errorf("borne demandée au store = %d : le service réclame le fil entier", fake.noteLimit)
	}
	if fake.noteLimit > 50 {
		t.Errorf("borne demandée au store = %d : trop large pour un contexte d'agent", fake.noteLimit)
	}
	if detail.NotesTotal != 42 {
		t.Errorf("notes_total = %d, attendu 42 : l'agent ne sait pas qu'il ne lit qu'une fenêtre",
			detail.NotesTotal)
	}
}

// « Passe en done, voilà pourquoi, et archive » est UNE écriture, et la note s'écrit d'abord.
//
// L'ordre n'est pas indifférent depuis que l'archivage est un champ du patch : patcher d'abord
// ferme la tâche, et l'écriture de la note — dont la query porte `archived_at IS NULL` — est
// refusée derrière. L'appel le plus courant d'une fin de session échouerait entièrement.
//
// MUTATION : remettre `tx.UpdateTask` avant `tx.AddNote` fait tomber ce test.
func TestEndOfTaskWritesTheNoteBeforeArchiving(t *testing.T) {
	svc, fake, teamID, projectID := newService()

	done := "done"
	note := "livré"
	task, err := svc.UpdateTask(context.Background(), service.UpdateTaskInput{
		TeamID: teamID, ProjectID: projectID, Number: 1,
		Status: &done, Note: &note, Archive: true,
	})
	if err != nil {
		t.Fatalf("fin de tâche: %v", err)
	}
	if task.Number != 1 {
		t.Errorf("numéro = %d, attendu 1", task.Number)
	}
	if fake.lastNote != "livré" {
		t.Errorf("note écrite = %q, attendu « livré »", fake.lastNote)
	}
	if !fake.lastPatch.Archive {
		t.Error("le patch ne porte pas l'archivage : c'est un second aller-retour qui revient")
	}
	if fake.txCalls != 1 {
		t.Errorf("%d transactions, attendu 1 : les deux écritures doivent tenir ensemble", fake.txCalls)
	}
}
