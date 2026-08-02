package store

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// codeUniqueViolation, codeCheckViolation et codeForeignKeyViolation : codes SQLSTATE de
	// Postgres. La violation de clé étrangère survient quand le projet visé n'existe pas dans la
	// team — c'est-à-dire un scope invalide, pas une erreur interne.
	codeUniqueViolation     = "23505"
	codeCheckViolation      = "23514"
	codeForeignKeyViolation = "23503"

	// constraintTaskNumber protège l'unicité (project_id, number). La violer ne veut pas dire
	// que l'appelant a mal appelé : le numéro est tiré du compteur du projet, jamais fourni.
	constraintTaskNumber = "tasks_number_unique_per_project"
)

// translate ramène les erreurs de la base aux erreurs domaine du store, pour que le service
// raisonne sur des cas métier et pas sur des codes SQLSTATE.
//
// Une violation de tasks_number_unique_per_project est traitée à part : elle signifie que le
// compteur du projet a servi deux fois le même numéro, donc que les données sont incohérentes.
// La rendre en « conflit » ferait répondre 409 à un agent qui n'a rien fait de mal et qui
// réessaierait indéfiniment ; c'est une panne serveur, elle doit se voir comme telle. Miroir
// exact de issue/store/errors.go, qui portait déjà cette discrimination.
func translate(err error, op string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.ConstraintName == constraintTaskNumber {
			return errors.Join(ErrCorrupted, errors.New("task store: "+op), err)
		}
		switch pgErr.Code {
		case codeUniqueViolation, codeCheckViolation, codeForeignKeyViolation:
			return ErrConflict
		}
	}

	return errors.Join(errors.New("task store: "+op), err)
}
