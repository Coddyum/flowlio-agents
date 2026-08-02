package store

import (
	"context"
	"fmt"
)

// WithTx exécute fn dans une transaction et ne commite que si fn réussit.
//
// fn reçoit un Store qui partage la transaction : le service continue de ne voir qu'une
// interface, et *sql.DB ne fuite nulle part au-dessus de cette couche.
//
// Le Rollback différé est inconditionnel : après un Commit réussi il est sans effet, et sur
// n'importe quel chemin d'erreur — y compris une panique — il libère la transaction.
func (s *store) WithTx(ctx context.Context, fn func(Store) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("task store: ouverture de transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(&store{q: s.q.WithTx(tx), db: s.db}); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("task store: commit: %w", err)
	}
	return nil
}
