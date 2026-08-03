package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément              | Résumé                                                  | Ligne |
// |----------------------|---------------------------------------------------------|-------|
// | store.AllowTrust     | Ouvre une paire de confiance, idempotente                | 31    |
// | store.RevokeTrust    | Ferme une paire de confiance, idempotente                | 48    |
// | store.ListTrustEdges | Liste le graphe d'une team, en clés lisibles             | 62    |
//
// Fin du sommaire.
// =====================================================================
//
// Ce fichier ne NOMME jamais la table du graphe : il appelle les queries générées. La décision de
// confiance vit dans le WHERE de CreateIssue, et l'administration dans sql/queries/trust.sql. Un
// `.go` qui aurait besoin du nom de la table serait le signe que la décision a quitté la query —
// c'est ce que garde scripts/check-trust-in-sql-only.sh, et c'est pourquoi ce commentaire-ci
// n'écrit pas ce nom non plus : une règle absolue s'applique, une règle à exceptions se négocie.

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// AllowTrust ouvre une paire, dans les deux sens puisque l'arête est symétrique.
//
// created distingue « créée » de « déjà autorisée » sans second aller-retour. Une clé inconnue,
// ou d'une autre team, ne se résout pas : la query rend zéro ligne, donc ErrNotFound.
func (s *store) AllowTrust(ctx context.Context, teamID uuid.UUID, firstKey, secondKey string) (bool, error) {
	created, err := s.q.AllowTrust(ctx, database.AllowTrustParams{
		TeamID:    teamID,
		FirstKey:  firstKey,
		SecondKey: secondKey,
	})
	if err != nil {
		return false, translate(err, "allow trust "+firstKey+" ↔ "+secondKey)
	}
	return created, nil
}

// RevokeTrust ferme une paire. removed vaut faux si la paire existait mais n'était pas déclarée ;
// une clé qui ne se résout pas remonte en ErrNotFound, pas en « rien à retirer ».
//
// Retirer une confiance n'interdit que d'OUVRIR une nouvelle issue. Les fils déjà ouverts restent
// répondables : le coupe-circuit du produit est la révocation de token.
func (s *store) RevokeTrust(ctx context.Context, teamID uuid.UUID, firstKey, secondKey string) (bool, error) {
	removed, err := s.q.RevokeTrust(ctx, database.RevokeTrustParams{
		TeamID:    teamID,
		FirstKey:  firstKey,
		SecondKey: secondKey,
	})
	if err != nil {
		return false, translate(err, "revoke trust "+firstKey+" ↔ "+secondKey)
	}
	return removed, nil
}

// ListTrustEdges rend le graphe d'une team en clés lisibles, trié. Lecture d'administration : elle
// n'est jamais servie à un token de projet.
func (s *store) ListTrustEdges(ctx context.Context, teamID uuid.UUID) ([]TrustEdge, error) {
	rows, err := s.q.ListTrustEdges(ctx, teamID)
	if err != nil {
		return nil, translate(err, "list trust edges")
	}

	edges := make([]TrustEdge, 0, len(rows))
	for _, row := range rows {
		edges = append(edges, TrustEdge{
			FirstKey:  row.FirstKey,
			SecondKey: row.SecondKey,
			CreatedAt: row.CreatedAt,
		})
	}
	return edges, nil
}
