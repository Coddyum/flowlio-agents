package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément   | Résumé                                                             | Ligne |
// |-----------|--------------------------------------------------------------------|-------|
// | translate | Ramène une erreur Postgres à une erreur domaine du store            | 37    |
// | isState   | Indique si une chaîne est un état d'issue connu                     | 64    |
//
// Fin du sommaire.
// =====================================================================

import (
	"database/sql"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	codeUniqueViolation     = "23505"
	codeCheckViolation      = "23514"
	codeForeignKeyViolation = "23503"

	// constraintIssueNumber protège l'unicité (project_id, number). La violer ne veut pas dire
	// « conflit d'appelant » mais « compteur du projet corrompu » : voir translate.
	constraintIssueNumber = "issues_number_unique_per_project"
)

// translate ramène les erreurs de la base aux erreurs domaine du store.
//
// Une violation de issues_number_unique_per_project est traitée à part : elle signifie que le
// compteur du projet a servi deux fois le même numéro, donc que les données sont incohérentes.
// La rendre en « conflit » ferait répondre 409 à un agent qui n'a rien fait de mal et qui
// réessaierait indéfiniment ; c'est une panne serveur, elle doit se voir comme telle.
func translate(err error, op string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.ConstraintName == constraintIssueNumber {
			return errors.Join(ErrCorrupted, errors.New("issue store: "+op), err)
		}
		switch pgErr.Code {
		case codeUniqueViolation, codeCheckViolation, codeForeignKeyViolation:
			return ErrConflict
		}
	}

	return errors.Join(errors.New("issue store: "+op), err)
}

// states est le vocabulaire des états d'issue, identique à l'enum issue_state de la migration.
var states = []string{"open", "answered", "closed"}

// isState indique si une chaîne est un état connu. Le store le vérifie avant d'envoyer la valeur
// en base : un état inconnu produirait une erreur de cast Postgres, pas un filtre vide.
func isState(state string) bool {
	return slices.Contains(states, state)
}
