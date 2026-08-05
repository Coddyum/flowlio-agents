package issue

// WHAT THIS FILE IS. The module's second surface: what `issue` offers to the FeatureRegistry, the
// way module.go declares what it offers over HTTP. Both are adapters onto the same service.
//
// IT IS NOT A HANDLER AND IT IS NOT A SERVICE. No business logic here, ever — see the same note
// in task/provider.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/feature/issue/service"
)

// ResolveIssueRef implements module.IssueRefResolver: it answers what CORE-34 is, when it turns
// out to be an issue the caller takes part in.
//
// It reuses GetIssue, so the reference path and the HTTP path read through the SAME visibility
// clause. The project key comes from the reference and is therefore caller-controlled; it opens
// nothing — the scope is decided on TeamID and ProjectID, which come from the token, inside the
// query. A key naming a project the caller neither authored to nor received from is simply
// unfindable.
//
// ErrNotFound becomes ErrRefNotFound; every other error is forwarded unchanged. Here that
// distinction has a second edge: an issue out of reach is already indistinguishable from a
// missing number (docs/DESIGN-TRUST.md § Le refus indiscernable), and mapping a real failure onto
// the same answer would make an outage look like the very refusal the product promises is silent.
func (m *mod) ResolveIssueRef(ctx context.Context, scope module.RefScope, projectKey string, number int64) (json.RawMessage, error) {
	detail, err := m.svc.GetIssue(ctx, service.Ref{
		TeamID:          scope.TeamID,
		CallerProjectID: scope.ProjectID,
		ProjectKey:      projectKey,
		Number:          number,
	})
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return nil, module.ErrRefNotFound
		}
		return nil, fmt.Errorf("issue provider: resolve ref %s-%d: %w", projectKey, number, err)
	}

	body, err := json.Marshal(detail)
	if err != nil {
		return nil, fmt.Errorf("issue provider: encode issue %s-%d: %w", projectKey, number, err)
	}
	return body, nil
}
