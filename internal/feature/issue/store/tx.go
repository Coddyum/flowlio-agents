package store

import (
	"context"
	"errors"
	"fmt"
)

// WithTx runs fn inside a transaction and only commits if fn succeeds.
//
// Nesting is refused, not absorbed. Opening a second transaction would take another connection
// from the pool, which would wait on the lock this one already holds on the recipient project's row
// (the number reservation): a deadlock no single-threaded test reveals. And silently joining the
// transaction in progress would have the outside commit the writes of an inner call whose error was
// swallowed.
func (s *store) WithTx(ctx context.Context, fn func(Store) error) error {
	if s.inTx {
		return errors.New("issue store: nested transaction")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("issue store: opening transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(&store{q: s.q.WithTx(tx), db: s.db, cache: s.cache, inTx: true}); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("issue store: commit: %w", err)
	}
	return nil
}
