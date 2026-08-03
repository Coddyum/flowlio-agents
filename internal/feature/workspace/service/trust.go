package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément              | Résumé                                                  | Ligne |
// |----------------------|---------------------------------------------------------|-------|
// | service.AllowTrust   | Ouvre une paire de confiance entre deux projets          | 43    |
// | service.RevokeTrust  | Ferme une paire de confiance entre deux projets          | 61    |
// | service.ListTrust    | Rend le graphe de confiance d'une team                   | 78    |
// | normalisePair        | Valide et normalise les deux clés d'une paire            | 105   |
//
// Fin du sommaire.
// =====================================================================
//
// CE FICHIER NE DÉCIDE D'AUCUNE AUTORISATION.
//
// Il édite une déclaration ; c'est le prédicat du WHERE de CreateIssue (sql/queries/issues.sql)
// qui l'applique, et lui seul. La seule validation ici est celle de deux chaînes tapées par un
// humain — la tenancy vit dans la query, où elle ne peut pas être contournée par un appelant qui
// atteindrait le store directement.

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// errSelfPair est le message rendu à un humain qui autorise un projet avec lui-même.
//
// C'est de la validation d'entrée, pas de la tenancy : la base refuserait de toute façon
// (project_trust_ordered exclut l'égalité), mais elle rendrait un 500 ou un `not found` là où un
// 400 lisible dit ce qu'il faut faire. Un projet qui se pose une question à lui-même n'a pas
// besoin du canal inter-projets : il a des tâches.
var errSelfPair = errors.New(
	"un projet ne peut pas s'autoriser lui-même — une question à son propre repo est une tâche")

// AllowTrust ouvre une paire. Idempotente : rejouer la commande rend Changed à faux.
//
// Une clé inconnue, ou d'une autre team, remonte en ErrNotFound depuis la query — jamais depuis
// un contrôle écrit ici, qui devrait pour cela résoudre les clés lui-même.
func (s *service) AllowTrust(ctx context.Context, in TrustPairInput) (TrustDecision, error) {
	first, second, err := normalisePair(in)
	if err != nil {
		return TrustDecision{}, err
	}

	created, err := s.store.AllowTrust(ctx, in.TeamID, first, second)
	if err != nil {
		return TrustDecision{}, translateStore(err, "allow trust "+first+" "+second)
	}
	return TrustDecision{First: first, Second: second, Changed: created}, nil
}

// RevokeTrust ferme une paire. Idempotente : rejouer la commande rend Changed à faux.
//
// Retirer une confiance interdit d'OUVRIR une nouvelle issue, et rien d'autre. Les fils déjà
// ouverts restent lisibles et répondables : ce n'est pas un outil de confinement, c'est une
// déclaration de moindre privilège. Le coupe-circuit du produit est la révocation de token.
func (s *service) RevokeTrust(ctx context.Context, in TrustPairInput) (TrustDecision, error) {
	first, second, err := normalisePair(in)
	if err != nil {
		return TrustDecision{}, err
	}

	removed, err := s.store.RevokeTrust(ctx, in.TeamID, first, second)
	if err != nil {
		return TrustDecision{}, translateStore(err, "revoke trust "+first+" "+second)
	}
	return TrustDecision{First: first, Second: second, Changed: removed}, nil
}

// ListTrust rend le graphe d'une team, trié par clés.
//
// C'est la seule surface où la vérité du graphe est lisible, et la première commande que tape un
// humain dont un agent vient de recevoir `not found` sur un create_issue.
func (s *service) ListTrust(ctx context.Context, teamID uuid.UUID) ([]TrustEdge, error) {
	if teamID == uuid.Nil {
		return nil, ErrInvalidInput
	}

	rows, err := s.store.ListTrustEdges(ctx, teamID)
	if err != nil {
		return nil, translateStore(err, "list trust")
	}

	edges := make([]TrustEdge, 0, len(rows))
	for _, row := range rows {
		edges = append(edges, TrustEdge{
			First:     row.FirstKey,
			Second:    row.SecondKey,
			CreatedAt: row.CreatedAt,
		})
	}
	return edges, nil
}

// normalisePair valide et normalise les deux clés. Majuscules, comme partout : `frnt` et `FRNT`
// désignent le même projet, et laisser la casse décider de l'existence d'une arête produirait
// deux graphes pour une seule intention.
//
// La comparaison d'égalité a lieu APRÈS normalisation : sans ça, `trust allow frnt FRNT` passerait
// la validation pour être refusé par la base.
func normalisePair(in TrustPairInput) (string, string, error) {
	if in.TeamID == uuid.Nil {
		return "", "", ErrInvalidInput
	}

	first := strings.ToUpper(strings.TrimSpace(in.First))
	second := strings.ToUpper(strings.TrimSpace(in.Second))

	if err := validateKey(first); err != nil {
		return "", "", err
	}
	if err := validateKey(second); err != nil {
		return "", "", err
	}
	if first == second {
		return "", "", errors.Join(ErrInvalidInput, errSelfPair)
	}
	return first, second, nil
}
