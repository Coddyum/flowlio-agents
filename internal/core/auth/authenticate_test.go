package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-ia/internal/pkg/crypto"
	"github.com/google/uuid"
)

// fakeStore substitue la base : les règles d'authentification se testent sans Postgres.
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
			name:   "token admin sans team",
			record: TokenRecord{ID: uuid.New(), Scope: ScopeAdmin, SecretHash: token.Hash},
			found:  true, raw: token.Plain,
		},
		{name: "préfixe inconnu", found: false, raw: token.Plain, wantErr: true},
		{name: "token malformé", record: valid, found: true, raw: "pas-un-token", wantErr: true},
		{
			name:   "secret erroné",
			record: TokenRecord{ID: uuid.New(), Scope: ScopeProject, TeamID: teamID, ProjectID: projectID, SecretHash: crypto.HashSecret("autre")},
			found:  true, raw: token.Plain, wantErr: true,
		},
		{
			name: "token révoqué",
			record: TokenRecord{
				ID: valid.ID, Scope: ScopeProject, TeamID: teamID, ProjectID: projectID,
				SecretHash: token.Hash, Revoked: true,
			},
			found: true, raw: token.Plain, wantErr: true,
		},
		{
			name: "token de projet sans scope complet",
			record: TokenRecord{
				ID: valid.ID, Scope: ScopeProject, SecretHash: token.Hash,
			},
			found: true, raw: token.Plain, wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{record: tc.record, found: tc.found}
			svc := New(store)

			principal, err := svc.Authenticate(context.Background(), tc.raw)

			if tc.wantErr {
				if !errors.Is(err, ErrUnauthenticated) {
					t.Fatalf("erreur = %v, attendu ErrUnauthenticated", err)
				}
				if principal != (Principal{}) {
					t.Errorf("principal non vide en cas d'échec: %+v", principal)
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

// Tous les échecs doivent être indiscernables : c'est ce qui empêche d'énumérer les tokens.
func TestAuthenticateFailuresAreIndistinguishable(t *testing.T) {
	token, err := crypto.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	unknown := New(&fakeStore{found: false})
	wrongSecret := New(&fakeStore{found: true, record: TokenRecord{
		ID: uuid.New(), Scope: ScopeAdmin, SecretHash: crypto.HashSecret("autre"),
	}})

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

	t.Run("usage récent : pas d'écriture", func(t *testing.T) {
		store := &fakeStore{found: true, record: TokenRecord{
			ID: uuid.New(), Scope: ScopeAdmin, SecretHash: token.Hash,
			LastUsedAt: time.Now(),
		}}
		if _, err := New(store).Authenticate(context.Background(), token.Plain); err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if store.touched != 0 {
			t.Errorf("touched = %d, attendu 0", store.touched)
		}
	})

	t.Run("usage ancien : une écriture", func(t *testing.T) {
		store := &fakeStore{found: true, record: TokenRecord{
			ID: uuid.New(), Scope: ScopeAdmin, SecretHash: token.Hash,
			LastUsedAt: time.Now().Add(-time.Hour),
		}}
		if _, err := New(store).Authenticate(context.Background(), token.Plain); err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if store.touched != 1 {
			t.Errorf("touched = %d, attendu 1", store.touched)
		}
	})
}
