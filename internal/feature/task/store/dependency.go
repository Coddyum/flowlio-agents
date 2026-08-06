package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                   | Résumé                                                | Ligne |
// |---------------------------|-------------------------------------------------------|-------|
// | store.CreateDependency    | Opens a blocking edge between two tasks                 | 41    |
// | store.ReleaseBlockerEdges | Releases the edges a task has just freed                | 68    |
// | store.ReleaseEdge         | Releases one named edge                                 | 83    |
// | store.ReleaseBodyEdge     | Releases the edge a description line had opened         | 100   |
// | store.TaskDependencies    | Returns the active edges blocking one task              | 114   |
// | store.ClearBlock          | Brings a task blocked by an edge back to `todo`         | 135   |
// | store.ActiveEdges         | Returns the project's active blocking graph             | 149   |
// | store.ProjectKey          | Resolves the readable key of the token's project        | 164   |
// | toDependency              | Projects a generated row onto the domain type           | 176   |
//
// The ReleasedEdge projection loops look alike without being factorable: sqlc generates a DISTINCT
// row type per query, and a single line of code separates them.
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// CreateDependency opens an edge "in.TaskID is blocked by in.BlockerTaskID".
//
// The edge's project_id is read from the blocked task rather than supplied: that is what brings
// both endpoints into the same composite foreign key. A blocker from another project therefore
// fails the constraint in the database — the cross-repo dependency is inexpressible, not merely
// refused by the service (D42).
//
// A blocked task that is archived, or belongs to another project, is out of the query's reach and
// yields ErrNotFound. An active edge already open on the same pair yields ErrConflict: replaying
// block_task does not manufacture a second block to release.
func (s *store) CreateDependency(ctx context.Context, in NewDependency) (Dependency, error) {
	// An unnamed origin is a programming defect, and without this guard it would surface as the
	// CHECK constraint firing — a conflict, which reads as the caller's fault when it is ours.
	if in.Origin != OriginAPI && in.Origin != OriginBody {
		return Dependency{}, fmt.Errorf("%w: dependency opened with origin %q", ErrCorrupted, in.Origin)
	}

	row, err := s.q.CreateTaskDependency(ctx, database.CreateTaskDependencyParams{
		TaskID:        in.TaskID,
		BlockerTaskID: in.BlockerTaskID,
		TeamID:        in.TeamID,
		ProjectID:     in.ProjectID,
		UntilStatus:   database.TaskStatus(in.UntilStatus),
		SetBlocked:    in.SetBlocked,
		Origin:        in.Origin,
	})
	if err != nil {
		return Dependency{}, translate(err, "create dependency")
	}
	return toDependency(row), nil
}

// ReleaseBlockerEdges releases the edges blockerTaskID has just freed by reaching blockerStatus,
// and returns the ones that were.
//
// force ignores the release condition: an archived blocker will never reach anything, and leaving
// its edges in place would manufacture tasks nothing can ever unblock.
func (s *store) ReleaseBlockerEdges(ctx context.Context, projectID, blockerTaskID uuid.UUID, blockerStatus string, force bool) ([]uuid.UUID, error) {
	freed, err := s.q.ReleaseDependenciesOfBlocker(ctx, database.ReleaseDependenciesOfBlockerParams{
		BlockerTaskID: blockerTaskID,
		ProjectID:     projectID,
		BlockerStatus: database.TaskStatus(blockerStatus),
		Force:         force,
	})
	if err != nil {
		return nil, translate(err, "release blocker edges")
	}
	return freed, nil
}

// ReleaseEdge releases one named edge and returns the freed task. A missing or already-released
// edge returns an empty list rather than an error: to the caller, both are the same non-event.
func (s *store) ReleaseEdge(ctx context.Context, projectID, taskID, blockerTaskID uuid.UUID) ([]uuid.UUID, error) {
	freed, err := s.q.ReleaseDependencyPair(ctx, database.ReleaseDependencyPairParams{
		TaskID:        taskID,
		BlockerTaskID: blockerTaskID,
		ProjectID:     projectID,
	})
	if err != nil {
		return nil, translate(err, "release edge")
	}
	return freed, nil
}

// ReleaseBodyEdge releases the edge a `#blocked-by` line had opened, and only that one.
//
// An empty list covers two cases that are the same non-event here: the pair carries no active edge
// at all, or it carries one this surface did not open. Deleting a line never lifts a block decided
// through block_task (D47).
func (s *store) ReleaseBodyEdge(ctx context.Context, projectID, taskID, blockerTaskID uuid.UUID) ([]uuid.UUID, error) {
	freed, err := s.q.ReleaseBodyDependencyPair(ctx, database.ReleaseBodyDependencyPairParams{
		TaskID:        taskID,
		BlockerTaskID: blockerTaskID,
		ProjectID:     projectID,
	})
	if err != nil {
		return nil, translate(err, "release body edge")
	}
	return freed, nil
}

// TaskDependencies returns the active edges blocking one task, whole rows: the caller compiling a
// description needs the release condition of each, not just its endpoints.
func (s *store) TaskDependencies(ctx context.Context, projectID, taskID uuid.UUID) ([]Dependency, error) {
	rows, err := s.q.ListTaskActiveDependencies(ctx, database.ListTaskActiveDependenciesParams{
		ProjectID: projectID,
		TaskID:    taskID,
	})
	if err != nil {
		return nil, translate(err, "task dependencies")
	}

	deps := make([]Dependency, 0, len(rows))
	for _, row := range rows {
		deps = append(deps, toDependency(row))
	}
	return deps, nil
}

// ClearBlock brings the task back from `blocked` to `todo`. All three conditions — status still
// blocked, no active edge left, at least one edge having set the block — live in the query.
//
// false means "nothing to change" rather than a failure: that is the nominal case of a task
// blocked by an agent for another reason, which gets notified without anyone deciding for it.
func (s *store) ClearBlock(ctx context.Context, teamID, projectID, taskID uuid.UUID) (bool, error) {
	rows, err := s.q.ClearTaskBlock(ctx, database.ClearTaskBlockParams{
		TaskID:    taskID,
		TeamID:    teamID,
		ProjectID: projectID,
	})
	if err != nil {
		return false, translate(err, "clear block")
	}
	return len(rows) > 0, nil
}

// ActiveEdges returns the project's unreleased blocking graph, for the cycle-detection traversal.
// Only the two endpoints cross the network: that is all the traversal needs.
func (s *store) ActiveEdges(ctx context.Context, projectID uuid.UUID) ([]Edge, error) {
	rows, err := s.q.ListActiveDependencyEdges(ctx, projectID)
	if err != nil {
		return nil, translate(err, "active edges")
	}

	edges := make([]Edge, 0, len(rows))
	for _, row := range rows {
		edges = append(edges, Edge{TaskID: row.TaskID, BlockerTaskID: row.BlockerTaskID})
	}
	return edges, nil
}

// ProjectKey returns the readable key of the project, scoped by team like every project read even
// though both identifiers already come from the token.
func (s *store) ProjectKey(ctx context.Context, teamID, projectID uuid.UUID) (string, error) {
	key, err := s.q.TaskProjectKey(ctx, database.TaskProjectKeyParams{
		ID:     projectID,
		TeamID: teamID,
	})
	if err != nil {
		return "", translate(err, "project key")
	}
	return key, nil
}

// toDependency projects a generated row onto the domain type.
func toDependency(row database.TaskDependency) Dependency {
	return Dependency{
		ID:            row.ID,
		ProjectID:     row.ProjectID,
		TaskID:        row.TaskID,
		BlockerTaskID: row.BlockerTaskID,
		UntilStatus:   string(row.UntilStatus),
		SetBlocked:    row.SetBlocked,
		Origin:        row.Origin,
		CreatedAt:     row.CreatedAt,
		ReleasedAt:    fromNullTime(row.ReleasedAt),
	}
}
