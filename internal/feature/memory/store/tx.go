package store

import (
	"context"
	"errors"
	"fmt"
)

// WithTx runs fn inside a transaction and only commits if fn succeeds.
//
// fn receives a Store that shares the transaction: the service still only ever sees an interface,
// and *sql.DB leaks nowhere above this layer.
//
// The deferred Rollback is unconditional: after a successful Commit it has no effect, and on any
// error path — a panic included — it releases the transaction.
//
// Nesting is refused rather than absorbed, for the same reason as in the task store: a second
// transaction takes another connection from the pool, and that connection would wait on the lock
// this one already holds on the project row (ChargeProjectMemoryBytes). Silently joining the
// existing transaction would be worse still — the outer call would commit the writes of an inner
// one whose error it swallowed.
func (s *store) WithTx(ctx context.Context, fn func(Store) error) error {
	if s.inTx {
		return errors.New("memory store: nested transaction")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory store: opening transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(&store{q: s.q.WithTx(tx), db: s.db, inTx: true}); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory store: commit: %w", err)
	}
	return nil
}
