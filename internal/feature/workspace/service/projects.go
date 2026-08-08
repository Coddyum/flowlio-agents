package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément               | Résumé                                                 | Ligne |
// |-----------------------|--------------------------------------------------------|-------|
// | service.CreateProject | Validates then creates a project, linked to its peers   | 36    |
// | service.ListProjects  | Lists a team's projects                                 | 59    |
// | service.DeleteProject | Removes a repo, unless a sibling still holds a thread   | 78    |
// | ProjectInUseError.Error  | The sentence the customer reads on the refusal       | 111   |
// | ProjectInUseError.Unwrap | Exposes the sentinel behind the detailed refusal     | 129   |
// | toProject             | Projects a store project onto the API view              | 134   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"fmt"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/google/uuid"
)

// CreateProject validates the key and the name, then creates the project in the team provided.
// The key is normalised to uppercase: `frnt` and `FRNT` name the same project.
//
// The repo arrives CONNECTED: the same statement opens a trust edge towards every repo already in
// the team, so `create_issue` works from the newcomer to its peers and back at the first gesture.
// Before that, a fresh repo could talk to nobody and the refusal was a 404 with no cause attached.
//
// There is nothing to read here about how that happens, and that is the design: the edges are
// written by the query behind store.CreateProject, never by this service. Naming the table in Go
// would be the trust decision leaving the query — refused by scripts/check-trust-in-sql-only.sh.
func (s *service) CreateProject(ctx context.Context, in CreateProjectInput) (Project, error) {
	key := strings.ToUpper(strings.TrimSpace(in.Key))
	name := strings.TrimSpace(in.Name)

	if in.TeamID == uuid.Nil {
		return Project{}, ErrInvalidInput
	}
	if err := validateKey(key); err != nil {
		return Project{}, err
	}
	if err := validateName("project name", name); err != nil {
		return Project{}, err
	}

	created, err := s.store.CreateProject(ctx, in.TeamID, key, name)
	if err != nil {
		return Project{}, translateStore(err, "create project "+key)
	}
	return toProject(created), nil
}

// ListProjects lists a team's projects. This is the only cross-project view a project token can
// reach: the metadata of the sibling repos, never their content.
func (s *service) ListProjects(ctx context.Context, teamID uuid.UUID) ([]Project, error) {
	rows, err := s.store.ListProjects(ctx, teamID)
	if err != nil {
		return nil, translateStore(err, "list projects")
	}

	projects := make([]Project, 0, len(rows))
	for _, row := range rows {
		projects = append(projects, toProject(row))
	}
	return projects, nil
}

// DeleteProject removes a repo, and refuses while a sibling repo holds a thread with it.
//
// THE SUCCESS IS ASSERTED, NOT DEDUCED. "No blocker" and "the row was removed" are two different
// statements, and answering 204 on the first would turn a delete that matched nothing into a
// silent success. The store reports the removal itself, and a store that reports neither a
// deletion nor a reason is an internal inconsistency: it becomes a 500 with a log, never a 204.
func (s *service) DeleteProject(ctx context.Context, teamID, projectID uuid.UUID) error {
	if teamID == uuid.Nil || projectID == uuid.Nil {
		return ErrInvalidInput
	}

	outcome, err := s.store.DeleteProject(ctx, teamID, projectID)
	if err != nil {
		return translateStore(err, "delete project "+projectID.String())
	}

	if len(outcome.Blockers) > 0 {
		holders := make([]ThreadHolder, 0, len(outcome.Blockers))
		for _, blocker := range outcome.Blockers {
			holders = append(holders, ThreadHolder{Key: blocker.Key, Threads: blocker.Threads})
		}
		return &ProjectInUseError{Holders: holders}
	}

	if !outcome.Deleted {
		return fmt.Errorf("workspace service: delete project %s: nothing blocked the deletion and "+
			"no row was removed", projectID)
	}
	return nil
}

// Error is the sentence the customer reads. It names every sibling and how many threads each holds,
// then says what to do INSTEAD — "no" on its own would leave a human with a repo they cannot retire
// and no next move.
//
// The advice is deliberately not "close the threads first": nothing in this product deletes an
// issue, and a closed thread still carries the sibling's words, so it would be advice that cannot
// be acted on. Revoking the tokens and denying the trust edges is what actually silences a repo,
// and both commands exist today.
func (e *ProjectInUseError) Error() string {
	holders := make([]string, 0, len(e.Holders))
	for _, holder := range e.Holders {
		unit := "threads"
		if holder.Threads == 1 {
			unit = "thread"
		}
		holders = append(holders, fmt.Sprintf("%s (%d %s)", holder.Key, holder.Threads, unit))
	}

	return "this repo still holds questions with " + strings.Join(holders, ", ") +
		", and deleting it would erase those threads from their side too. " +
		"Retire it instead: revoke its tokens, then deny its trust edges."
}

// Unwrap exposes the sentinel, so a caller matches the CASE with errors.Is while the handler reads
// the DETAIL with errors.As. Without it, the only way to recognise this refusal would be to compare
// its message, which is the one thing here meant to be free to change.
func (e *ProjectInUseError) Unwrap() error {
	return ErrProjectInUse
}

// toProject projects a store project onto the API view.
func toProject(p store.Project) Project {
	return Project{ID: p.ID, Key: p.Key, Name: p.Name, CreatedAt: p.CreatedAt}
}
