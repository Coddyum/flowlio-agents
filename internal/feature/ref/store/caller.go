package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// CallerProjectKey resolves the key of the project the token is scoped to.
//
// An absent row is ErrNotFound rather than an internal error: a token pinned to a project that no
// longer exists is a tenancy fact, not a failure of this instance.
func (s *store) CallerProjectKey(ctx context.Context, teamID, projectID uuid.UUID) (string, error) {
	key, err := s.q.RefCallerProjectKey(ctx, database.RefCallerProjectKeyParams{
		ID:     projectID,
		TeamID: teamID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", errors.Join(errors.New("ref store: caller project key"), err)
	}
	return key, nil
}
