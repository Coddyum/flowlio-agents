package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                         | Ligne |
// |---------------------|----------------------------------------------------------------|-------|
// | store.Watermark     | Reads the durable wake watermark of a project, 0 when none        | 31    |
// | store.SaveWatermark | Persists the head the probe just decided on, monotonically        | 42    |
//
// Fin du sommaire.
// =====================================================================
//
// The durable half of the wake watermark (FLWL-90). The in-memory watermark (internal/core/probe)
// suppresses a re-wake within one process; these two methods let that suppression survive a cold
// process — a restart or a Render spin-down — by seeding the memory value from Postgres on a cold
// cache and writing it back when the probe decides on a launch. Both stay off the idle path: the read
// fires only on a cache miss, the write only on the has-work path.

import (
	"context"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// Watermark reads the durable wake watermark of a project — the head the probe last decided on —
// coalesced to 0 when no decision has ever been persisted. That 0 is the correct cold-start value:
// the probe then decides the project's standing work once and writes the row, so it does not read 0
// again after the next restart.
func (s *store) Watermark(ctx context.Context, teamID, projectID uuid.UUID) (int64, error) {
	head, err := s.q.WakeWatermark(ctx, database.WakeWatermarkParams{TeamID: teamID, ProjectID: projectID})
	if err != nil {
		return 0, fmt.Errorf("wake store: watermark: team %s project %s: %w", teamID, projectID, err)
	}
	return head, nil
}

// SaveWatermark persists the head the probe just decided on for a project. The query upserts with
// GREATEST, so a concurrent probe or a lagging replica can never move the watermark backwards and
// re-wake standing work already decided on.
func (s *store) SaveWatermark(ctx context.Context, teamID, projectID uuid.UUID, head int64) error {
	if err := s.q.SaveWakeWatermark(ctx, database.SaveWakeWatermarkParams{
		TeamID:    teamID,
		ProjectID: projectID,
		Head:      head,
	}); err != nil {
		return fmt.Errorf("wake store: save watermark: team %s project %s head %d: %w", teamID, projectID, head, err)
	}
	return nil
}
