package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/crypto"
	"github.com/google/uuid"
)

// fakeStore stands in for the database: the authentication rules are tested without Postgres.
type fakeStore struct {
	record  TokenRecord
	found   bool
	touched int
}

func (f *fakeStore) TokenByPrefix(_ context.Context, _ string) (TokenRecord, error) {
	if !f.found {
		return TokenRecord{}, ErrTokenNotFound
	}
	return f.record, nil
}

func (f *fakeStore) TouchToken(_ context.Context, _ uuid.UUID) error {
	f.touched++
	return nil
}

func TestAuthenticate(t *testing.T) {
	token, err := crypto.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	teamID, projectID := uuid.New(), uuid.New()
	valid := TokenRecord{
		ID:         uuid.New(),
		Scope:      ScopeProject,
		TeamID:     teamID,
		ProjectID:  projectID,
		SecretHash: token.Hash,
	}

	cases := []struct {
		name    string
		record  TokenRecord
		found   bool
		raw     string
		wantErr bool
	}{
		{name: "token de projet valide", record: valid, found: true, raw: token.Plain},
		{
			name:   "admin token without a team",
			record: TokenRecord{ID: uuid.New(), Scope: ScopeAdmin, SecretHash: token.Hash},
			found:  true, raw: token.Plain,
		},
		{name: "unknown prefix", found: false, raw: token.Plain, wantErr: true},
		{name: "malformed token", record: valid, found: true, raw: "not-a-token", wantErr: true},
		{
			name:   "wrong secret",
			record: TokenRecord{ID: uuid.New(), Scope: ScopeProject, TeamID: teamID, ProjectID: projectID, SecretHash: crypto.HashSecret("autre")},
			found:  true, raw: token.Plain, wantErr: true,
		},
		{
			name: "revoked token",
			record: TokenRecord{
				ID: valid.ID, Scope: ScopeProject, TeamID: teamID, ProjectID: projectID,
				SecretHash: token.Hash, Revoked: true,
			},
			found: true, raw: token.Plain, wantErr: true,
		},
		{
			name: "project token without a complete scope",
			record: TokenRecord{
				ID: valid.ID, Scope: ScopeProject, SecretHash: token.Hash,
			},
			found: true, raw: token.Plain, wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{record: tc.record, found: tc.found}
			svc := New(store, nil)

			principal, err := svc.Authenticate(context.Background(), tc.raw)

			if tc.wantErr {
				if !errors.Is(err, ErrUnauthenticated) {
					t.Fatalf("erreur = %v, attendu ErrUnauthenticated", err)
				}
				if principal != (Principal{}) {
					t.Errorf("non-empty principal on failure: %+v", principal)
				}
				return
			}

			if err != nil {
				t.Fatalf("erreur inattendue: %v", err)
			}
			if principal.TokenID != tc.record.ID {
				t.Errorf("TokenID = %v, attendu %v", principal.TokenID, tc.record.ID)
			}
			if principal.Scope != tc.record.Scope {
				t.Errorf("Scope = %v, attendu %v", principal.Scope, tc.record.Scope)
			}
		})
	}
}

// Every failure must be indistinguishable: that is what stops the tokens being enumerated.
func TestAuthenticateFailuresAreIndistinguishable(t *testing.T) {
	token, err := crypto.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	unknown := New(&fakeStore{found: false}, nil)
	wrongSecret := New(&fakeStore{found: true, record: TokenRecord{
		ID: uuid.New(), Scope: ScopeAdmin, SecretHash: crypto.HashSecret("autre"),
	}}, nil)

	_, errUnknown := unknown.Authenticate(context.Background(), token.Plain)
	_, errWrong := wrongSecret.Authenticate(context.Background(), token.Plain)

	if errUnknown.Error() != errWrong.Error() {
		t.Errorf("messages distincts: %q vs %q", errUnknown, errWrong)
	}
}

func TestTouchIsThrottled(t *testing.T) {
	token, err := crypto.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	t.Run("recent use: no write", func(t *testing.T) {
		store := &fakeStore{found: true, record: TokenRecord{
			ID: uuid.New(), Scope: ScopeAdmin, SecretHash: token.Hash,
			LastUsedAt: time.Now(),
		}}
		if _, err := New(store, nil).Authenticate(context.Background(), token.Plain); err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if store.touched != 0 {
			t.Errorf("touched = %d, attendu 0", store.touched)
		}
	})

	t.Run("old use: one write", func(t *testing.T) {
		store := &fakeStore{found: true, record: TokenRecord{
			ID: uuid.New(), Scope: ScopeAdmin, SecretHash: token.Hash,
			LastUsedAt: time.Now().Add(-time.Hour),
		}}
		if _, err := New(store, nil).Authenticate(context.Background(), token.Plain); err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if store.touched != 1 {
			t.Errorf("touched = %d, attendu 1", store.touched)
		}
	})
}
