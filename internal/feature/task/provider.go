package task

// WHAT THIS FILE IS. The module's second surface: what `task` offers to the FeatureRegistry, the
// way module.go declares what it offers over HTTP. Both are adapters onto the same service — one
// translates HTTP, this one translates a Go call from a sibling feature.
//
// IT IS NOT A HANDLER AND IT IS NOT A SERVICE. It holds no business logic and never will: every
// line below either shapes an argument or translates an error. The moment a rule about tasks
// appears here, it belongs in service/ instead — this file would then be a second place where
// task behaviour is decided, reachable only through the registry, and invisible to the HTTP tests.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
)

// ResolveTaskRef implements module.TaskRefResolver: it answers what CORE-34 is, when 34 turns out
// to be a task of the caller's own project.
//
// It reuses GetTask, so the reference path and the HTTP path read through the SAME scoped query.
// A second read path would be a second place to get tenancy right.
//
// ErrNotFound becomes ErrRefNotFound — "I own nothing under that number", which lets the caller
// try the issue side. EVERY OTHER ERROR IS FORWARDED UNCHANGED, and that distinction is the whole
// point: collapsing them would let a database outage read as a plain "not found", and the agent
// would conclude its reference does not exist.
func (m *mod) ResolveTaskRef(ctx context.Context, scope module.RefScope, number int64) (json.RawMessage, error) {
	detail, err := m.svc.GetTask(ctx, scope.TeamID, scope.ProjectID, number)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return nil, module.ErrRefNotFound
		}
		return nil, fmt.Errorf("task provider: resolve ref %d: %w", number, err)
	}

	body, err := json.Marshal(detail)
	if err != nil {
		return nil, fmt.Errorf("task provider: encode task %d: %w", number, err)
	}
	return body, nil
}
