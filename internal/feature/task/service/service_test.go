package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
	"github.com/google/uuid"
)

// fakeStore records what the service asks of it, without a database. These tests are about
// validation and default values: isolation itself is proven against Postgres (see
// store/store_integration_test.go), because it is carried by the queries.
type fakeStore struct {
	claimed   int64
	lastTask  store.NewTask
	lastPatch store.TaskPatch
	lastFiler store.TaskFilter
	lastNote  string

	// noteLimit is the bound the service asked the store for on the note thread. It is the only
	// place from which one can check that it does not claim the whole of it.
	noteLimit int32

	// archived reproduces the `archived_at IS NULL` clause the REAL write queries carry. Without
	// it, this double would accept a note on an archived task where Postgres refuses it, and
	// nothing would check the write order inside UpdateTask.
	archived bool

	// txCalls counts transaction openings. That is what proves a composed write opens one, and that
	// a simple write does not pay its cost.
	txCalls int

	// Blocking. This double stays DELIBERATELY dumb: the rule for going back to `todo` — every edge
	// released AND at least one having set the block — lives in the ClearTaskBlock query, and
	// replaying it here would prove the reimplementation, not the product. It is checked against
	// Postgres (store/dependency_integration_test.go). What gets tested here is what the SERVICE
	// decides on its own: the refusals, and what it passes on to the store.
	statusByNumber  map[int64]string
	archivedNumbers map[int64]bool
	activeEdges     []store.Edge
	lastDependency  store.NewDependency
	events          []store.Event
	cleared         []uuid.UUID
	releasedPairs   int

	claimErr error
	writeErr error
	noteErr  error
}

// taskID gives a stable identifier per number: without stability, two reads of the same task inside
// a transaction would name two objects, and the self-blocking refusal would go through.
func (f *fakeStore) taskID(number int64) uuid.UUID {
	return uuid.NewSHA1(uuid.Nil, []byte(strconv.FormatInt(number, 10)))
}

func (f *fakeStore) CreateDependency(_ context.Context, in store.NewDependency) (store.Dependency, error) {
	if f.writeErr != nil {
		return store.Dependency{}, f.writeErr
	}
	f.lastDependency = in
	return store.Dependency{
		TaskID:        in.TaskID,
		BlockerTaskID: in.BlockerTaskID,
		UntilStatus:   in.UntilStatus,
		SetBlocked:    in.SetBlocked,
	}, nil
}

func (f *fakeStore) ReleaseBlockerEdges(_ context.Context, _, blockerTaskID uuid.UUID, _ string, _ bool) ([]uuid.UUID, error) {
	freed := make([]uuid.UUID, 0, len(f.activeEdges))
	for _, edge := range f.activeEdges {
		if edge.BlockerTaskID == blockerTaskID {
			freed = append(freed, edge.TaskID)
		}
	}
	return freed, nil
}

func (f *fakeStore) ReleaseEdge(_ context.Context, _, taskID, blockerTaskID uuid.UUID) ([]uuid.UUID, error) {
	for _, edge := range f.activeEdges {
		if edge.TaskID == taskID && edge.BlockerTaskID == blockerTaskID {
			f.releasedPairs++
			return []uuid.UUID{taskID}, nil
		}
	}
	return nil, nil
}

func (f *fakeStore) ClearBlock(_ context.Context, _, _, taskID uuid.UUID) (bool, error) {
	f.cleared = append(f.cleared, taskID)
	return true, nil
}

func (f *fakeStore) ActiveEdges(context.Context, uuid.UUID) ([]store.Edge, error) {
	return f.activeEdges, nil
}

func (f *fakeStore) AppendEvent(_ context.Context, event store.Event) error {
	f.events = append(f.events, event)
	return nil
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

	status := "todo"
	if s, ok := f.statusByNumber[number]; ok {
		status = s
	}
	task := store.Task{
		ID:       f.taskID(number),
		Number:   number,
		Title:    "task",
		Status:   status,
		Priority: "normal",
	}
	if f.archivedNumbers[number] {
		archivedAt := time.Unix(0, 0)
		task.ArchivedAt = &archivedAt
	}
	return task, nil
}

func (f *fakeStore) ListTasks(_ context.Context, filter store.TaskFilter) ([]store.Task, error) {
	f.lastFiler = filter
	return []store.Task{{Number: 1, Title: "task", Body: "bulky body"}}, nil
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
	return store.Task{Number: patch.Number, Title: "task", Status: "todo", Priority: "normal"}, nil
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

// ListNotes records the bound it received: that is what proves the service does NOT ask for the
// whole thread, and the fake store is the only place from which it can be observed.
func (f *fakeStore) ListNotes(_ context.Context, _, _ uuid.UUID, _ int64, limit int32) ([]store.Note, int, error) {
	f.noteLimit = limit
	return []store.Note{{Body: "note"}}, 42, nil
}

// newService returns a service backed by the fake store, with a valid project scope.
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
		{"empty title", service.CreateTaskInput{TeamID: teamID, ProjectID: projectID}},
		{"whitespace title", service.CreateTaskInput{TeamID: teamID, ProjectID: projectID, Title: "   "}},
		{
			"oversized title",
			service.CreateTaskInput{TeamID: teamID, ProjectID: projectID, Title: strings.Repeat("a", 201)},
		},
		{
			"unknown status",
			service.CreateTaskInput{TeamID: teamID, ProjectID: projectID, Title: "x", Status: "wontfix"},
		},
		{
			"unknown priority",
			service.CreateTaskInput{TeamID: teamID, ProjectID: projectID, Title: "x", Priority: "critical"},
		},
		{"missing team", service.CreateTaskInput{ProjectID: projectID, Title: "x"}},
		{"missing project", service.CreateTaskInput{TeamID: teamID, Title: "x"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := service.New(&fakeStore{})
			if _, err := svc.CreateTask(context.Background(), tc.in); !errors.Is(err, service.ErrInvalidInput) {
				t.Errorf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// An incomplete scope must never reach the store: the query would then be filtered on a nil UUID,
// which protects nothing any more.
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
		"UpdateTask with a note": func(s service.Service) error {
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
				t.Errorf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// An agent opening a task without naming a state wants the nominal case.
func TestCreateTaskDefaults(t *testing.T) {
	svc, fake, teamID, projectID := newService()

	task, err := svc.CreateTask(context.Background(), service.CreateTaskInput{
		TeamID:    teamID,
		ProjectID: projectID,
		Title:     "  task with whitespace  ",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if task.Status != "todo" || task.Priority != "normal" {
		t.Errorf("defaults = %s/%s, want todo/normal", task.Status, task.Priority)
	}
	if fake.lastTask.Title != "task with whitespace" {
		t.Errorf("title = %q, want it trimmed", fake.lastTask.Title)
	}
	if fake.lastTask.Number != 1 {
		t.Errorf("number = %d, want 1 (reserved inside the transaction)", fake.lastTask.Number)
	}
}

// The listing must not carry the description: an agent scanning its backlog would pay in context
// for what it does not read.
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
		t.Fatalf("%d tasks, want 1", len(tasks))
	}
	if tasks[0].Body != "" {
		t.Errorf("the listing carries the description: %q", tasks[0].Body)
	}
	if fake.lastFiler.Limit != 50 {
		t.Errorf("default limit = %d, want 50", fake.lastFiler.Limit)
	}
}

func TestListTasksLimitIsClamped(t *testing.T) {
	tests := []struct {
		name     string
		limit    int
		expected int32
	}{
		{"missing", 0, 50},
		{"negative", -3, 50},
		{"within bounds", 10, 10},
		{"above the maximum", 5000, 200},
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
				t.Errorf("limit = %d, want %d", fake.lastFiler.Limit, tc.expected)
			}
		})
	}
}

// A field absent from the patch stays nil all the way to the store: that is what guarantees a
// partial update does not overwrite what it never mentions.
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
		t.Errorf("fields absent from the patch were passed on: %+v", fake.lastPatch)
	}
	if fake.lastPatch.Status == nil || *fake.lastPatch.Status != "done" {
		t.Errorf("status passed on = %v, want done", fake.lastPatch.Status)
	}
	if fake.lastPatch.Number != 7 {
		t.Errorf("number passed on = %d, want 7", fake.lastPatch.Number)
	}
}

func TestUpdateTaskValidation(t *testing.T) {
	teamID, projectID := uuid.New(), uuid.New()
	blank := "  "
	unknownStatus := "archived"
	unknownPriority := "p0"

	tests := map[string]service.UpdateTaskInput{
		"empty title":      {TeamID: teamID, ProjectID: projectID, Number: 1, Title: &blank},
		"unknown status":   {TeamID: teamID, ProjectID: projectID, Number: 1, Status: &unknownStatus},
		"unknown priority": {TeamID: teamID, ProjectID: projectID, Number: 1, Priority: &unknownPriority},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			svc := service.New(&fakeStore{})
			if _, err := svc.UpdateTask(context.Background(), in); !errors.Is(err, service.ErrInvalidInput) {
				t.Errorf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// An explicitly empty note is an error, not a no-op: the agent would believe it left a trace where
// the next session reads nothing. An ABSENT field, on the other hand, means "no note".
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
			t.Errorf("note %q: error = %v, want ErrInvalidInput", body, err)
		}
	}
}

// "Move to done and say why" is ONE write. Were the patch and the note to leave separately, the
// state "status changed, reason lost" would stay reachable: the note fails while the done is
// already in the database, and the next session reads a done that nothing explains.
//
// MUTATION: replacing the WithTx of update_task.go with two direct store calls makes this test fail
// on txCalls == 0.
func TestUpdateTaskWritesNoteInTheSameTransaction(t *testing.T) {
	svc, fake, teamID, projectID := newService()

	status, note := "done", "  migration applied, docs still to do  "
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
		t.Errorf("%d transaction(s) opened, want 1: the patch and the note must be atomic",
			fake.txCalls)
	}
	if fake.lastPatch.Status == nil || *fake.lastPatch.Status != "done" {
		t.Errorf("status passed on = %v, want done", fake.lastPatch.Status)
	}
	if fake.lastNote != "migration applied, docs still to do" {
		t.Errorf("note passed on = %q, want it trimmed", fake.lastNote)
	}
	if task.Number != 7 {
		t.Errorf("task returned = #%d, want #7: it is the patch that is returned, not the note",
			task.Number)
	}
}

// The frequent case — a patch composing nothing — must not pay for a transaction round trip.
//
// The example was `status: in_progress` until blocking edges came along: that status now RELEASES,
// hence it composes. The nominal patch is the one touching neither the note thread, nor archiving,
// nor a release status.
func TestUpdateTaskWithoutCompositionDoesNotOpenTransaction(t *testing.T) {
	svc, fake, teamID, projectID := newService()

	priority := "urgent"
	if _, err := svc.UpdateTask(context.Background(), service.UpdateTaskInput{
		TeamID:    teamID,
		ProjectID: projectID,
		Number:    3,
		Priority:  &priority,
	}); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	if fake.txCalls != 0 {
		t.Errorf("%d transaction(s) for a simple patch, want 0", fake.txCalls)
	}
	if fake.lastNote != "" {
		t.Errorf("a note was written without one being asked for: %q", fake.lastNote)
	}
}

// The counterpart, and it is the one that matters: a patch able to release an edge MUST open a
// transaction. Outside a transaction, the defect would be the one this feature exists to remove —
// the blocker committed `done`, and the blocked task ignoring it forever.
func TestUpdateTaskOpensTransactionWhenItCanRelease(t *testing.T) {
	releasing := []struct {
		name  string
		patch service.UpdateTaskInput
	}{
		{"move to in_progress", service.UpdateTaskInput{Status: ptr("in_progress")}},
		{"move to done", service.UpdateTaskInput{Status: ptr("done")}},
		{"archiving", service.UpdateTaskInput{Archive: true}},
	}

	for _, tc := range releasing {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake, teamID, projectID := newService()

			in := tc.patch
			in.TeamID, in.ProjectID, in.Number = teamID, projectID, 3
			if _, err := svc.UpdateTask(context.Background(), in); err != nil {
				t.Fatalf("UpdateTask: %v", err)
			}
			if fake.txCalls != 1 {
				t.Errorf("%d transaction(s), want 1: the release must be written with the patch",
					fake.txCalls)
			}
		})
	}
}

// ptr returns the address of a string literal, which partial patches demand everywhere.
func ptr(s string) *string { return &s }

// A failing note must fail the WHOLE call: a patch applied on its own would make the "and say why"
// optional without the caller knowing.
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
		t.Fatalf("error = %v, want the ErrNotFound raised by the note", err)
	}
	if fake.txCalls != 1 {
		t.Errorf("%d transaction(s), want 1 — it is what rolls the patch back", fake.txCalls)
	}
}

// A missing row must surface as a domain ErrNotFound, not as an internal error: the handler relies
// on it to answer 404 rather than 500.
func TestStoreErrorsAreTranslated(t *testing.T) {
	fake := &fakeStore{writeErr: store.ErrNotFound}
	svc := service.New(fake)
	teamID, projectID := uuid.New(), uuid.New()

	if _, err := svc.GetTask(context.Background(), teamID, projectID, 42); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("GetTask: error = %v, want ErrNotFound", err)
	}

	fake.writeErr = store.ErrConflict
	if _, err := svc.UpdateTask(context.Background(), service.UpdateTaskInput{
		TeamID: teamID, ProjectID: projectID, Number: 42, Archive: true,
	}); !errors.Is(err, service.ErrConflict) {
		t.Errorf("UpdateTask(archive): error = %v, want ErrConflict", err)
	}
}

// A deadline whose year falls outside [0, 9999] must be refused at the door.
//
// Without this barrier, the task inserts just fine then makes the entire project listing
// unreadable: time.Time refuses to encode such a year, and the encoding happens AFTER the write to
// the database. The server answered 200 with an empty body, and an agent concluded "empty backlog".
func TestDeadlineYearIsBounded(t *testing.T) {
	teamID, projectID := uuid.New(), uuid.New()

	// The exact payload that reproduced the defect: the year written is 9999, but the time zone
	// offset tips it into year 10000 once brought back to UTC.
	overflow := time.Date(9999, 12, 31, 23, 30, 0, 0, time.FixedZone("test", -5*60*60))
	if overflow.UTC().Year() != 10000 {
		t.Fatalf("the test payload no longer overflows: UTC year = %d", overflow.UTC().Year())
	}

	svc := service.New(&fakeStore{})
	if _, err := svc.CreateTask(context.Background(), service.CreateTaskInput{
		TeamID: teamID, ProjectID: projectID, Title: "x", Deadline: &overflow,
	}); !errors.Is(err, service.ErrInvalidInput) {
		t.Errorf("CreateTask: error = %v, want ErrInvalidInput", err)
	}

	if _, err := svc.UpdateTask(context.Background(), service.UpdateTaskInput{
		TeamID: teamID, ProjectID: projectID, Number: 1, Deadline: &overflow,
	}); !errors.Is(err, service.ErrInvalidInput) {
		t.Errorf("UpdateTask: error = %v, want ErrInvalidInput", err)
	}

	// An ordinary deadline stays accepted, and what is accepted must be serialisable: that is the
	// real invariant the validation protects.
	sane := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	task, err := svc.CreateTask(context.Background(), service.CreateTaskInput{
		TeamID: teamID, ProjectID: projectID, Title: "x", Deadline: &sane,
	})
	if err != nil {
		t.Fatalf("ordinary deadline refused: %v", err)
	}
	if _, err := json.Marshal(task); err != nil {
		t.Errorf("task accepted but not serialisable: %v", err)
	}
}

// GetTask asks the store for a WINDOW, not the whole thread, and tells the agent what it is not
// reading.
//
// The bound has to be carried by the query: a `[:10]` in Go would still have pulled 62.6 MiB out of
// Postgres, which is exactly the cost being refused. The only place from which one can check the
// service does not claim everything is what the store received.
//
// MUTATION: passing 0 (or nothing) as the bound to store.ListNotes makes this test fail.
func TestGetTaskAsksForABoundedThread(t *testing.T) {
	svc, fake, teamID, projectID := newService()

	detail, err := svc.GetTask(context.Background(), teamID, projectID, 1)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	if fake.noteLimit <= 0 {
		t.Errorf("bound asked of the store = %d: the service claims the whole thread", fake.noteLimit)
	}
	if fake.noteLimit > 50 {
		t.Errorf("bound asked of the store = %d: too wide for an agent context", fake.noteLimit)
	}
	if detail.NotesTotal != 42 {
		t.Errorf("notes_total = %d, want 42: the agent does not know it is only reading a window",
			detail.NotesTotal)
	}
}

// "Move to done, here is why, and archive" is ONE write, and the note is written first.
//
// The order stopped being indifferent once archiving became a field of the patch: patching first
// closes the task, and writing the note — whose query carries `archived_at IS NULL` — is refused
// behind it. The most common end-of-session call would fail entirely.
//
// MUTATION: putting `tx.UpdateTask` back before `tx.AddNote` makes this test fail.
func TestEndOfTaskWritesTheNoteBeforeArchiving(t *testing.T) {
	svc, fake, teamID, projectID := newService()

	done := "done"
	note := "delivered"
	task, err := svc.UpdateTask(context.Background(), service.UpdateTaskInput{
		TeamID: teamID, ProjectID: projectID, Number: 1,
		Status: &done, Note: &note, Archive: true,
	})
	if err != nil {
		t.Fatalf("end of task: %v", err)
	}
	if task.Number != 1 {
		t.Errorf("number = %d, want 1", task.Number)
	}
	if fake.lastNote != "delivered" {
		t.Errorf("note written = %q, want \"delivered\"", fake.lastNote)
	}
	if !fake.lastPatch.Archive {
		t.Error("the patch does not carry the archiving: that is a second round trip coming back")
	}
	if fake.txCalls != 1 {
		t.Errorf("%d transactions, want 1: both writes must hold together", fake.txCalls)
	}
}
