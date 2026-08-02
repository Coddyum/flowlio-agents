package store

import (
	"context"
	"errors"
	"fmt"
)

// WithTx exécute fn dans une transaction et ne commite que si fn réussit.
//
// L'imbrication est refusée, pas absorbée. Ouvrir une seconde transaction prendrait une autre
// connexion du pool, qui attendrait le verrou que celle-ci détient déjà sur la ligne du projet
// destinataire (réservation du numéro) : un interblocage qu'aucun test mono-thread ne révèle.
// Et rejoindre silencieusement la transaction en cours ferait committer par l'extérieur les
// écritures d'un appel interne dont l'erreur aurait été avalée.
func (s *store) WithTx(ctx context.Context, fn func(Store) error) error {
	if s.inTx {
		return errors.New("issue store: transaction imbriquée")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("issue store: ouverture de transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(&store{q: s.q.WithTx(tx), db: s.db, inTx: true}); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("issue store: commit: %w", err)
	}
	return nil
}
