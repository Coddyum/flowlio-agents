package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

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

	claimErr error
	writeErr error
}

func (f *fakeStore) WithTx(ctx context.Context, fn func(store.Store) error) error {
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
	f.lastPatch = patch
	return store.Task{Number: patch.Number, Title: "tâche", Status: "todo", Priority: "normal"}, nil
}

func (f *fakeStore) ArchiveTask(_ context.Context, _, _ uuid.UUID, number int64) (store.Task, error) {
	if f.writeErr != nil {
		return store.Task{}, f.writeErr
	}
	return store.Task{Number: number}, nil
}

func (f *fakeStore) AddNote(_ context.Context, _, _ uuid.UUID, _ int64, body string) (store.Note, error) {
	if f.writeErr != nil {
		return store.Note{}, f.writeErr
	}
	f.lastNote = body
	return store.Note{Body: body}, nil
}

func (f *fakeStore) ListNotes(context.Context, uuid.UUID, uuid.UUID, int64) ([]store.Note, error) {
	return []store.Note{{Body: "note"}}, nil
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
		"AddNote": func(s service.Service) error {
			_, err := s.AddNote(ctx, service.AddNoteInput{ProjectID: id, Number: 1, Body: "x"})
			return err
		},
		"ArchiveTask": func(s service.Service) error {
			_, err := s.ArchiveTask(ctx, uuid.Nil, id, 1)
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

func TestAddNoteRejectsEmptyBody(t *testing.T) {
	svc, _, teamID, projectID := newService()

	for _, body := range []string{"", "   ", "\n\t "} {
		if _, err := svc.AddNote(context.Background(), service.AddNoteInput{
			TeamID:    teamID,
			ProjectID: projectID,
			Number:    1,
			Body:      body,
		}); !errors.Is(err, service.ErrInvalidInput) {
			t.Errorf("note %q: erreur = %v, attendu ErrInvalidInput", body, err)
		}
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
	if _, err := svc.ArchiveTask(context.Background(), teamID, projectID, 42); !errors.Is(err, service.ErrConflict) {
		t.Errorf("ArchiveTask: erreur = %v, attendu ErrConflict", err)
	}
}
