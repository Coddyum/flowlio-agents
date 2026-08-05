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

func (f *fakeStore) CreateProject(_ context.Context, teamID uuid.UUID, key, name string) (store.Project, error) {
	if f.failWith != nil {
		return store.Project{}, f.failWith
	}
	project := store.Project{ID: uuid.New(), TeamID: teamID, Key: key, Name: name}
	f.projects[key] = project
	return project, nil
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
