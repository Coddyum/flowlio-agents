package bootstrap

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                | Résumé                                                 | Ligne |
// |------------------------|--------------------------------------------------------|-------|
// | Store                  | Contrat minimal nécessaire à l'amorçage                  | 35    |
// | store                  | Implémentation adossée aux queries générées              | 41    |
// | NewStore               | Crée le store d'amorçage                                 | 46    |
// | store.CountTokens      | Compte les tokens existants                              | 51    |
// | store.CreateAdminToken | Insère le token d'administration initial                 | 60    |
// | EnsureAdminToken       | Crée le token admin au tout premier démarrage local      | 77    |
//
// Fin du sommaire.
// =====================================================================
//
// Mode local : ni compte, ni mot de passe. Au tout premier démarrage, le serveur émet un unique
// token d'administration, l'écrit dans le fichier d'identifiants de l'utilisateur et l'affiche
// une seule fois. C'est ce token que la CLI utilise pour créer la première team.
//
// En mode hosted, cette amorce n'est jamais exécutée : les tokens admin y découlent d'un compte.

import (
	"context"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/pkg/crypto"
)

// adminTokenName identifie le token créé à l'amorçage.
const adminTokenName = "local bootstrap"

// Store est le contrat minimal de l'amorçage : compter les tokens, en créer un.
type Store interface {
	CountTokens(ctx context.Context) (int64, error)
	CreateAdminToken(ctx context.Context, name, prefix, hash string) error
}

// store adosse le contrat aux queries générées.
type store struct {
	q *database.Queries
}

// NewStore crée le store d'amorçage.
func NewStore(q *database.Queries) Store {
	return &store{q: q}
}

// CountTokens compte les tokens existants, toutes portées confondues.
func (s *store) CountTokens(ctx context.Context) (int64, error) {
	n, err := s.q.CountTokens(ctx)
	if err != nil {
		return 0, fmt.Errorf("bootstrap store: count tokens: %w", err)
	}
	return n, nil
}

// CreateAdminToken insère le token d'administration initial.
func (s *store) CreateAdminToken(ctx context.Context, name, prefix, hash string) error {
	_, err := s.q.CreateAdminToken(ctx, database.CreateAdminTokenParams{
		Name:       name,
		Prefix:     prefix,
		SecretHash: hash,
	})
	if err != nil {
		return fmt.Errorf("bootstrap store: create admin token: %w", err)
	}
	return nil
}

// EnsureAdminToken émet le token d'administration si et seulement si la base n'en contient
// aucun. Le secret renvoyé n'existe qu'ici : il n'est pas relisible ensuite.
//
// Le second retour indique si un token vient d'être créé ; false signifie que l'installation
// était déjà amorcée et qu'il n'y a rien à afficher.
func EnsureAdminToken(ctx context.Context, st Store) (string, bool, error) {
	count, err := st.CountTokens(ctx)
	if err != nil {
		return "", false, err
	}
	if count > 0 {
		return "", false, nil
	}

	token, err := crypto.NewToken()
	if err != nil {
		return "", false, fmt.Errorf("bootstrap: génération du token admin: %w", err)
	}

	if err := st.CreateAdminToken(ctx, adminTokenName, token.Prefix, token.Hash); err != nil {
		return "", false, err
	}

	return token.Plain, true, nil
}
