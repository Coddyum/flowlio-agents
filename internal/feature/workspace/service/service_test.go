package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/Coddyum/flowlio-agents/internal/pkg/crypto"
	"github.com/google/uuid"
)

// fakeStore keeps in memory what the service hands it, so as to check what is actually persisted —
// in particular that a secret never is.
type fakeStore struct {
	store.Store

	teams    map[string]store.Team
	projects map[string]store.Project
	tokens   []store.Token

	lastHash   string
	lastPrefix string
	failWith   error

	// deletion is what DeleteProject reports back, and deleteErr what it fails with. Both are
	// settable because the three outcomes of a delete (removed, refused, absent) are three
	// different answers from the store, and a fake that could only produce one of them would leave
	// the other two untested.
	deletion  store.ProjectDeletion
	deleteErr error

	// deletedTeam records which team DeleteTeam was asked to remove, and deleteTeamErr what it
	// answers. The recorded identifier is the assertion that matters: a delete has no return value
	// to inspect, so "the right team was deleted" can only be read off what the store received.
	deletedTeam   uuid.UUID
	deleteTeamErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		teams:    make(map[string]store.Team),
		projects: make(map[string]store.Project),
	}
}

func (f *fakeStore) CreateTeam(_ context.Context, slug, name string) (store.Team, error) {
	if f.failWith != nil {
		return store.Team{}, f.failWith
	}
	if _, exists := f.teams[slug]; exists {
		return store.Team{}, store.ErrConflict
	}
	team := store.Team{ID: uuid.New(), Slug: slug, Name: name}
	f.teams[slug] = team
	return team, nil
}

func (f *fakeStore) DeleteTeam(_ context.Context, id uuid.UUID) error {
	if f.deleteTeamErr != nil {
		return f.deleteTeamErr
	}
	f.deletedTeam = id
	return nil
}

func (f *fakeStore) CreateProject(_ context.Context, teamID uuid.UUID, key, name string) (store.Project, error) {
	if f.failWith != nil {
		return store.Project{}, f.failWith
	}
	project := store.Project{ID: uuid.New(), TeamID: teamID, Key: key, Name: name}
	f.projects[key] = project
	return project, nil
}

func (f *fakeStore) DeleteProject(_ context.Context, _ uuid.UUID, _ uuid.UUID) (store.ProjectDeletion, error) {
	if f.deleteErr != nil {
		return store.ProjectDeletion{}, f.deleteErr
	}
	return f.deletion, nil
}

func (f *fakeStore) ProjectByKey(_ context.Context, _ uuid.UUID, key string) (store.Project, error) {
	project, ok := f.projects[key]
	if !ok {
		return store.Project{}, store.ErrNotFound
	}
	return project, nil
}

func (f *fakeStore) CreateToken(_ context.Context, teamID, projectID uuid.UUID, name, prefix, hash string) (store.Token, error) {
	f.lastHash, f.lastPrefix = hash, prefix
	token := store.Token{
		ID: uuid.New(), TeamID: teamID, ProjectID: projectID,
		Name: name, Prefix: prefix,
	}
	f.tokens = append(f.tokens, token)
	return token, nil
}

func TestCreateTeamValidation(t *testing.T) {
	cases := []struct {
		name     string
		in       CreateTeamInput
		wantErr  error
		wantSlug string
	}{
		{name: "valid slug and name", in: CreateTeamInput{Slug: "omiros", Name: "Omiros"}, wantSlug: "omiros"},
		{name: "slug normalised to lowercase", in: CreateTeamInput{Slug: "  OMIROS ", Name: "Omiros"}, wantSlug: "omiros"},
		{name: "empty slug", in: CreateTeamInput{Slug: "", Name: "Omiros"}, wantErr: ErrInvalidInput},
		{name: "slug with a space", in: CreateTeamInput{Slug: "omi ros", Name: "Omiros"}, wantErr: ErrInvalidInput},
		{name: "slug with an underscore", in: CreateTeamInput{Slug: "omi_ros", Name: "O"}, wantErr: ErrInvalidInput},
		{name: "empty name", in: CreateTeamInput{Slug: "omiros", Name: "   "}, wantErr: ErrInvalidInput},
		{name: "name too long", in: CreateTeamInput{Slug: "omiros", Name: strings.Repeat("a", 201)}, wantErr: ErrInvalidInput},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(newFakeStore())

			team, err := svc.CreateTeam(context.Background(), tc.in)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if team.Slug != tc.wantSlug {
				t.Errorf("slug = %q, want %q", team.Slug, tc.wantSlug)
			}
		})
	}
}

func TestCreateTeamConflict(t *testing.T) {
	svc := New(newFakeStore())
	in := CreateTeamInput{Slug: "omiros", Name: "Omiros"}

	if _, err := svc.CreateTeam(context.Background(), in); err != nil {
		t.Fatalf("first creation: %v", err)
	}

	_, err := svc.CreateTeam(context.Background(), in)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
}

func TestCreateProjectValidation(t *testing.T) {
	teamID := uuid.New()

	cases := []struct {
		name    string
		in      CreateProjectInput
		wantKey string
		wantErr error
	}{
		{name: "valid key", in: CreateProjectInput{TeamID: teamID, Key: "CORE", Name: "omiros-core"}, wantKey: "CORE"},
		{name: "key normalised to uppercase", in: CreateProjectInput{TeamID: teamID, Key: "core", Name: "omiros-core"}, wantKey: "CORE"},
		{name: "single-character key", in: CreateProjectInput{TeamID: teamID, Key: "C", Name: "x"}, wantErr: ErrInvalidInput},
		{name: "key starting with a digit", in: CreateProjectInput{TeamID: teamID, Key: "1CORE", Name: "x"}, wantErr: ErrInvalidInput},
		{name: "key too long", in: CreateProjectInput{TeamID: teamID, Key: "WAYTOOLONGKEY", Name: "x"}, wantErr: ErrInvalidInput},
		{name: "key with a dash", in: CreateProjectInput{TeamID: teamID, Key: "CO-RE", Name: "x"}, wantErr: ErrInvalidInput},
		{name: "missing team", in: CreateProjectInput{Key: "CORE", Name: "x"}, wantErr: ErrInvalidInput},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(newFakeStore())

			project, err := svc.CreateProject(context.Background(), tc.in)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if project.Key != tc.wantKey {
				t.Errorf("key = %q, want %q", project.Key, tc.wantKey)
			}
		})
	}
}

// The secret must never reach the persistence layer: only its hash goes down there.
func TestCreateTokenNeverPersistsTheSecret(t *testing.T) {
	fake := newFakeStore()
	svc := New(fake)
	teamID := uuid.New()

	if _, err := svc.CreateProject(context.Background(), CreateProjectInput{
		TeamID: teamID, Key: "CORE", Name: "omiros-core",
	}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	created, err := svc.CreateToken(context.Background(), CreateTokenInput{
		TeamID: teamID, ProjectKey: "core", Name: "claude",
	})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	if created.Secret == "" {
		t.Fatal("the secret must be returned at creation")
	}
	if fake.lastHash == created.Secret {
		t.Fatal("the secret was persisted as is")
	}
	if strings.Contains(fake.lastHash, created.Secret) {
		t.Fatal("the persisted hash contains the secret")
	}

	_, secret, err := crypto.ParseToken(created.Secret)
	if err != nil {
		t.Fatalf("the secret returned must be a valid token: %v", err)
	}
	if !crypto.VerifySecret(secret, fake.lastHash) {
		t.Error("the persisted hash does not validate the secret issued")
	}
	if created.Prefix != fake.lastPrefix {
		t.Errorf("prefix returned = %q, persisted = %q", created.Prefix, fake.lastPrefix)
	}
}

func TestCreateTokenOnUnknownProject(t *testing.T) {
	svc := New(newFakeStore())

	_, err := svc.CreateToken(context.Background(), CreateTokenInput{
		TeamID: uuid.New(), ProjectKey: "GHOST", Name: "claude",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

// --------------------------------------------------------------------------------------------
// PART 2 OF FLWL-70 — `ListTeams` carries a tenancy scope.
//
// It is the SERVICE that has to be observed here, and not only the handler. A handler test proves
// the scope is passed on; it says nothing about what is done with it, and a service dropping the
// argument would leave the handler test green while every team of the installation kept being
// read. That gap was real: it is what these two tests close.
// --------------------------------------------------------------------------------------------

// teamStore answers both team reads and records which one the service used.
type teamStore struct {
	store.Store

	all    []store.Team
	byID   map[uuid.UUID]store.Team
	listed bool
	gotID  uuid.UUID
}

func (f *teamStore) ListTeams(context.Context) ([]store.Team, error) {
	f.listed = true
	return f.all, nil
}

func (f *teamStore) TeamByID(_ context.Context, id uuid.UUID) (store.Team, error) {
	f.gotID = id
	team, ok := f.byID[id]
	if !ok {
		return store.Team{}, store.ErrNotFound
	}
	return team, nil
}

func newTeamStore() (*teamStore, store.Team) {
	mine := store.Team{ID: uuid.New(), Slug: "mine", Name: "Mine"}
	other := store.Team{ID: uuid.New(), Slug: "neighbour", Name: "The neighbour"}
	return &teamStore{
		all:  []store.Team{mine, other},
		byID: map[uuid.UUID]store.Team{mine.ID: mine, other.ID: other},
	}, mine
}

// A pinned caller reads its own team, THROUGH `TeamByID` — the scope is the `WHERE id = $1` of the
// query, not a filter applied to a full list that was read anyway.
//
// MUTATION: remove the `pinned != uuid.Nil` branch from ListTeams — this test goes red, both on
// the number of teams returned and on `listed`.
func TestListTeamsScopesAPinnedCallerToItsOwnTeam(t *testing.T) {
	st, mine := newTeamStore()
	svc := New(st)

	teams, err := svc.ListTeams(context.Background(), mine.ID)
	if err != nil {
		t.Fatalf("ListTeams: %v", err)
	}

	if len(teams) != 1 || teams[0].ID != mine.ID {
		t.Fatalf("teams = %+v, want the single team %s — the caller reads outside its boundary",
			teams, mine.ID)
	}
	if st.gotID != mine.ID {
		t.Errorf("TeamByID called with %s, want %s", st.gotID, mine.ID)
	}
	if st.listed {
		t.Error("the unscoped ListTeams query was run: the whole installation was read, then filtered")
	}
}

// COUNTER-PROOF: an unpinned caller — the global admin, the only shape the database can issue
// today — still reads the installation. Without this case, a service returning nothing at all
// would pass the test above.
func TestListTeamsLeavesAnUnpinnedCallerUnbounded(t *testing.T) {
	st, _ := newTeamStore()
	svc := New(st)

	teams, err := svc.ListTeams(context.Background(), uuid.Nil)
	if err != nil {
		t.Fatalf("ListTeams: %v", err)
	}

	if len(teams) != len(st.all) {
		t.Errorf("teams = %d, want %d: the global admin can no longer administer the installation",
			len(teams), len(st.all))
	}
	if !st.listed {
		t.Error("the unscoped query was not run for an unpinned caller")
	}
}
